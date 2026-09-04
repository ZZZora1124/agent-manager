package background

import (
	"context"
	"time"

	"icode.baidu.com/baidu/gdp/logit"

	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/dao"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/k8s"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/resource"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/model"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/service"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util/constant"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util/envvar"
)

const (
	defaultPVCCheckInterval = 30 * time.Second
	defaultPVCBatchSize     = 100
)

type PVCCleaner struct {
	ctx           context.Context
	cancelFunc    context.CancelFunc
	checkInterval time.Duration
}

func NewPVCCleaner(checkInterval time.Duration) *PVCCleaner {
	ctx, cancel := context.WithCancel(context.Background())
	return &PVCCleaner{ctx: ctx, cancelFunc: cancel, checkInterval: checkInterval}
}

func init() {
	if envvar.IsDisableBackgroundTask() {
		return
	}
	cleaner := NewPVCCleaner(defaultPVCCheckInterval)
	cleaner.Start()
}

func (pc *PVCCleaner) Start() {
	go pc.run()
}

func (pc *PVCCleaner) run() {
	<-resource.InitDone
	ticker := time.NewTicker(pc.checkInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			pc.cleanReleasedBindings()
		case <-pc.ctx.Done():
			return
		}
	}
}

func (pc *PVCCleaner) cleanReleasedBindings() {
	ctx := context.Background()
	lockTTL := 5 * time.Minute
	lock, err := resource.RedisClient.SetNX(ctx, constant.SandboxPVCCleanLockKey, "1", lockTTL).Result()
	if err != nil {
		resource.LoggerService.Error(ctx, "set pvc clean lock failed", logit.String("error", err.Error()))
		return
	}
	if !lock {
		return
	}
	defer resource.RedisClient.Del(ctx, constant.SandboxPVCCleanLockKey)

	// 修复不一致：sandbox 已关闭（kill/expire_kill）但 binding 仍是 active 的情况。
	// 这类不一致会导致 PVC 永远不会进入回收窗口。
	pc.reconcileActiveBindingsForClosedSandboxes(ctx)

	// 在同一把锁内清理迁移完成后的旧 PVC，避免多副本并发
	pc.cleanMigratedOldPVCs(ctx)

	var bindings []*model.SandboxPVCBinding
	err = resource.OrmMain.WithContext(ctx).
		Where("status = ? AND deleted = 0 AND reclaim_after IS NOT NULL AND reclaim_after <= ?",
			model.SandboxPVCStatusKill,
			time.Now(),
		).
		Limit(defaultPVCBatchSize).
		Find(&bindings).Error
	if err != nil {
		resource.LoggerService.Error(ctx, "query reclaimable pvc bindings failed", logit.String("error", err.Error()))
		return
	}

	sandboxRuntimeDao := dao.Use(resource.OrmMain).SandboxRuntime
	for _, binding := range bindings {
		if binding == nil {
			continue
		}
		runtime, runtimeErr := sandboxRuntimeDao.Where(sandboxRuntimeDao.SandboxUUID.Eq(binding.SandboxUUID)).First()
		if runtimeErr == nil {
			if runtime.Status == constant.StatusRunning || runtime.Status == constant.StatusPending {
				continue
			}
		}

		// PVC 到期，直接进行资源清理；sandbox 继续保持 kill/expire_kill，不再迁移到 released。

		cluster := resource.ClusterManager.GetClusterByName(binding.Cluster)
		if cluster == nil {
			_ = resource.OrmMain.WithContext(ctx).Model(&model.SandboxPVCBinding{}).Where("id = ?", binding.ID).
				Updates(map[string]any{"status": model.SandboxPVCStatusFailed, "update_time": time.Now()}).Error
			continue
		}
		// 当前约束：同集群只使用一个 namespace（cluster.namespace）。
		// 若未来引入多 namespace，需要把 delete/get PVC 的 namespace 显式化，并使用 binding.Namespace。
		if binding.Namespace != "" && binding.Namespace != cluster.GetNamespace() {
			resource.LoggerService.Warning(ctx, "sandbox pvc binding namespace mismatch (single-namespace mode)",
				logit.String("sandboxID", binding.SandboxUUID),
				logit.String("cluster", binding.Cluster),
				logit.String("bindingNamespace", binding.Namespace),
				logit.String("clusterNamespace", cluster.GetNamespace()))
		}

		if err = cluster.DeletePVC(ctx, binding.PVCName); err != nil {
			_ = resource.OrmMain.WithContext(ctx).Model(&model.SandboxPVCBinding{}).Where("id = ?", binding.ID).
				Updates(map[string]any{"status": model.SandboxPVCStatusFailed, "update_time": time.Now()}).Error
			continue
		}
		// BOS 是静态供给（手动创建 PV），需要手动删除 PV；CFS/local-path/hp1 是动态供给，provisioner 根据 reclaimPolicy:Delete 自动清理
		if service.IsBOSStorageClass(binding.StorageClass) {
			pvName := k8s.BuildBOSPVName(binding.SandboxUUID)
			if err = cluster.DeletePV(ctx, pvName); err != nil {
				resource.LoggerService.Warning(ctx, "delete BOS PV failed after pvc deleted",
					logit.String("sandboxID", binding.SandboxUUID),
					logit.String("pvName", pvName),
					logit.String("error", err.Error()))
			}
		}

		// 成功删除 PVC/PV 后，标记 binding 为终态
		_ = resource.OrmMain.WithContext(ctx).Model(&model.SandboxPVCBinding{}).Where("id = ?", binding.ID).
			Updates(map[string]any{
				"status":        model.SandboxPVCStatusReleased,
				"deleted":       true,
				"reclaim_after": nil,
				"update_time":   time.Now(),
			}).Error
	}
}

