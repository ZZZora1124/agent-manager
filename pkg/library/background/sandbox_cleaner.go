package background

import (
	"context"
	"fmt"
	"time"

	"icode.baidu.com/baidu/gdp/logit"

	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/common"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/dao"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/mcpcenter"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/resource"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/model"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/service"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util/constant"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util/envvar"
)

// SandboxCleaner 沙箱清理器：定期清理过期的沙箱
type SandboxCleaner struct {
	ctx            context.Context
	cancelFunc     context.CancelFunc
	checkInterval  time.Duration
	expireDuration time.Duration
}

// NewSandboxCleaner 创建新的沙箱清理器
func NewSandboxCleaner(checkInterval, expireDuration time.Duration) *SandboxCleaner {
	ctx, cancel := context.WithCancel(context.Background())
	return &SandboxCleaner{
		ctx:            ctx,
		cancelFunc:     cancel,
		checkInterval:  checkInterval,
		expireDuration: expireDuration,
	}
}

func init() {
	if envvar.IsDisableBackgroundTask() {
		return
	}
	// 创建沙箱清理器，每隔一段时间检查一次过期的沙箱，默认过期时间1年;
	sandboxCleaner := NewSandboxCleaner(15*time.Second, time.Hour*24*365)
	sandboxCleaner.Start()
}

// Start 启动沙箱清理器
func (sc *SandboxCleaner) Start() {
	go sc.run()
}

// Stop 停止沙箱清理器
func (sc *SandboxCleaner) Stop() {
	sc.cancelFunc()
}

// 主工作逻辑
func (sc *SandboxCleaner) run() {
	<-resource.InitDone
	ticker := time.NewTicker(sc.checkInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			sc.cleanExpiredSandboxes()
		case <-sc.ctx.Done():
			return
		}
	}
}

