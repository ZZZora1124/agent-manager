package background

import (
	"context"
	"time"

	"icode.baidu.com/baidu/gdp/logit"

	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/dao"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/resource"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/service"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util/constant"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util/envvar"
)

// SandboxPoolPreheater 预热池管理器：定期扫描template表，创建预热pod
type SandboxPoolPreheater struct {
	ctx            context.Context
	cancelFunc     context.CancelFunc
	checkInterval  time.Duration
	poolExpireTime time.Duration
}

// NewSandboxPoolPreheater 创建新的预热池管理器
func NewSandboxPoolPreheater(checkInterval, poolExpireTime time.Duration) *SandboxPoolPreheater {
	ctx, cancel := context.WithCancel(context.Background())
	return &SandboxPoolPreheater{
		ctx:            ctx,
		cancelFunc:     cancel,
		checkInterval:  checkInterval,
		poolExpireTime: poolExpireTime,
	}
}

var poolPreheater *SandboxPoolPreheater

func init() {
	if envvar.IsDisableBackgroundTask() {
		return
	}
	// 创建预热池管理器，每隔30秒检查一次需要预热的模板，预热pod过期时间为12小时
	poolPreheater = NewSandboxPoolPreheater(10*time.Second, 12*time.Hour)
	poolPreheater.Start()
}

// Start 启动预热池管理器
func (sp *SandboxPoolPreheater) Start() {
	go sp.run()
}

// Stop 停止预热池管理器
func (sp *SandboxPoolPreheater) Stop() {
	sp.cancelFunc()
}

// 主工作逻辑
func (sp *SandboxPoolPreheater) run() {
	<-resource.InitDone
	ticker := time.NewTicker(sp.checkInterval)
	defer ticker.Stop()

	// 首次启动时，先清理一次过期的预热pod
	time.AfterFunc(5*time.Second, func() {
		ctx := context.Background()
		poolService := service.NewSandboxRuntimePoolService()
		poolService.CleanExpiredPoolItems(ctx, sp.poolExpireTime)
	})

	for {
		select {
		case templateID := <-resource.PoolReplenishChan:
			// 收到pod被取走的通知，立即为该模板补充pod
			sp.replenishTemplate(templateID)
		case templateID := <-resource.PoolChangeChan:
			// 收到模版镜像变更时间，清理该模板的pod
			sp.clearTemplatePoll(templateID)
		case <-ticker.C:
			// 定期扫描所有需要预热的模板
			sp.preheatTemplates()
			sp.cleanExpiredPoolItems()
		case <-sp.ctx.Done():
			return
		}
	}
}

// preheatTemplates 扫描template表，为需要预热的模板创建pod
func (sp *SandboxPoolPreheater) preheatTemplates() {
	ctx := context.Background()
	logger := resource.LoggerService
	logger.Notice(ctx, "starting template preheat scan")

	// 尝试获取分布式锁
	lock, err := resource.RedisClient.SetNX(ctx, constant.SandboxPoolPreheatLockKey, "1", sp.checkInterval).Result()
	if err != nil {
		logger.Error(ctx, "set pool preheat lock failed", logit.String("error", err.Error()))
		return
	}
	if !lock {
		logger.Debug(ctx, "pool preheat lock already exist, skipping this round")
		return
	}
	logger.Notice(ctx, "pool preheat lock acquired")

	defer func() {
		// 锁会自动过期，这里不做主动释放
	}()

	templateDao := dao.Use(resource.OrmMain).Template
	poolService := service.NewSandboxRuntimePoolService()

	// 查询所有设置了预热个数且未删除的模板
	templates, err := templateDao.Where(
		templateDao.PreheatCount.Gt(0),
		templateDao.Deleted.Is(false),
	).Find()

	if err != nil {
		logger.Error(ctx, "query templates failed", logit.String("error", err.Error()))
		return
	}

	logger.Notice(ctx, "found templates with preheat count", logit.Int("count", len(templates)))

	for _, tpl := range templates {
		// 统计当前模板在预热池中就绪状态的pod数量
		readyCount, err := poolService.CountReadyPoolItemsByTemplate(ctx, tpl.ID)
		if err != nil {
			logger.Error(ctx, "count ready pool items failed",
				logit.Int32("templateID", tpl.ID),
				logit.String("error", err.Error()))
			continue
		}

		// 计算需要创建的pod数量
		needCreateCount := tpl.PreheatCount - int32(readyCount)

		if needCreateCount <= 0 {
			logger.Debug(ctx, "template pool is full",
				logit.Int32("templateID", tpl.ID),
				logit.String("templateName", tpl.Name),
				logit.Int("readyCount", int(readyCount)),
				logit.Int("preheatCount", int(tpl.PreheatCount)))
			continue
		}

		logger.Notice(ctx, "creating pool items for template",
			logit.Int32("templateID", tpl.ID),
			logit.String("templateName", tpl.Name),
			logit.Int("readyCount", int(readyCount)),
			logit.Int("needCreateCount", int(needCreateCount)),
			logit.Int("preheatCount", int(tpl.PreheatCount)))

		// 创建缺失的预热pod
		for i := int32(0); i < needCreateCount; i++ {
			go poolService.CreatePoolItem(ctx, tpl)
		}
	}

	logger.Notice(ctx, "template preheat scan completed")
}

