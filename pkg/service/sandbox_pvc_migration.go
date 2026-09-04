package service

import (
	"context"
	"fmt"
	"hash/crc32"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
	corev1 "k8s.io/api/core/v1"

	"icode.baidu.com/baidu/gdp/logit"

	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/common"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/dao"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/k8s"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/resource"
	libssh "icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/ssh"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/model"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util/constant"
)

const (
	migrateLockTTL      = 30 * time.Minute
	migrateNodeUser     = "root"
	migrateNodePassword = "QkpG5xo$7BZRMrLV"

	migrateDataRetryMax = 3
	migratePodTimeout   = 5 * time.Minute

	// lazyMigrationTimeout 懒迁移 Pod 最大等待时间
	lazyMigrationTimeout = 30 * time.Minute
)

// MigrateSandboxPVC triggers a same-cluster cross-node PVC migration using client-go + SSH.
func MigrateSandboxPVC(ctx context.Context, sandboxUUID, targetNode string) (*model.SandboxPVCMigration, error) {
	// 1. Get PVC binding.
	binding, err := GetSandboxPVCBinding(ctx, sandboxUUID)
	if err != nil {
		return nil, fmt.Errorf("get pvc binding failed: %w", err)
	}

	// 1.5 CFS/BOS 存储跨节点天然可达，不需要迁移。
	if IsSharedStorageClass(binding.StorageClass) {
		return nil, fmt.Errorf("shared storage (%s) does not require cross-node migration", binding.StorageClass)
	}

	// 2. Get cluster object via ClusterManager（前移）。
	cluster := resource.ClusterManager.GetClusterByName(binding.Cluster)
	if cluster == nil {
		return nil, fmt.Errorf("cluster %s not found", binding.Cluster)
	}

	// 3. targetNode 为空时自动选择最空闲节点。
	if targetNode == "" {
		pvName, err := cluster.GetPVNameByPVC(ctx, binding.PVCName)
		if err != nil {
			return nil, fmt.Errorf("get pv name for auto node selection: %w", err)
		}
		sourceNode, err := cluster.GetPVSourceNode(ctx, pvName, binding.PVCName)
		if err != nil {
			return nil, fmt.Errorf("get source node for auto node selection: %w", err)
		}
		excludeNodes := map[string]bool{sourceNode: true}
		targetNode, err = cluster.SelectLeastLoadedNode(ctx, excludeNodes)
		if err != nil {
			return nil, fmt.Errorf("auto select target node failed: %w", err)
		}
		resource.LoggerService.Notice(ctx, "auto selected target node",
			logit.String("sandboxUUID", sandboxUUID),
			logit.String("targetNode", targetNode),
			logit.String("sourceNode", sourceNode))
	}

	// 4. Ensure sandbox is not running/pending.
	sandboxRuntimeDao := dao.Use(resource.OrmMain).SandboxRuntime
	runtime, err := sandboxRuntimeDao.Where(sandboxRuntimeDao.SandboxUUID.Eq(sandboxUUID)).First()
	if err != nil {
		return nil, fmt.Errorf("get sandbox runtime failed: %w", err)
	}
	if runtime.Status == constant.StatusRunning || runtime.Status == constant.StatusPending {
		return nil, fmt.Errorf("sandbox is %s, please delete the sandbox first before migration", runtime.Status)
	}

	// 5. Check no in-progress migration.
	var inProgressCount int64
	resource.OrmMain.WithContext(ctx).Model(&model.SandboxPVCMigration{}).
		Where("sandbox_uuid = ? AND status IN ?", sandboxUUID,
			[]string{model.PVCMigrationStatusPending, model.PVCMigrationStatusRunning}).
		Count(&inProgressCount)
	if inProgressCount > 0 {
		return nil, fmt.Errorf("another migration is in progress for sandbox %s", sandboxUUID)
	}

	// 6. Distributed lock via Redis SETNX.
	lockKey := constant.SandboxPVCMigrateLockPrefix + sandboxUUID
	locked, err := resource.RedisClient.SetNX(ctx, lockKey, "1", migrateLockTTL).Result()
	if err != nil {
		return nil, fmt.Errorf("acquire lock failed: %w", err)
	}
	if !locked {
		return nil, fmt.Errorf("migration lock already held for sandbox %s", sandboxUUID)
	}

	// 7. Insert migration record (status=pending).
	now := time.Now()
	migration := &model.SandboxPVCMigration{
		SandboxUUID: sandboxUUID,
		BindingID:   binding.ID,
		TargetNode:  targetNode,
		Cluster:     binding.Cluster,
		Namespace:   binding.Namespace,
		Status:      model.PVCMigrationStatusPending,
		CreateTime:  now,
	}
	if err := resource.OrmMain.WithContext(ctx).Create(migration).Error; err != nil {
		resource.RedisClient.Del(ctx, lockKey)
		return nil, fmt.Errorf("create migration record failed: %w", err)
	}

	// 8. Launch async goroutine to perform the migration.
	go executeMigration(sandboxUUID, targetNode, binding, migration, cluster, lockKey)

	return migration, nil
}