// 清理过期的沙箱
func (sc *SandboxCleaner) cleanExpiredSandboxes() {
	ctx := context.Background()
	resource.LoggerService.Notice(ctx, "starting sandbox cleanup")

	lock, err := resource.RedisClient.SetNX(ctx, constant.SandboxCleanLockKey, "1", sc.checkInterval).Result()
	if err != nil {
		resource.LoggerService.Error(ctx, "set sandbox clean lock failed", logit.String("error", err.Error()))
		return
	}
	if !lock {
		resource.LoggerService.Warning(ctx, "sandbox clean lock already exist")
		return
	}
	resource.LoggerService.Notice(ctx, "starting sandbox cleanup, lock set success")

	sandboxRuntimeDao := dao.Use(resource.OrmMain).SandboxRuntime
	// 查询所有运行中/待运行的沙箱（排除app_id=2071，因为2071已有空闲清理机制）
	runningSandboxes, err := sandboxRuntimeDao.Where(
		sandboxRuntimeDao.Status.In(constant.StatusRunning, constant.StatusPending),
		sandboxRuntimeDao.AppID.Neq(2071),
	).Find()
	if err != nil {
		resource.LoggerService.Error(ctx, "query running sandboxes failed", logit.String("error", err.Error()))
		return
	}
	resource.LoggerService.Notice(ctx, "starting sandbox cleanup", logit.Int("count", len(runningSandboxes)))

	expiredCount := 0
	currentTime := time.Now()

	for _, sandbox := range runningSandboxes {
		// 重新从 DB 读取最新记录，避免使用批量查询时的快照数据
		// （恢复流程可能在查询后修改了 start_time/status/timeout 等字段）
		latest, err := sandboxRuntimeDao.Where(sandboxRuntimeDao.ID.Eq(sandbox.ID)).First()
		if err != nil {
			resource.LoggerService.Error(ctx, "re-query sandbox failed, skip",
				logit.String("sandboxID", sandbox.SandboxUUID),
				logit.String("error", err.Error()))
			continue
		}
		sandbox = latest

		// 不再是 Running/Pending 状态，说明已被其他流程处理，跳过
		if sandbox.Status != constant.StatusRunning && sandbox.Status != constant.StatusPending {
			continue
		}

		// 计算过期时间,从启动时间开始;
		expireTime := sandbox.StartTime.Add(time.Second * time.Duration(sandbox.Timeout))
		// timeout为0的默认不过期;
		if sandbox.Timeout > 0 && currentTime.After(expireTime) {
			// 检查沙箱是否正在恢复中，避免恢复时被误杀
			restoreLockKey := constant.SandboxRestoreLockPrefix + sandbox.SandboxUUID
			exists, lockErr := resource.RedisClient.Exists(ctx, restoreLockKey).Result()
			if lockErr != nil {
				resource.LoggerService.Error(ctx, "check restore lock failed, skip this sandbox",
					logit.String("sandboxID", sandbox.SandboxUUID),
					logit.String("error", lockErr.Error()))
				continue
			}
			if exists > 0 {
				resource.LoggerService.Notice(ctx, "sandbox is being restored, skip expire kill",
					logit.String("sandboxID", sandbox.SandboxUUID))
				continue
			}

			prevStatus := sandbox.Status
			// usePVC=true：过期后进入 expire_kill（PVC 保留到期后再释放）
			// usePVC=false：过期后进入 kill（与 usePVC=true 保持一致）
			if sandbox.UsePvc {
				sandbox.Status = constant.StatusExpiredKill
			} else {
				sandbox.Status = constant.StatusKill
			}
			sandbox.ReleaseName = "admin"
			// Pending 状态过期不计费：end_time 取 start_time；Running 状态取当前时间。
			if prevStatus == constant.StatusPending {
				sandbox.EndTime = sandbox.StartTime
			} else {
				sandbox.EndTime = currentTime
			}
			// 使用 map 更新，避免 GORM 忽略零值字段（如 UsePVC=false）
			updateData := map[string]any{
				"status":       sandbox.Status,
				"release_name": sandbox.ReleaseName,
				"end_time":     sandbox.EndTime,
			}
			_, err := sandboxRuntimeDao.Where(sandboxRuntimeDao.ID.Eq(sandbox.ID)).Updates(updateData)
			if err != nil {
				resource.LoggerService.Error(ctx, "update expired sandbox status failed",
					logit.String("sandboxID", sandbox.SandboxUUID),
					logit.String("error", err.Error()))
				continue
			}

			// 查询 app 信息获取 workspace_id 和 type
			appDao := dao.Use(resource.OrmMain).App
			app, appErr := appDao.WithContext(ctx).Where(appDao.ID.Eq(sandbox.AppID)).First()
			workspaceID := int32(0)
			appType := ""
			if appErr == nil && app != nil {
				workspaceID = app.WorkspaceID
				appType = app.Type
			} else {
				resource.LoggerService.Warning(ctx, "Failed to get app info for history record",
					logit.Int32("appID", sandbox.AppID),
					logit.String("sandboxUUID", sandbox.SandboxUUID),
					logit.String("error", fmt.Sprintf("%v", appErr)))
			}

			// 记录沙箱销毁历史（过期自动销毁）
			service.RecordDestroyHistory(
				sandbox.SandboxUUID,
				sandbox.AppID,
				sandbox.TemplateID,
				sandbox.Name,
				sandbox.Cluster,
				sandbox.CreateUser,
				"admin",
				sandbox.StartTime,
				sandbox.EndTime,
				"expire_kill",
				sandbox.NodeIP,
				sandbox.PortIP,
				"",
				sandbox.CPU,    // 新增：CPU毫核数
				sandbox.Memory, // 新增：内存大小（MB）
				sandbox.Disk,   // 新增：磁盘大小（GB）
				workspaceID,    // 新增：工作空间ID
				appType,        // 新增：应用类型
			)

			if sandbox.UsePvc {
				if err = service.ReleaseSandboxPVCBinding(ctx, sandbox.SandboxUUID, sandbox.EndTime, service.DefaultSandboxPVCGracePeriod()); err != nil {
					resource.LoggerService.Error(ctx, "release sandbox pvc binding failed",
						logit.String("sandboxID", sandbox.SandboxUUID),
						logit.String("error", err.Error()))
				}
				binding, bindErr := service.GetSandboxPVCBinding(ctx, sandbox.SandboxUUID)
				// 跨集群 PVC 迁移：将 app_id=2071 且 storage_class=sharedcfs 的 PVC 从老集群迁移到 agent-sandbox2-prod
				if bindErr == nil && binding != nil && common.IsCfsMigrateUser(sandbox.CreateUser) {
					if err := service.MigrateSbxPVCToNewCluster(ctx, sandbox.SandboxUUID, binding, sandbox.Cluster,
						sandbox.AppID); err != nil {
						resource.LoggerService.Warning(ctx, "migrate PVC to new cluster failed",
							logit.String("sandboxUUID", sandbox.SandboxUUID),
							logit.String("error", err.Error()))
					}
				}
			}

			if cluster := resource.ClusterManager.GetClusterByName(sandbox.Cluster); cluster != nil {
				cluster.DeletePod(sandbox.SandboxUUID)
				redisKey := constant.BackendIPPrefix + sandbox.SandboxUUID
				resource.LoggerService.Notice(ctx, "deleting redis key for expired sandbox",
					logit.String("sandboxID", sandbox.SandboxUUID),
					logit.String("redisKey", redisKey))
				_, err := resource.RedisClient.Del(ctx, redisKey).Result()
				if err != nil {
					resource.LoggerService.Error(ctx, "delete redis key failed for expired sandbox",
						logit.String("sandboxID", sandbox.SandboxUUID),
						logit.String("redisKey", redisKey),
						logit.String("error", err.Error()))
				}
			}
			expiredCount++
			resource.LoggerService.Notice(ctx, "expired sandbox cleaned",
				logit.String("sandboxID", sandbox.SandboxUUID),
				logit.Int("expiredCount", expiredCount))
		} else {
			if sandbox.Remind && !sandbox.IsReminded {
				out := sandbox.StartTime.Add(time.Duration(sandbox.Timeout) * time.Second)
				t := sandbox.PeriodTime
				if time.Until(out) < time.Duration(t)*time.Second {
					// 已到达过期前 t 秒提醒窗口, 调服务号通知用户
					var tpl *model.Template
					var app *model.App
					templateName := ""
					workspaceID := 0
					tplDao := dao.Use(resource.OrmMain).Template
					appDao := dao.Use(resource.OrmMain).App
					err = tplDao.Where(tplDao.ID.Eq(sandbox.TemplateID)).Scan(&tpl)
					if tpl != nil {
						templateName = tpl.Name
						err = appDao.Where(appDao.ID.Eq(sandbox.AppID)).Scan(&app)
						if app != nil {
							workspaceID = int(app.WorkspaceID)
						}
					}
					mcpcenter.GetMcpCenterApi().UseAIRobot(sandbox.CreateUser, "沙箱实例即将过期提醒", sandbox.Name, templateName, fmt.Sprintf("该沙箱实例将在%d分钟后过期，如需续期，请前往平台的实例列表进行续期操作", sandbox.PeriodTime/60), workspaceID)
					sandboxRuntimeDao.Where(sandboxRuntimeDao.ID.Eq(sandbox.ID)).
						Updates(map[string]any{
							"is_reminded": true,
						})
				}
			}
			resource.LoggerService.Debug(ctx, "sandbox is not expired",
				logit.String("sandboxID", sandbox.SandboxUUID))
		}
	}
	resource.LoggerService.Notice(ctx, "expired sandboxes cleanup completed", logit.Int("expiredCount", expiredCount))
}