// replenishTemplate 为指定模板补充预热pod（立即响应模式）
func (sp *SandboxPoolPreheater) replenishTemplate(templateID int32) {
	ctx := context.Background()
	logger := resource.LoggerService
	logger.Notice(ctx, "triggering immediate replenish for template",
		logit.Int32("templateID", templateID))

	templateDao := dao.Use(resource.OrmMain).Template
	poolService := service.NewSandboxRuntimePoolService()

	// 查询指定模板
	tpl, err := templateDao.Where(
		templateDao.ID.Eq(templateID),
		templateDao.PreheatCount.Gt(0),
		templateDao.Deleted.Is(false),
	).First()

	if err != nil {
		logger.Debug(ctx, "template not found or no preheat config",
			logit.Int32("templateID", templateID),
			logit.String("error", err.Error()))
		return
	}

	// 统计当前模板在预热池中就绪状态的pod数量
	readyCount, err := poolService.CountReadyPoolItemsByTemplate(ctx, tpl.ID)
	if err != nil {
		logger.Error(ctx, "count ready pool items failed",
			logit.Int32("templateID", tpl.ID),
			logit.String("error", err.Error()))
		return
	}

	// 计算需要创建的pod数量
	needCreateCount := tpl.PreheatCount - int32(readyCount)

	if needCreateCount <= 0 {
		logger.Debug(ctx, "template pool is already full",
			logit.Int32("templateID", tpl.ID),
			logit.String("templateName", tpl.Name),
			logit.Int("readyCount", int(readyCount)),
			logit.Int("preheatCount", int(tpl.PreheatCount)))
		return
	}

	logger.Notice(ctx, "immediately creating pool items for template",
		logit.Int32("templateID", tpl.ID),
		logit.String("templateName", tpl.Name),
		logit.Int("readyCount", int(readyCount)),
		logit.Int("needCreateCount", int(needCreateCount)),
		logit.Int("preheatCount", int(tpl.PreheatCount)))

	// 创建缺失的预热pod
	for i := 0; i < int(needCreateCount); i++ {
		go poolService.CreatePoolItem(ctx, tpl)
	}
}

// cleanExpiredPoolItems 清理过期的预热池pod
func (sp *SandboxPoolPreheater) cleanExpiredPoolItems() {
	ctx := context.Background()
	logger := resource.LoggerService

	// 尝试获取分布式锁
	lock, err := resource.RedisClient.SetNX(ctx, constant.SandboxPoolCleanLockKey, "1", sp.checkInterval).Result()
	if err != nil {
		logger.Error(ctx, "set pool clean lock failed", logit.String("error", err.Error()))
		return
	}
	if !lock {
		logger.Debug(ctx, "pool clean lock already exist, skipping this round")
		return
	}
	logger.Notice(ctx, "pool clean lock acquired")

	poolService := service.NewSandboxRuntimePoolService()
	deletedCount, err := poolService.CleanExpiredPoolItems(ctx, sp.poolExpireTime)
	if err != nil {
		logger.Error(ctx, "clean expired pool items failed", logit.String("error", err.Error()))
		return
	}

	if deletedCount > 0 {
		logger.Notice(ctx, "expired pool items cleaned", logit.Int("count", deletedCount))
	} else {
		logger.Debug(ctx, "no expired pool items to clean")
	}
}

func (sp *SandboxPoolPreheater) clearTemplatePoll(templateID int32) {
	poolService := service.NewSandboxRuntimePoolService()
	poolService.CleanModifyTemplatePool(context.Background(), templateID)
}