// cleanMigratedOldPVCs 清理跨存储迁移完成超过 2 天的旧 local-path PVC。
// 迁移完成时旧 PVC 被保留作为安全兜底，2 天后确认无问题再自动清理。
func (pc *PVCCleaner) cleanMigratedOldPVCs(ctx context.Context) {
	const migratedPVCRetentionDays = 2

	// 查询已完成(done)的跨存储迁移记录，且 end_time 超过 2 天
	cutoff := time.Now().AddDate(0, 0, -migratedPVCRetentionDays)
	var migrations []*model.SandboxPVCMigration
	if err := resource.OrmMain.WithContext(ctx).
		Where("migration_type = ? AND status = ? AND end_time IS NOT NULL AND end_time <= ?",
			model.PVCMigrationTypeCrossStorage,
			model.PVCMigrationStatusDone,
			cutoff,
		).
		Limit(defaultPVCBatchSize).
		Find(&migrations).Error; err != nil {
		resource.LoggerService.Error(ctx, "query migrated old PVCs failed",
			logit.String("error", err.Error()))
		return
	}

	for _, m := range migrations {
		if m == nil {
			continue
		}

		cluster := resource.ClusterManager.GetClusterByName(m.Cluster)
		if cluster == nil {
			resource.LoggerService.Warning(ctx, "cluster not found for migrated PVC cleanup",
				logit.String("sandboxUUID", m.SandboxUUID),
				logit.String("cluster", m.Cluster))
			continue
		}

		// 删除前校验 binding 当前是否仍指向旧 PVC（可能被运维手动回滚）
		// 如果 binding 已被回滚到旧 PVC，跳过删除以避免数据丢失
		if m.SourcePVCName != "" {
			var currentBinding model.SandboxPVCBinding
			if err := resource.OrmMain.WithContext(ctx).
				Where("sandbox_uuid = ? AND deleted = 0", m.SandboxUUID).
				First(&currentBinding).Error; err == nil {
				if currentBinding.PVCName == m.SourcePVCName {
					resource.LoggerService.Warning(ctx, "skip old PVC cleanup: binding still points to source PVC (possibly rolled back)",
						logit.String("sandboxUUID", m.SandboxUUID),
						logit.String("pvc", m.SourcePVCName))
					continue
				}
			}
		}

		// 删除旧 local-path PVC
		if m.SourcePVCName != "" {
			if err := cluster.DeletePVC(ctx, m.SourcePVCName); err != nil {
				resource.LoggerService.Warning(ctx, "delete migrated old PVC failed",
					logit.String("sandboxUUID", m.SandboxUUID),
					logit.String("pvc", m.SourcePVCName),
					logit.String("error", err.Error()))
				continue
			}
		}
		// 删除旧 local-path PV
		if m.SourcePVName != "" {
			if err := cluster.DeletePV(ctx, m.SourcePVName); err != nil {
				resource.LoggerService.Warning(ctx, "delete migrated old PV failed (non-fatal)",
					logit.String("sandboxUUID", m.SandboxUUID),
					logit.String("pv", m.SourcePVName),
					logit.String("error", err.Error()))
			}
		}

		// 标记为已清理，避免重复处理（乐观锁：仅 status=done 时更新）
		result := resource.OrmMain.WithContext(ctx).Model(&model.SandboxPVCMigration{}).
			Where("id = ? AND status = ?", m.ID, model.PVCMigrationStatusDone).
			Updates(map[string]any{
				"status": model.PVCMigrationStatusCleaned,
			})
		if result.Error != nil {
			resource.LoggerService.Error(ctx, "update migration status to cleaned failed",
				logit.String("sandboxUUID", m.SandboxUUID),
				logit.String("error", result.Error.Error()))
		}

		resource.LoggerService.Notice(ctx, "cleaned migrated old PVC after retention period",
			logit.String("sandboxUUID", m.SandboxUUID),
			logit.String("oldPVC", m.SourcePVCName),
			logit.String("oldPV", m.SourcePVName))
	}
}

func (pc *PVCCleaner) reconcileActiveBindingsForClosedSandboxes(ctx context.Context) {
	var activeBindings []*model.SandboxPVCBinding
	if err := resource.OrmMain.WithContext(ctx).
		Where("status = ? AND deleted = 0", model.SandboxPVCStatusActive).
		Limit(defaultPVCBatchSize).
		Find(&activeBindings).Error; err != nil {
		resource.LoggerService.Error(ctx, "query active pvc bindings failed", logit.String("error", err.Error()))
		return
	}
	if len(activeBindings) == 0 {
		return
	}

	sandboxRuntimeDao := dao.Use(resource.OrmMain).SandboxRuntime
	grace := service.DefaultSandboxPVCGracePeriod()
	now := time.Now()

	for _, binding := range activeBindings {
		if binding == nil {
			continue
		}
		rt, err := sandboxRuntimeDao.Where(sandboxRuntimeDao.SandboxUUID.Eq(binding.SandboxUUID)).First()
		if err != nil || rt == nil {
			continue
		}
		if !rt.UsePvc {
			continue
		}
		if rt.Status != constant.StatusKill && rt.Status != constant.StatusExpiredKill {
			continue
		}

		endAt := rt.EndTime
		if endAt.IsZero() {
			endAt = now
		}
		reclaimAt := endAt.Add(grace)
		_ = resource.OrmMain.WithContext(ctx).
			Model(&model.SandboxPVCBinding{}).
			Where("id = ? AND status = ? AND deleted = 0", binding.ID, model.SandboxPVCStatusActive).
			Updates(map[string]any{
				"status":        model.SandboxPVCStatusKill,
				"reclaim_after": reclaimAt,
				"update_time":   now,
			}).Error
	}
}
