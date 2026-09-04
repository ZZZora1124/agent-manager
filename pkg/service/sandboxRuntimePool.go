package service

import (
	"context"
	"icode.baidu.com/baidu/gdp/env"
	"strconv"
	"time"

	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/common"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/dao"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/k8s"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/resource"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/model"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util/constant"
	"icode.baidu.com/baidu/gdp/logit"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
)

type sandboxRuntimePoolService struct{}

func NewSandboxRuntimePoolService() SandboxRuntimePoolService {
	return &sandboxRuntimePoolService{}
}

type SandboxRuntimePoolService interface {
	// TryAcquirePoolItem 尝试从预热池获取一个可用的pod，返回nil表示池中没有可用pod
	TryAcquirePoolItem(ctx context.Context, templateID int32) (*model.SandboxRuntimePool, error)

	// AcquireAndRemove 从预热池获取并移除一个可用的pod
	AcquireAndRemove(ctx context.Context, templateID int32, username string) (*model.SandboxRuntimePool, error)

	// CreatePoolItem 创建一个新的预热池pod
	CreatePoolItem(ctx context.Context, template *model.Template) (*model.SandboxRuntimePool, error)

	// UpdatePoolItemStatus 更新预热池pod状态
	UpdatePoolItemStatus(ctx context.Context, id int64, status string) error

	// UpdatePoolItemToReady 更新预热池pod为就绪状态
	UpdatePoolItemToReady(ctx context.Context, poolID int64, podEvent k8s.PodEvent) error

	// MarkPoolItemAsUsed 标记预热池pod为已使用
	MarkPoolItemAsUsed(ctx context.Context, id int64) error

	// MarkPoolItemAsFailed 标记预热池pod为失败
	MarkPoolItemAsFailed(ctx context.Context, id int64, reason string) error

	// GetPendingPoolItems 获取pending状态的预热池pod列表
	GetPendingPoolItems(ctx context.Context) ([]*model.SandboxRuntimePool, error)

	// GetReadyPoolItemsByTemplate 获取指定模板就绪状态的预热池pod列表
	GetReadyPoolItemsByTemplate(ctx context.Context, templateID int32) ([]*model.SandboxRuntimePool, error)

	// CountReadyPoolItemsByTemplate 统计指定模板就绪状态的预热池pod数量
	CountReadyPoolItemsByTemplate(ctx context.Context, templateID int32) (int64, error)

	// DeletePoolItem 删除预热池pod
	DeletePoolItem(ctx context.Context, id int64) error

	// DeletePoolItemByUUID 通过UUID删除预热池pod
	DeletePoolItemByUUID(ctx context.Context, sandboxUUID string) error

	// CleanExpiredPoolItems 清理过期的预热池pod
	CleanExpiredPoolItems(ctx context.Context, expireDuration time.Duration) (int, error)

	CleanModifyTemplatePool(ctx context.Context, templateID int32)
}

var poolStatusMapping = map[corev1.PodPhase]string{
	corev1.PodPending:   constant.PoolStatusPending,
	corev1.PodRunning:   constant.PoolStatusReady,
	corev1.PodSucceeded: constant.PoolStatusReady,
	corev1.PodFailed:    constant.PoolStatusFailed,
}

// TryAcquirePoolItem 尝试从预热池获取一个可用的pod
func (s *sandboxRuntimePoolService) TryAcquirePoolItem(ctx context.Context, templateID int32) (*model.SandboxRuntimePool, error) {
	logger := resource.LoggerService
	sandboxRuntimePoolDao := dao.Use(resource.OrmMain).SandboxRuntimePool

	poolItem, err := sandboxRuntimePoolDao.Where(
		sandboxRuntimePoolDao.TemplateID.Eq(templateID),
		sandboxRuntimePoolDao.Status.Eq(constant.PoolStatusReady),
	).Order(sandboxRuntimePoolDao.StartTime.Asc()).Take()

	if err != nil {
		logger.Debug(ctx, "pool item not found", logit.Int32("templateID", templateID))
		return nil, nil
	}

	logger.Notice(ctx, "found pool item", logit.Int64("id", poolItem.ID), logit.String("sandboxUUID", poolItem.SandboxUUID))
	return poolItem, nil
}