// executeMigration performs the full migration workflow in background.
func executeMigration(sandboxUUID, targetNode string, binding *model.SandboxPVCBinding,
	migration *model.SandboxPVCMigration, cluster *k8s.Cluster, lockKey string) {

	ctx := context.Background()
	logger := resource.LoggerService

	// Ensure lock release on exit.
	defer func() {
		resource.RedisClient.Del(ctx, lockKey)
	}()

	updateStatus := func(status, errMsg string) {
		updates := map[string]interface{}{"status": status}
		if errMsg != "" {
			updates["error_msg"] = errMsg
		}
		if status == model.PVCMigrationStatusDone || status == model.PVCMigrationStatusFailed {
			now := time.Now()
			updates["end_time"] = now
		}
		resource.OrmMain.WithContext(ctx).Model(&model.SandboxPVCMigration{}).
			Where("id = ?", migration.ID).Updates(updates)
	}

	failAndReturn := func(step string, err error) {
		errMsg := fmt.Sprintf("[%s] %v", step, err)
		logger.Error(ctx, "migration failed",
			logit.String("sandboxUUID", sandboxUUID),
			logit.String("step", step),
			logit.String("error", err.Error()))
		updateStatus(model.PVCMigrationStatusFailed, errMsg)
	}

	// (a) Update status → running.
	startTime := time.Now()
	resource.OrmMain.WithContext(ctx).Model(&model.SandboxPVCMigration{}).
		Where("id = ?", migration.ID).
		Updates(map[string]interface{}{
			"status":     model.PVCMigrationStatusRunning,
			"start_time": startTime,
		})

	// (b) Get PV info: name, source node, local path.
	labelSelector := fmt.Sprintf("sandbox-id=%s", sandboxUUID)
	pvName, err := cluster.GetPVNameByPVC(ctx, binding.PVCName)
	if err != nil {
		failAndReturn("get-pv-name", err)
		return
	}
	pv, err := cluster.GetPV(ctx, pvName)
	if err != nil {
		failAndReturn("get-pv", err)
		return
	}
	sourceNode, err := cluster.GetPVSourceNode(ctx, pvName, binding.PVCName)
	if err != nil {
		failAndReturn("get-source-node", err)
		return
	}
	sourcePVPath := cluster.GetPVLocalPath(pv)
	if sourcePVPath == "" {
		failAndReturn("get-pv-path", fmt.Errorf("PV %s has no local/hostPath", pvName))
		return
	}

	// Update migration record with source info.
	resource.OrmMain.WithContext(ctx).Model(&model.SandboxPVCMigration{}).
		Where("id = ?", migration.ID).
		Updates(map[string]interface{}{
			"source_node":     sourceNode,
			"source_pv_name":  pvName,
			"source_pvc_name": binding.PVCName,
			"source_pv_path":  sourcePVPath,
		})

	// (c) Validate source != target.
	if sourceNode == targetNode {
		failAndReturn("validate", fmt.Errorf("source node %s equals target node", sourceNode))
		return
	}

	// (d) SSH: connect to source and target nodes.
	srcSSH, err := libssh.Dial(sourceNode, migrateNodeUser, migrateNodePassword)
	if err != nil {
		failAndReturn("ssh-source", err)
		return
	}
	defer srcSSH.Close()

	dstSSH, err := libssh.Dial(targetNode, migrateNodeUser, migrateNodePassword)
	if err != nil {
		failAndReturn("ssh-target", err)
		return
	}
	defer dstSSH.Close()

	// (e) Target node: mkdir -p target path.
	// 使用与源 PV 相同的路径，保持一致性（如 /opt/local-path-provisioner/pvc-xxx）。
	targetPath := sourcePVPath
	if _, err := dstSSH.Run(fmt.Sprintf("mkdir -p %s", targetPath)); err != nil {
		failAndReturn("mkdir-target", err)
		return
	}

	// (f) Data transfer with retry: source tar → io.Pipe → target tar.
	var transferErr error
	for attempt := 1; attempt <= migrateDataRetryMax; attempt++ {
		transferErr = transferData(srcSSH, dstSSH, sourcePVPath, targetPath)
		if transferErr == nil {
			// Verify file count matches.
			if verifyErr := verifyFileCount(srcSSH, dstSSH, sourcePVPath, targetPath); verifyErr != nil {
				logger.Warning(ctx, "file count mismatch, retrying",
					logit.String("sandboxUUID", sandboxUUID),
					logit.Int("attempt", attempt),
					logit.String("error", verifyErr.Error()))
				transferErr = verifyErr
				continue
			}
			break
		}
		logger.Warning(ctx, "data transfer failed, retrying",
			logit.String("sandboxUUID", sandboxUUID),
			logit.Int("attempt", attempt),
			logit.String("error", transferErr.Error()))
	}
	if transferErr != nil {
		failAndReturn("data-transfer", transferErr)
		return
	}

	logger.Notice(ctx, "data transfer completed",
		logit.String("sandboxUUID", sandboxUUID),
		logit.String("source", sourceNode+":"+sourcePVPath),
		logit.String("target", targetNode+":"+targetPath))

	// (g) Delete existing pod (if any) to release PVC binding.
	// Pod is directly created (not managed by STS/Deployment), so just delete it.
	_ = cluster.DeletePod(sandboxUUID)

	// (h) Wait for pod termination.
	if err := cluster.WaitForPodTermination(ctx, labelSelector, migratePodTimeout); err != nil {
		failAndReturn("wait-termination", err)
		return
	}

	// (i) Recreate PV pointing to target node (with rollback on failure).
	if err := cluster.RecreatePVForMigration(ctx, pv, binding.PVCName, targetNode, targetPath); err != nil {
		failAndReturn("recreate-pv", err)
		return
	}

	// Migration done: PV now points to target node.
	// User triggers restore to create a new Pod on the target node.
	updateStatus(model.PVCMigrationStatusDone, "")

	// Update binding update_time.
	resource.OrmMain.WithContext(ctx).Model(&model.SandboxPVCBinding{}).
		Where("id = ?", migration.BindingID).
		Updates(map[string]interface{}{"update_time": time.Now()})

	logger.Notice(ctx, "migration completed successfully",
		logit.String("sandboxUUID", sandboxUUID),
		logit.String("sourceNode", sourceNode),
		logit.String("targetNode", targetNode))
}