// AcquireAndRemove 从预热池获取并移除一个可用的pod
func (s *sandboxRuntimePoolService) AcquireAndRemove(ctx context.Context, templateID int32, username string) (*model.SandboxRuntimePool, error) {
	logger := resource.LoggerService

	if env.RunMode() == env.RunModeRelease && (templateID == 1905 || templateID == 4346) && !common.IsPoolSmallTrafficEnabled(username) {
		return nil, nil
	}

	// 使用templateID粒度的锁，避免全局锁阻塞其他templateID的请求
	lockKey := constant.SandboxPoolAcquireLockKeyPrefix + strconv.Itoa(int(templateID))

	// 轮询等待获取锁，最多等待30秒
	maxRetry := 30
	retryInterval := 100 * time.Millisecond

	var locked bool
	var err error

	for i := 0; i < maxRetry; i++ {
		locked, err = resource.RedisClient.SetNX(ctx, lockKey, "1", 10*time.Second).Result()
		if err != nil {
			logger.Error(ctx, "acquire pool lock failed", logit.String("error", err.Error()))
			return nil, err
		}
		if locked {
			break
		}
		// 未获取到锁，等待一段时间后重试
		logger.Debug(ctx, "pool acquire lock already exist, waiting...",
			logit.Int32("templateID", templateID),
			logit.Int("retry", i+1))
		time.Sleep(retryInterval)
	}

	if !locked {
		logger.Warning(ctx, "failed to acquire pool lock after max retries",
			logit.Int32("templateID", templateID))
		return nil, nil
	}
	defer resource.RedisClient.Del(ctx, lockKey)

	poolItem, err := s.TryAcquirePoolItem(ctx, templateID)
	if err != nil {
		return nil, err
	}
	if poolItem == nil {
		return nil, nil
	}

	// 标记为已使用
	err = s.MarkPoolItemAsUsed(ctx, poolItem.ID)
	if err != nil {
		logger.Error(ctx, "mark pool item as used failed",
			logit.Int64("id", poolItem.ID),
			logit.String("error", err.Error()))
		return nil, err
	}

	logger.Notice(ctx, "acquired pool item", logit.Int64("id", poolItem.ID), logit.String("sandboxUUID", poolItem.SandboxUUID))

	// 触发预热池补充（通知预热任务创建新的pod）
	select {
	case resource.PoolReplenishChan <- templateID:
		// 成功发送到通道
	default:
		// 通道已满，丢弃该请求
		resource.LoggerService.Debug(ctx, "replenish channel is full, dropping replenish request",
			logit.Int32("templateID", templateID))
	}

	return poolItem, nil
}

// CreatePoolItem 创建一个新的预热池pod
func (s *sandboxRuntimePoolService) CreatePoolItem(ctx context.Context, template *model.Template) (*model.SandboxRuntimePool, error) {
	logger := resource.LoggerService
	sandboxRuntimePoolDao := dao.Use(resource.OrmMain).SandboxRuntimePool

	// 获取应用信息
	var app model.App
	appDao := dao.Use(resource.OrmMain).App
	err := appDao.Where(appDao.ID.Eq(template.AppID)).Scan(&app)
	if err != nil {
		logger.Error(ctx, "get app failed", logit.String("error", err.Error()))
		return nil, err
	}

	// 获取镜像信息
	var img model.Image
	imageDao := dao.Use(resource.OrmMain).Image
	err = imageDao.Where(imageDao.ID.Eq(template.ImageID)).Scan(&img)
	if err != nil {
		logger.Error(ctx, "get image failed", logit.String("error", err.Error()))
		return nil, err
	}

	// 获取集群名称
	clusterName := NewAppService().GetAppCluster(template.AppID, "")

	poolDao := dao.Use(resource.OrmMain).SandboxRuntimePool
	sandboxUuid := "pool" + util.GenerateUniqueSandboxID(8, func(id string) bool {
		fullID := "pool" + id
		count, _ := poolDao.Where(poolDao.SandboxUUID.Eq(fullID)).Count()
		return count > 0
	})
	token := util.GenerateRandomString(32)
	logUuid := uuid.New().String()

	// 获取应用PVC绑定
	appPvcDao := dao.Use(resource.OrmMain).AppPvcBinding
	pvcBindings, err := appPvcDao.Where(appPvcDao.AppID.Eq(template.AppID)).Find()
	if err != nil {
		logger.Error(ctx, "get pvc failed", logit.String("error", err.Error()))
		return nil, err
	}
	pvcs := make([]*k8s.PvcBinding, 0, len(pvcBindings))
	for _, b := range pvcBindings {
		pvcs = append(pvcs, &k8s.PvcBinding{
			PvcName:   b.PvcName,
			MountPath: b.MountPath,
		})
	}

	// 解析环境变量
	envVars := make(map[string]string)
	if template.EnvirVars != "" {
		err = util.UnmarshalJSON(template.EnvirVars, &envVars)
		if err != nil {
			logger.Error(ctx, "unmarshal env vars failed", logit.String("error", err.Error()))
			return nil, err
		}
	}

	// 创建预热池记录
	poolItem := &model.SandboxRuntimePool{
		TemplateID:  template.ID,
		AppID:       template.AppID,
		SandboxUUID: sandboxUuid,
		Token:       token,
		LogUUID:     logUuid,
		Cluster:     clusterName,
		Status:      constant.PoolStatusPending,
		CreateTime:  time.Now(),
		Memory:      template.Memory,
		CPU:         template.CPU,
		Disk:        template.Disk,
		EnvirVars:   template.EnvirVars,
		ImageID:     template.ImageID,
		CodeBean:    template.CodeBean,
	}

	err = sandboxRuntimePoolDao.Create(poolItem)
	if err != nil {
		logger.Error(ctx, "create pool item failed", logit.String("error", err.Error()))
		return nil, err
	}

	logger.Notice(ctx, "pool item created", logit.Int64("id", poolItem.ID), logit.String("sandboxUUID", sandboxUuid))

	// 创建pod
	cluster := resource.ClusterManager.GetClusterByName(clusterName)
	if cluster == nil {
		errMsg := "cluster not found: " + clusterName
		logger.Error(ctx, errMsg)
		s.MarkPoolItemAsFailed(ctx, poolItem.ID, errMsg)
		return poolItem, nil
	}

	createPodFields := k8s.NewCreatePodFieldsBuilder(sandboxUuid).
		WithImage(template.Image, template.Type).
		WithResources(template.CPU, template.Memory, template.Disk).
		WithToken(token).
		WithLogUuid(logUuid).
		WithEnvVars(envVars).
		WithBindPvcs(pvcs).
		WithReadLessPort(util.GetReadProbPort(template.Type)).
		WithClusterName(clusterName).
		WithAppID(template.AppID).
		Build()
	resultChan := make(chan k8s.PodEvent, 1)

	err = cluster.CreatePod(createPodFields, resultChan)
	if err != nil {
		logger.Error(ctx, "create pool pod failed",
			logit.String("error", err.Error()))
		s.MarkPoolItemAsFailed(ctx, poolItem.ID, "创建pod失败: "+err.Error())
		return poolItem, nil
	}

	// 启动goroutine监听pod事件
	go func(poolItemID int64, sandboxUUID string) {
		bgCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		select {
		case data := <-resultChan:
			status := poolStatusMapping[data.PodPhase]
			poolItem.Status = poolStatusMapping[data.PodPhase]
			if status == constant.PoolStatusReady {
				s.UpdatePoolItemToReady(bgCtx, poolItemID, data)
				logger.Notice(bgCtx, "pool pod status updated",
					logit.String("sandboxUUID", sandboxUUID),
					logit.String("status", poolItem.Status))
			} else if poolItem.Status == constant.PoolStatusFailed {
				logger.Warning(bgCtx, "pool pod status fail",
					logit.String("sandboxUUID", sandboxUUID),
					logit.String("status", poolItem.Status))
				// 如果pod启动失败，删除k8s上的pod
				if data.PodPhase == corev1.PodFailed {
					cluster.DeletePod(sandboxUUID)
				}
			}

		case <-bgCtx.Done():
			logger.Error(bgCtx, "pool pod creation timeout",
				logit.String("sandboxUUID", sandboxUUID),
				logit.Int64("id", poolItemID))
			s.MarkPoolItemAsFailed(bgCtx, poolItemID, "pod创建超时")
			cluster.DeletePod(sandboxUUID)
		}

		// 清理事件通道
		cluster.UnRegisterSandboxEvent(sandboxUuid)
	}(poolItem.ID, sandboxUuid)
	return poolItem, nil
}

// UpdatePoolItemStatus 更新预热池pod状态
func (s *sandboxRuntimePoolService) UpdatePoolItemStatus(ctx context.Context, id int64, status string) error {
	sandboxRuntimePoolDao := dao.Use(resource.OrmMain).SandboxRuntimePool
	_, err := sandboxRuntimePoolDao.Where(sandboxRuntimePoolDao.ID.Eq(id)).Updates(
		map[string]interface{}{"status": status},
	)
	return err
}