// transferData streams data from source to target via io.Pipe.
// source: tar cf - -C <srcPath> . → pipe → target: tar xf - -C <dstPath>
func transferData(srcSSH, dstSSH *libssh.Client, srcPath, dstPath string) error {
	pr, pw := io.Pipe()

	srcCmd := fmt.Sprintf("tar cf - -C %s .", srcPath)
	dstCmd := fmt.Sprintf("tar xf - -C %s", dstPath)

	errCh := make(chan error, 2)

	// Source: tar and write to pipe.
	go func() {
		errCh <- srcSSH.RunStream(srcCmd, pw)
	}()

	// Target: read from pipe and untar.
	go func() {
		errCh <- dstSSH.RunWithStdin(dstCmd, pr)
	}()

	// Wait for both goroutines.
	var firstErr error
	for i := 0; i < 2; i++ {
		if err := <-errCh; err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// verifyFileCount compares the file count on source and target paths.
func verifyFileCount(srcSSH, dstSSH *libssh.Client, srcPath, dstPath string) error {
	countCmd := func(path string) string {
		return fmt.Sprintf("find %s -type f | wc -l", path)
	}

	srcCount, err := srcSSH.Run(countCmd(srcPath))
	if err != nil {
		return fmt.Errorf("count source files: %w", err)
	}
	dstCount, err := dstSSH.Run(countCmd(dstPath))
	if err != nil {
		return fmt.Errorf("count target files: %w", err)
	}

	srcCount = strings.TrimSpace(srcCount)
	dstCount = strings.TrimSpace(dstCount)
	if srcCount != dstCount {
		return fmt.Errorf("file count mismatch: source=%s, target=%s", srcCount, dstCount)
	}
	return nil
}

// GetMigrationStatus returns the most recent migration record for a sandbox.
func GetMigrationStatus(ctx context.Context, sandboxUUID string) (*model.SandboxPVCMigration, error) {
	var migration model.SandboxPVCMigration
	err := resource.OrmMain.WithContext(ctx).
		Where("sandbox_uuid = ?", sandboxUUID).
		Order("id DESC").
		First(&migration).Error
	if err != nil {
		return nil, err
	}
	return &migration, nil
}

// IsStorageMigrationInProgress 检查指定沙箱是否有进行中的跨存储类迁移。
// 恢复沙箱前应调用此函数，迁移中禁止恢复以避免数据不一致。
func IsStorageMigrationInProgress(ctx context.Context, sandboxUUID string) bool {
	var count int64
	err := resource.OrmMain.WithContext(ctx).Model(&model.SandboxPVCMigration{}).
		Where("sandbox_uuid = ? AND migration_type = ? AND status IN ?", sandboxUUID,
			model.PVCMigrationTypeCrossStorage,
			[]string{model.PVCMigrationStatusPending, model.PVCMigrationStatusRunning}).
		Count(&count).Error
	if err != nil {
		// fail-safe：DB 查询失败时视为有迁移进行中，阻止恢复操作以避免数据不一致
		resource.LoggerService.Error(ctx, "check migration in progress failed, fail-safe blocking restore",
			logit.String("sandboxUUID", sandboxUUID),
			logit.String("error", err.Error()))
		return true
	}
	return count > 0
}

// GetMigrationByID retrieves a migration record by ID.
func GetMigrationByID(ctx context.Context, id int64) (*model.SandboxPVCMigration, error) {
	var migration model.SandboxPVCMigration
	err := resource.OrmMain.WithContext(ctx).
		Where("id = ?", id).
		First(&migration).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &migration, nil
}

// shouldMigrateToNewCFS 判断是否需要迁移到其他 CFS StorageClass。
// 仅对 sharedcfs 触发  CFS → CFS-N
// 优先级：百分比灰度 > 用户白名单（不依赖全量开关）
func shouldMigrateToNewCFS(runtime *model.SandboxRuntime, binding *model.SandboxPVCBinding) bool {
	// 只有 sharedcfs 才参与迁移
	if binding.StorageClass != StorageClassCFS {
		return false
	}

	// 计算目标 CFS StorageClass
	targetSC := SelectCFSStorageClass(runtime.CreateUser)

	// 如果当前已经是目标 StorageClass，无需迁移
	if binding.StorageClass == targetSC {
		return false
	}

	// 百分比灰度：Redis 动态配置，对 sandboxUUID 取 hash 判断是否命中
	if isHitByNewCFSMigratePercentage(runtime.SandboxUUID) {
		return true
	}
	// 灰度：新 CFS 迁移专用白名单
	return common.IsCfsNewMigrateUser(runtime.CreateUser)
}

// ============ 懒迁移：local-path → CFS ============
// shouldLazyMigrate 判断释放时是否需要触发存储类懒迁移。
// 仅对非共享存储（local-path / hp1）的沙箱触发。
// 优先级：全量开关 > 用户白名单 > 百分比灰度。
func shouldLazyMigrate(runtime *model.SandboxRuntime, binding *model.SandboxPVCBinding) bool {
	// 已经是共享存储，无需迁移
	if IsSharedStorageClass(binding.StorageClass) {
		return false
	}
	// 全量开关优先（app.toml [Feature] LazyMigrationToCFS）
	if resource.Feature.LazyMigrationToCFS {
		return true
	}
	// 灰度：CFS 迁移专用白名单
	if common.IsCfsMigrateUser(runtime.CreateUser) {
		return true
	}
	// 百分比灰度：Redis 动态配置，对 sandboxUUID 取 hash 判断是否命中
	return isHitByMigratePercentage(runtime.SandboxUUID)
}

// isHitByMigratePercentage 根据 Redis 中的百分比配置判断是否命中灰度。
// 对 sandboxUUID 做 CRC32 取模，确保同一沙箱每次判断结果一致（确定性灰度）。
// Redis Key: sandbox:cfs:migrate:percentage，值为 0~100 的整数。
func isHitByMigratePercentage(sandboxUUID string) bool {
	return isHitByPercentage(sandboxUUID, constant.CfsMigratePercentageKey)
}

// isHitByNewCFSMigratePercentage 根据 Redis 中的百分比配置判断是否命中新 CFS 迁移灰度。
// 对 sandboxUUID 做 CRC32 取模，确保同一沙箱每次判断结果一致（确定性灰度）。
// Redis Key: sandbox:cfs:new:migrate:percentage，值为 0~100 的整数。
func isHitByNewCFSMigratePercentage(sandboxUUID string) bool {
	return isHitByPercentage(sandboxUUID, constant.CfsNewMigratePercentageKey)
}

// isHitByPercentage 根据 Redis 中的百分比配置判断是否命中灰度。
// 对 sandboxUUID 做 CRC32 取模，确保同一沙箱每次判断结果一致（确定性灰度）。
// redisKey: Redis Key，值为 0~100 的整数。
func isHitByPercentage(sandboxUUID, redisKey string) bool {
	raw, err := resource.RedisClient.Get(context.Background(), redisKey).Result()
	if err != nil || raw == "" {
		return false
	}
	val, err := strconv.Atoi(raw)
	if err != nil || val <= 0 {
		return false
	}
	if val >= 100 {
		return true
	}
	h := crc32.ChecksumIEEE([]byte(sandboxUUID))
	return int(h%100) < val
}

// buildMigrationPVCName 生成懒迁移目标 CFS PVC 名称。
// 如果源 PVC 名称已经是 sandbox-{uuid}-pvc-cfs 格式，则生成带版本号的新名称（如 sandbox-{uuid}-pvc-cfs-v2）。
func buildMigrationPVCName(sandboxUUID string, sourcePVCName string) string {
	baseName := fmt.Sprintf("sandbox-%s-pvc-cfs", sandboxUUID)
	// 如果源 PVC 名称已经是目标格式，生成带版本号的新名称
	if sourcePVCName == baseName {
		return baseName + "-v2"
	}
	return baseName
}

// buildMigrationPodName 生成懒迁移临时 Pod 名称。
func buildMigrationPodName(sandboxUUID string) string {
	return fmt.Sprintf("migrate-storage-%s", sandboxUUID)
}

// TriggerLazyStorageMigration 创建迁移记录并启动异步 goroutine 执行 local-path → CFS / CFS → CFS 数据迁移。
// 由 ReleaseSandboxRuntime 在释放流程末尾调用。
// 注意：此函数在 goroutine 中执行，不能使用请求级 context（HTTP 返回后会 cancel），
// 内部统一使用 context.Background()。
func TriggerLazyStorageMigration(_ context.Context, sandboxUUID string, binding *model.SandboxPVCBinding, cluster *k8s.Cluster, username string) error {
	ctx := context.Background()
	logger := resource.LoggerService

	// 分布式锁防止重复触发
	lockKey := constant.SandboxStorageMigrateLockPrefix + sandboxUUID
	locked, err := resource.RedisClient.SetNX(ctx, lockKey, "1", lazyMigrationTimeout+5*time.Minute).Result()
	if err != nil {
		return fmt.Errorf("acquire storage migration lock: %w", err)
	}
	if !locked {
		logger.Notice(ctx, "storage migration lock already held, skip",
			logit.String("sandboxUUID", sandboxUUID))
		return nil
	}

	// 检查是否已有进行中的跨存储类迁移
	var inProgressCount int64
	resource.OrmMain.WithContext(ctx).Model(&model.SandboxPVCMigration{}).
		Where("sandbox_uuid = ? AND migration_type = ? AND status IN ?", sandboxUUID,
			model.PVCMigrationTypeCrossStorage,
			[]string{model.PVCMigrationStatusPending, model.PVCMigrationStatusRunning}).
		Count(&inProgressCount)
	if inProgressCount > 0 {
		resource.RedisClient.Del(ctx, lockKey)
		return fmt.Errorf("cross-storage migration already in progress for sandbox %s", sandboxUUID)
	}

	// 根据用户名哈希选择目标 CFS StorageClass
	targetStorageClass := SelectCFSStorageClass(username)

	// 创建迁移记录
	now := time.Now()
	newPVCName := buildMigrationPVCName(sandboxUUID, binding.PVCName)
	migration := &model.SandboxPVCMigration{
		SandboxUUID:        sandboxUUID,
		BindingID:          binding.ID,
		SourcePVCName:      binding.PVCName,
		Cluster:            binding.Cluster,
		Namespace:          binding.Namespace,
		MigrationType:      model.PVCMigrationTypeCrossStorage,
		TargetStorageClass: targetStorageClass,
		TargetPVCName:      newPVCName,
		Status:             model.PVCMigrationStatusPending,
		CreateTime:         now,
	}
	if err := resource.OrmMain.WithContext(ctx).Create(migration).Error; err != nil {
		resource.RedisClient.Del(ctx, lockKey)
		return fmt.Errorf("create storage migration record: %w", err)
	}

	logger.Notice(ctx, "lazy storage migration triggered",
		logit.String("sandboxUUID", sandboxUUID),
		logit.String("sourcePVC", binding.PVCName),
		logit.String("targetPVC", newPVCName),
		logit.String("targetStorageClass", targetStorageClass))

	// 异步执行迁移
	go executeLazyStorageMigration(sandboxUUID, binding, migration, cluster, lockKey)

	return nil
}

// executeLazyStorageMigration 异步执行 local-path → CFS / CFS → CFS 全流程迁移。
func executeLazyStorageMigration(sandboxUUID string, binding *model.SandboxPVCBinding,
	migration *model.SandboxPVCMigration, cluster *k8s.Cluster, lockKey string) {

	ctx := context.Background()
	logger := resource.LoggerService

	// 退出时释放锁
	defer resource.RedisClient.Del(ctx, lockKey)

	updateStatus := func(status, errMsg string) {
		updates := map[string]interface{}{"status": status}
		if errMsg != "" {
			updates["error_msg"] = errMsg
		}
		if status == model.PVCMigrationStatusDone || status == model.PVCMigrationStatusFailed {
			now := time.Now()
			updates["end_time"] = now
		}
		resource.OrmMain.WithContext(ctx).Model(&model.SandboxPVCMigration{}).
			Where("id = ?", migration.ID).Updates(updates)
	}

	failAndCleanup := func(step string, err error, cleanupNewPVC bool) {
		errMsg := fmt.Sprintf("[%s] %v", step, err)
		logger.Error(ctx, "lazy storage migration failed",
			logit.String("sandboxUUID", sandboxUUID),
			logit.String("step", step),
			logit.String("error", err.Error()))
		updateStatus(model.PVCMigrationStatusFailed, errMsg)

		// 失败回滚：清理新 CFS PVC + 临时 Pod
		migratePodName := buildMigrationPodName(sandboxUUID)
		_ = cluster.DeletePodByName(ctx, migratePodName)
		if cleanupNewPVC {
			_ = cluster.DeletePVC(ctx, migration.TargetPVCName)
		}
	}

	// (a) 更新状态为 running
	startTime := time.Now()
	resource.OrmMain.WithContext(ctx).Model(&model.SandboxPVCMigration{}).
		Where("id = ?", migration.ID).
		Updates(map[string]interface{}{
			"status":     model.PVCMigrationStatusRunning,
			"start_time": startTime,
		})

	// 判断源存储类型：local-path/hp1 需要获取节点信息，CFS 不需要
	isLocalStorage := !IsSharedStorageClass(binding.StorageClass)
	var sourceNode string

	if isLocalStorage {
		// (b) 获取源 PV 信息（节点）
		pvName, err := cluster.GetPVNameByPVC(ctx, binding.PVCName)
		if err != nil {
			failAndCleanup("get-pv-name", err, false)
			return
		}
		sourceNode, err = cluster.GetPVSourceNode(ctx, pvName, binding.PVCName)
		if err != nil {
			failAndCleanup("get-source-node", err, false)
			return
		}

		// 更新迁移记录的源节点信息
		resource.OrmMain.WithContext(ctx).Model(&model.SandboxPVCMigration{}).
			Where("id = ?", migration.ID).
			Updates(map[string]interface{}{
				"source_node":    sourceNode,
				"source_pv_name": pvName,
			})

		logger.Notice(ctx, "lazy migration source info",
			logit.String("sandboxUUID", sandboxUUID),
			logit.String("sourceNode", sourceNode),
			logit.String("pvName", pvName))
	} else {
		// CFS → CFS：获取源 PV 名称（用于记录，不需要节点信息）
		pvName, err := cluster.GetPVNameByPVC(ctx, binding.PVCName)
		if err != nil {
			failAndCleanup("get-pv-name", err, false)
			return
		}

		// 更新迁移记录的源 PVC 和 PV 信息
		resource.OrmMain.WithContext(ctx).Model(&model.SandboxPVCMigration{}).
			Where("id = ?", migration.ID).
			Updates(map[string]interface{}{
				"source_node":    "",
				"source_pv_name": pvName,
			})

		logger.Notice(ctx, "lazy migration source info (CFS)",
			logit.String("sandboxUUID", sandboxUUID),
			logit.String("sourcePVC", binding.PVCName),
			logit.String("pvName", pvName))
	}

	// (c) 创建新 CFS PVC（ReadWriteMany）
	newPVCName := migration.TargetPVCName
	if err := cluster.CreateDynamicPVCWithAccessMode(ctx, newPVCName, migration.TargetStorageClass,
		buildPVCSize(20), corev1.ReadWriteMany); err != nil {
		failAndCleanup("create-cfs-pvc", err, false)
		return
	}

	// (d) 等待旧 Pod 完全终止（释放 PVC 占用）
	labelSelector := fmt.Sprintf("sandbox-id=%s", sandboxUUID)
	if err := cluster.WaitForPodTermination(ctx, labelSelector, 5*time.Minute); err != nil {
		failAndCleanup("wait-pod-termination", err, true)
		return
	}

	// (e) 创建临时迁移 Pod
	migratePodName := buildMigrationPodName(sandboxUUID)
	// 创建迁移 Pod：local-path → CFS 传 sourceNode，CFS → CFS 传空字符串
	err := cluster.CreateStorageMigrationPod(ctx, migratePodName, sourceNode,
		binding.PVCName, newPVCName)
	if err != nil {
		failAndCleanup("create-migration-pod", err, true)
		return
	}

	// (f) 等待迁移 Pod 完成（超时 30min）
	phase, err := cluster.WaitForPodCompletion(ctx, migratePodName, lazyMigrationTimeout)
	if err != nil {
		failAndCleanup("wait-pod-completion", err, true)
		return
	}

	// (g) 获取日志用于诊断
	logs, _ := cluster.GetPodLogs(ctx, migratePodName, "migrate")

	// (h) 清理迁移 Pod
	_ = cluster.DeletePodByName(ctx, migratePodName)

	if phase != corev1.PodSucceeded {
		failAndCleanup("pod-failed", fmt.Errorf("migration pod phase: %s, logs: %s", phase, logs), true)
		return
	}

	logger.Notice(ctx, "lazy migration data copy completed",
		logit.String("sandboxUUID", sandboxUUID),
		logit.String("logs", logs))

	// (i) 先更新 binding（可回滚），再删旧 PVC（不可逆）
	// 顺序很重要：如果先删旧 PVC 再更新 binding，一旦 binding 更新失败会导致数据丢失。
	// 乐观锁：匹配迁移开始时的 pvc_name，防止迁移期间 binding 被并发修改（如恢复操作、cleaner 等）。
	// 同时清除 reclaim_after，防止 PVCCleaner 按旧的回收时间误删新 CFS PVC。
	result := resource.OrmMain.WithContext(ctx).Model(&model.SandboxPVCBinding{}).
		Where("id = ? AND pvc_name = ? AND deleted = 0", binding.ID, binding.PVCName).
		Updates(map[string]interface{}{
			"pvc_name":      newPVCName,
			"storage_class": migration.TargetStorageClass,
			"reclaim_after": nil,
			"update_time":   time.Now(),
		})
	if result.Error != nil {
		// binding 更新失败：新旧 PVC 并存，旧 PVC 数据安全，需人工介入
		logger.Error(ctx, "update binding to CFS failed, keeping old PVC, manual intervention needed",
			logit.String("sandboxUUID", sandboxUUID),
			logit.String("newPVC", newPVCName),
			logit.String("error", result.Error.Error()))
		updateStatus(model.PVCMigrationStatusFailed, fmt.Sprintf("[update-binding] %v", result.Error))
		return
	}
	if result.RowsAffected == 0 {
		// binding 已被并发修改（pvc_name 不匹配或已 deleted），放弃迁移，保留旧 PVC
		logger.Error(ctx, "binding was modified during migration, aborting",
			logit.String("sandboxUUID", sandboxUUID),
			logit.String("expectedPVC", binding.PVCName))
		updateStatus(model.PVCMigrationStatusFailed, "binding modified concurrently")
		_ = cluster.DeletePVC(ctx, newPVCName) // 清理新 CFS PVC
		return
	}

	// (j) 旧 local-path PVC 不立即删除，保留 2 天作为安全兜底。
	// 由 PVCCleaner 后台任务在 end_time + 2天后自动清理。
	// 这样如果 CFS 挂载有问题，可以人工快速恢复到旧 PVC。

	// (k) 迁移完成
	updateStatus(model.PVCMigrationStatusDone, "")

	logger.Notice(ctx, "lazy storage migration completed",
		logit.String("sandboxUUID", sandboxUUID),
		logit.String("oldPVC", binding.PVCName),
		logit.String("newPVC", newPVCName),
		logit.String("storageClass", migration.TargetStorageClass))
}

// BatchMigrateLocalPathToCFSRequest 批量迁移请求参数
type BatchMigrateLocalPathToCFSRequest struct {
	AppID       int32  `json:"appId"`       // 可选，限定 appId
	Cluster     string `json:"cluster"`     // 可选，限定集群
	Concurrency int    `json:"concurrency"` // 可选，并发数，默认5，范围1-20
	Limit       int    `json:"limit"`       // 可选，迁移数量，默认500，范围1-3000
	DryRun      bool   `json:"dryRun"`      // 可选，true=只查询不执行
}

// BatchMigrateLocalPathToCFSResult 批量迁移结果
type BatchMigrateLocalPathToCFSResult struct {
	Total          int                       `json:"total"`
	Triggered      int                       `json:"triggered"`
	Skipped        int                       `json:"skipped"`
	SkippedDetails []BatchMigrateSkippedItem `json:"skippedDetails,omitempty"`
}

// BatchMigrateSkippedItem 跳过的迁移项
type BatchMigrateSkippedItem struct {
	SandboxUUID string `json:"sandboxUUID"`
	Reason      string `json:"reason"`
}

// BatchMigrateLocalPathToCFS 批量将 kill 状态的 local-path PVC 迁移为 CFS。
// 复用 TriggerLazyStorageMigration 逐个触发，使用信号量控制并发数。
func BatchMigrateLocalPathToCFS(ctx context.Context, req *BatchMigrateLocalPathToCFSRequest) (*BatchMigrateLocalPathToCFSResult, error) {
	logger := resource.LoggerService

	// 参数默认值
	concurrency := req.Concurrency
	if concurrency <= 0 {
		concurrency = 5
	}
	if concurrency > 20 {
		concurrency = 20
	}

	limit := req.Limit
	if limit <= 0 {
		limit = 500
	}
	if limit > 3000 {
		limit = 3000
	}

	// 查询符合条件的 binding：kill 状态 + local-path 或 sharedcfs + 未删除，按 update_time 升序
	query := resource.OrmMain.WithContext(ctx).
		Where("status = ? AND (storage_class = ? OR storage_class = ?) AND deleted = 0",
			model.SandboxPVCStatusKill, StorageClassLocalPath, StorageClassCFS)
	if req.AppID > 0 {
		query = query.Where("app_id = ?", req.AppID)
	}
	if req.Cluster != "" {
		query = query.Where("cluster = ?", req.Cluster)
	}

	var bindings []model.SandboxPVCBinding
	if err := query.Order("update_time ASC").Limit(limit).Find(&bindings).Error; err != nil {
		return nil, fmt.Errorf("查询 local-path binding 失败: %w", err)
	}

	// 批量查询 SandboxRuntime 获取 create_user，用于哈希选择目标 CFS
	sandboxUUIDs := make([]string, 0, len(bindings))
	for _, b := range bindings {
		sandboxUUIDs = append(sandboxUUIDs, b.SandboxUUID)
	}
	var runtimes []model.SandboxRuntime
	if err := resource.OrmMain.WithContext(ctx).
		Where("sandbox_uuid IN ?", sandboxUUIDs).
		Find(&runtimes).Error; err != nil {
		return nil, fmt.Errorf("查询 sandbox runtime 失败: %w", err)
	}
	// 构建 sandboxUUID -> createUser 映射
	userMap := make(map[string]string, len(runtimes))
	for _, rt := range runtimes {
		userMap[rt.SandboxUUID] = rt.CreateUser
	}

	result := &BatchMigrateLocalPathToCFSResult{
		Total: len(bindings),
	}

	// dryRun 模式只返回统计
	if req.DryRun {
		return result, nil
	}

	logger.Notice(ctx, "batch migrate local-path to CFS started",
		logit.Int("total", len(bindings)),
		logit.Int("concurrency", concurrency))

	// 信号量控制并发
	sem := make(chan struct{}, concurrency)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for i := range bindings {
		binding := bindings[i]
		wg.Add(1)
		sem <- struct{}{} // 获取信号量

		go func() {
			defer wg.Done()
			defer func() { <-sem }() // 释放信号量
			// 防止 panic 导致进程崩溃
			defer func() {
				if r := recover(); r != nil {
					logger.Error(context.Background(), "batch migrate goroutine panic",
						logit.String("sandboxUUID", binding.SandboxUUID),
						logit.String("panic", fmt.Sprintf("%v", r)))
					mu.Lock()
					result.Skipped++
					result.SkippedDetails = append(result.SkippedDetails, BatchMigrateSkippedItem{
						SandboxUUID: binding.SandboxUUID,
						Reason:      fmt.Sprintf("panic: %v", r),
					})
					mu.Unlock()
				}
			}()

			// 获取集群对象
			cluster := resource.ClusterManager.GetClusterByName(binding.Cluster)
			if cluster == nil {
				mu.Lock()
				result.Skipped++
				result.SkippedDetails = append(result.SkippedDetails, BatchMigrateSkippedItem{
					SandboxUUID: binding.SandboxUUID,
					Reason:      fmt.Sprintf("集群 %s 不存在", binding.Cluster),
				})
				mu.Unlock()
				return
			}

			// 获取 create_user 用于哈希选择目标 CFS
			username := userMap[binding.SandboxUUID]
			if username == "" {
				mu.Lock()
				result.Skipped++
				result.SkippedDetails = append(result.SkippedDetails, BatchMigrateSkippedItem{
					SandboxUUID: binding.SandboxUUID,
					Reason:      "未找到对应的 sandbox runtime 记录",
				})
				mu.Unlock()
				return
			}

			// 触发懒迁移（TriggerLazyStorageMigration 内部使用 context.Background）
			if err := TriggerLazyStorageMigration(ctx, binding.SandboxUUID, &binding, cluster, username); err != nil {
				mu.Lock()
				result.Skipped++
				result.SkippedDetails = append(result.SkippedDetails, BatchMigrateSkippedItem{
					SandboxUUID: binding.SandboxUUID,
					Reason:      err.Error(),
				})
				mu.Unlock()
				return
			}

			mu.Lock()
			result.Triggered++
			mu.Unlock()
		}()
	}

	wg.Wait()

	logger.Notice(ctx, "batch migrate local-path to CFS completed",
		logit.Int("total", result.Total),
		logit.Int("triggered", result.Triggered),
		logit.Int("skipped", result.Skipped))

	return result, nil
}