// UpdatePoolItemToReady 更新预热池pod为就绪状态
func (s *sandboxRuntimePoolService) UpdatePoolItemToReady(ctx context.Context, poolID int64, podEvent k8s.PodEvent) error {
	sandboxRuntimePoolDao := dao.Use(resource.OrmMain).SandboxRuntimePool

	_, err := sandboxRuntimePoolDao.Where(sandboxRuntimePoolDao.ID.Eq(poolID)).Updates(
		map[string]interface{}{
			"status":       constant.PoolStatusReady,
			"start_time":   time.Now(),
			"port_ip":      podEvent.PortIp,
			"node_ip":      podEvent.NodeIp,
			"command_port": podEvent.CommandPort,
			"code_port":    podEvent.CodePort,
			"pod_name":     podEvent.PodName,
		},
	)
	return err
}

// MarkPoolItemAsUsed 标记预热池pod为已使用
func (s *sandboxRuntimePoolService) MarkPoolItemAsUsed(ctx context.Context, id int64) error {
	sandboxRuntimePoolDao := dao.Use(resource.OrmMain).SandboxRuntimePool

	_, err := sandboxRuntimePoolDao.Where(sandboxRuntimePoolDao.ID.Eq(id)).Updates(
		map[string]interface{}{
			"status":   constant.PoolStatusUsed,
			"use_time": time.Now(),
		},
	)
	return err
}

// MarkPoolItemAsFailed 标记预热池pod为失败
func (s *sandboxRuntimePoolService) MarkPoolItemAsFailed(ctx context.Context, id int64, reason string) error {
	sandboxRuntimePoolDao := dao.Use(resource.OrmMain).SandboxRuntimePool

	_, err := sandboxRuntimePoolDao.Where(sandboxRuntimePoolDao.ID.Eq(id)).Updates(
		map[string]interface{}{
			"status":        constant.PoolStatusFailed,
			"failed_reason": reason,
		},
	)
	return err
}

// GetPendingPoolItems 获取pending状态的预热池pod列表
func (s *sandboxRuntimePoolService) GetPendingPoolItems(ctx context.Context) ([]*model.SandboxRuntimePool, error) {
	sandboxRuntimePoolDao := dao.Use(resource.OrmMain).SandboxRuntimePool

	items, err := sandboxRuntimePoolDao.Where(
		sandboxRuntimePoolDao.Status.Eq(constant.PoolStatusPending),
	).Find()

	if err != nil {
		return nil, err
	}

	return items, nil
}

// GetReadyPoolItemsByTemplate 获取指定模板就绪状态的预热池pod列表
func (s *sandboxRuntimePoolService) GetReadyPoolItemsByTemplate(ctx context.Context, templateID int32) ([]*model.SandboxRuntimePool, error) {
	sandboxRuntimePoolDao := dao.Use(resource.OrmMain).SandboxRuntimePool

	items, err := sandboxRuntimePoolDao.Where(
		sandboxRuntimePoolDao.TemplateID.Eq(templateID),
		sandboxRuntimePoolDao.Status.Eq(constant.PoolStatusReady),
	).Order(sandboxRuntimePoolDao.StartTime.Asc()).Find()

	if err != nil {
		return nil, err
	}

	return items, nil
}

// CountReadyPoolItemsByTemplate 统计指定模板就绪状态的预热池pod数量
func (s *sandboxRuntimePoolService) CountReadyPoolItemsByTemplate(ctx context.Context, templateID int32) (int64, error) {
	sandboxRuntimePoolDao := dao.Use(resource.OrmMain).SandboxRuntimePool
	// 统计就绪状态且未被使用的预热池pod数量, TODO failed的预热pod得添加报警
	count, err := sandboxRuntimePoolDao.Where(
		sandboxRuntimePoolDao.TemplateID.Eq(templateID),
		sandboxRuntimePoolDao.Status.Neq(constant.PoolStatusUsed),
	).Count()

	return count, err
}

// DeletePoolItem 删除预热池pod
func (s *sandboxRuntimePoolService) DeletePoolItem(ctx context.Context, id int64) error {
	sandboxRuntimePoolDao := dao.Use(resource.OrmMain).SandboxRuntimePool

	_, err := sandboxRuntimePoolDao.Where(sandboxRuntimePoolDao.ID.Eq(id)).Delete()
	return err
}

// DeletePoolItemByUUID 通过UUID删除预热池pod
func (s *sandboxRuntimePoolService) DeletePoolItemByUUID(ctx context.Context, sandboxUUID string) error {
	sandboxRuntimePoolDao := dao.Use(resource.OrmMain).SandboxRuntimePool

	_, err := sandboxRuntimePoolDao.Where(sandboxRuntimePoolDao.SandboxUUID.Eq(sandboxUUID)).Delete()
	return err
}

// CleanExpiredPoolItems 清理过期的预热池pod
func (s *sandboxRuntimePoolService) CleanExpiredPoolItems(ctx context.Context, expireDuration time.Duration) (int, error) {
	logger := resource.LoggerService
	sandboxRuntimePoolDao := dao.Use(resource.OrmMain).SandboxRuntimePool

	// 查询就绪状态超过过期时间的pod
	expireTime := time.Now().Add(-expireDuration)
	expiredItems, err := sandboxRuntimePoolDao.Where(
		sandboxRuntimePoolDao.Status.Eq(constant.PoolStatusReady),
		sandboxRuntimePoolDao.StartTime.Lt(expireTime),
	).Find()

	if err != nil {
		logger.Error(ctx, "query expired pool items failed", logit.String("error", err.Error()))
		return 0, err
	}

	deletedCount := 0
	for _, item := range expiredItems {
		// 删除k8s pod
		if cluster := resource.ClusterManager.GetClusterByName(item.Cluster); cluster != nil {
			cluster.DeletePod(item.SandboxUUID)
		}
		// 删除数据库记录
		err := s.DeletePoolItem(ctx, item.ID)
		if err != nil {
			logger.Error(ctx, "delete pool item failed",
				logit.Int64("id", item.ID),
				logit.String("error", err.Error()))
			continue
		}
		deletedCount++
	}

	if deletedCount > 0 {
		logger.Notice(ctx, "cleaned expired pool items", logit.Int("count", deletedCount))
	}

	return deletedCount, nil
}

// CleanModifyTemplatePool 变更模版时,清理老版本的池化pod
func (s *sandboxRuntimePoolService) CleanModifyTemplatePool(ctx context.Context, templateID int32) {
	logger := resource.LoggerService
	logger.Error(ctx, "clear template pool start", logit.Int32("templateID", templateID))

	// 使用templateID粒度的锁，避免全局锁阻塞其他templateID的请求
	lockKey := constant.SandboxPoolAcquireLockKeyPrefix + strconv.Itoa(int(templateID))

	// 轮询等待获取锁，最多等待30秒
	maxRetry := 30
	retryInterval := 100 * time.Millisecond

	var locked bool
	var err error

	for i := 0; i < maxRetry; i++ {
		locked, err = resource.RedisClient.SetNX(ctx, lockKey, "1", 10*time.Second).Result()
		if err != nil {
			logger.Error(ctx, "acquire pool lock failed", logit.String("error", err.Error()))
			return
		}
		if locked {
			break
		}
		// 未获取到锁，等待一段时间后重试
		logger.Debug(ctx, "pool acquire lock already exist, waiting...",
			logit.Int32("templateID", templateID),
			logit.Int("retry", i+1))
		time.Sleep(retryInterval)
	}

	if !locked {
		logger.Warning(ctx, "failed to acquire pool lock after max retries",
			logit.Int32("templateID", templateID))
		return
	}
	defer resource.RedisClient.Del(ctx, lockKey)

	sandboxRuntimePoolDao := dao.Use(resource.OrmMain).SandboxRuntimePool

	poolItems, err := sandboxRuntimePoolDao.Where(
		sandboxRuntimePoolDao.TemplateID.Eq(templateID),
		sandboxRuntimePoolDao.Status.Eq(constant.PoolStatusReady),
	).Find()

	if err != nil {
		logger.Error(ctx, "query template pool items failed", logit.String("error", err.Error()))
		return
	}

	for _, item := range poolItems {
		logger.Error(ctx, "clear template pool instance",
			logit.Int32("templateID", templateID),
			logit.Int64("poolID", item.ID),
			logit.String("sandboxUUID", item.SandboxUUID),
		)
		// 删除k8s pod
		if cluster := resource.ClusterManager.GetClusterByName(item.Cluster); cluster != nil {
			cluster.DeletePod(item.SandboxUUID)
		}
		// 删除数据库记录
		err := s.DeletePoolItem(ctx, item.ID)
		if err != nil {
			logger.Error(ctx, "delete pool item failed",
				logit.Int64("id", item.ID),
				logit.String("error", err.Error()))
			continue
		}
	}
}
