package background

import (
	"context"
	"fmt"
	"time"

	"icode.baidu.com/baidu/gdp/logit"

	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/dao"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/resource"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/model"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/service"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util/constant"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util/envvar"
)

// ScheduledTaskExecutor 定时任务执行器：定期检查并执行到达时间点的定时任务
type ScheduledTaskExecutor struct {
	ctx           context.Context
	cancelFunc    context.CancelFunc
	checkInterval time.Duration
}

// NewScheduledTaskExecutor 创建新的定时任务执行器
func NewScheduledTaskExecutor(checkInterval time.Duration) *ScheduledTaskExecutor {
	ctx, cancel := context.WithCancel(context.Background())
	return &ScheduledTaskExecutor{
		ctx:           ctx,
		cancelFunc:    cancel,
		checkInterval: checkInterval,
	}
}

func init() {
	if envvar.IsDisableBackgroundTask() {
		return
	}
	// 创建定时任务执行器，每分钟检查一次
	executor := NewScheduledTaskExecutor(1 * time.Minute)
	executor.Start()
}

// Start 启动定时任务执行器
func (ste *ScheduledTaskExecutor) Start() {
	go ste.run()
}

// Stop 停止定时任务执行器
func (ste *ScheduledTaskExecutor) Stop() {
	ste.cancelFunc()
}

// 主工作逻辑
func (ste *ScheduledTaskExecutor) run() {
	<-resource.InitDone
	ticker := time.NewTicker(ste.checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// 执行定时任务检查（提前10分钟执行）
			ste.executeScheduledTasks()
		case <-ste.ctx.Done():
			return
		}
	}
}

// executeScheduledTasks 执行定时任务
// 1. 提前5分钟更新 running 状态沙箱的时间戳，防止空闲检测误杀
// 2. 查询所有到达执行时间（scheduled_time <= 当前时间）的定时任务并执行
func (ste *ScheduledTaskExecutor) executeScheduledTasks() {
	ctx := context.Background()
	logger := resource.LoggerService

	// 尝试获取分布式锁
	lock, err := resource.RedisClient.SetNX(ctx, constant.ScheduledTaskExecutorLockKey, "1", ste.checkInterval).Result()
	if err != nil {
		logger.Error(ctx, "set scheduled task executor lock failed", logit.String("error", err.Error()))
		return
	}
	if !lock {
		logger.Notice(ctx, "scheduled task executor lock already exist, skipping this round")
		return
	}

	logger.Notice(ctx, "starting scheduled task execution")

	currentTime := time.Now()
	currentTimeStr := currentTime.Format("2006-01-02 15:04")

	// ========== 步骤1: 提前5分钟更新 running 状态沙箱的时间戳 ==========
	// 查询未来5分钟内即将执行的定时任务，提前更新时间戳，防止空闲检测误杀
	ste.updateLastAccessForUpcomingTasks(ctx, currentTime, 5)

	// ========== 步骤2: 查询并执行到达时间的定时任务 ==========
	logger.Notice(ctx, "checking scheduled tasks for execution",
		logit.String("currentTime", currentTimeStr))

	// 查询所有到达执行时间的定时任务（scheduled_time <= 当前时间）
	scheduledTaskDao := dao.Use(resource.OrmMain).ScheduledTask
	tasks, err := scheduledTaskDao.Where(
		scheduledTaskDao.Deleted.Is(false),
		scheduledTaskDao.ScheduledTime.Lte(currentTimeStr),
	).Find()

	if err != nil {
		logger.Error(ctx, "failed to query scheduled tasks", logit.String("error", err.Error()))
		return
	}

	if len(tasks) == 0 {
		logger.Notice(ctx, "no scheduled tasks to execute", logit.String("currentTime", currentTimeStr))
		return
	}

	logger.Notice(ctx, "found scheduled tasks for execution",
		logit.Int("count", len(tasks)),
		logit.String("currentTime", currentTimeStr))

	// 查询对应的沙箱运行时信息
	sandboxRuntimeDao := dao.Use(resource.OrmMain).SandboxRuntime
	executedCount := 0
	skippedCount := 0

	for _, task := range tasks {
		// 查询沙箱状态
		sandboxRuntime, err := sandboxRuntimeDao.Where(
			sandboxRuntimeDao.ID.Eq(task.SandboxID),
		).First()

		if err != nil {
			logger.Error(ctx, "failed to get sandbox runtime",
				logit.Int64("sandboxId", task.SandboxID),
				logit.String("sandboxUUID", task.SandboxUUID),
				logit.String("error", err.Error()))
			continue
		}

		// 如果沙箱状态是 released，定时任务失效，不执行
		if sandboxRuntime.Status == constant.StatusReleased {
			logger.Warning(ctx, "skip scheduled task, sandbox is released (task disabled)",
				logit.Int64("sandboxId", task.SandboxID),
				logit.String("sandboxUUID", task.SandboxUUID),
				logit.String("scheduledTime", task.ScheduledTime))
			skippedCount++

			// 软删除 at 和 every 类型的定时任务
			ste.softDeleteOneTimeTask(ctx, task, "sandbox released")
			continue
		}

		// 如果沙箱状态是 running，说明实例正常运行，更新 last_access 时间戳
		if sandboxRuntime.Status == constant.StatusRunning {
			// 更新 last_access 时间戳，防止空闲检测误杀有定时任务的沙箱
			redisKey := "sandbox:last_access:" + sandboxRuntime.SandboxUUID
			err := resource.RedisClient.Set(ctx, redisKey, time.Now().Unix(), time.Hour*24).Err()
			if err != nil {
				logger.Error(ctx, "failed to update sandbox last_access",
					logit.Int64("sandboxId", task.SandboxID),
					logit.String("sandboxUUID", task.SandboxUUID),
					logit.String("error", err.Error()))
			} else {
				logger.Notice(ctx, "sandbox already running, updated last_access timestamp",
					logit.Int64("sandboxId", task.SandboxID),
					logit.String("sandboxUUID", task.SandboxUUID),
					logit.String("scheduledTime", task.ScheduledTime))
			}
			skippedCount++

			// 软删除 at 和 every 类型的定时任务
			ste.softDeleteOneTimeTask(ctx, task, "sandbox already running")
			continue
		}

		// 只对 kill 状态的沙箱进行恢复
		needRestoreStatuses := []string{constant.StatusKill}
		needRestore := false
		for _, status := range needRestoreStatuses {
			if sandboxRuntime.Status == status {
				needRestore = true
				break
			}
		}

		if !needRestore {
			logger.Notice(ctx, "skip scheduled task, sandbox status does not need restore",
				logit.Int64("sandboxId", task.SandboxID),
				logit.String("sandboxUUID", task.SandboxUUID),
				logit.String("status", sandboxRuntime.Status),
				logit.String("scheduledTime", task.ScheduledTime))
			skippedCount++
			continue
		}

		// 检查是否开启了 PVC 持久化（恢复实例需要 PVC）
		if !sandboxRuntime.UsePvc {
			logger.Warning(ctx, "sandbox has no PVC, cannot restore for scheduled task",
				logit.Int64("sandboxId", task.SandboxID),
				logit.String("sandboxUUID", task.SandboxUUID),
				logit.String("status", sandboxRuntime.Status))
			skippedCount++
			continue
		}

		// 尝试获取任务执行锁，防止重复执行同一任务
		taskLockKey := fmt.Sprintf("scheduled:task:executing:%d", task.ID)
		locked, lockErr := resource.RedisClient.SetNX(ctx, taskLockKey, "1", 10*time.Minute).Result()
		if lockErr != nil {
			logger.Error(ctx, "failed to acquire task execution lock",
				logit.Int64("taskId", task.ID),
				logit.String("sandboxUUID", task.SandboxUUID),
				logit.String("error", lockErr.Error()))
			skippedCount++
			continue
		}
		if !locked {
			logger.Notice(ctx, "task is already being executed by another instance",
				logit.Int64("taskId", task.ID),
				logit.String("sandboxUUID", task.SandboxUUID),
				logit.String("scheduledTime", task.ScheduledTime))
			skippedCount++
			continue
		}

		// 异步恢复实例，避免阻塞
		executedCount++
		go func(sr *model.SandboxRuntime, t *model.ScheduledTask, taskScheduledTime string, lockKey string) {
			restoreCtx := context.Background()

			// 确保锁最终被释放
			defer func() {
				delErr := resource.RedisClient.Del(restoreCtx, lockKey).Err()
				if delErr != nil {
					logger.Error(restoreCtx, "failed to release task execution lock",
						logit.Int64("taskId", t.ID),
						logit.String("lockKey", lockKey),
						logit.String("error", delErr.Error()))
				}
			}()

			err := ste.restoreSandboxForScheduledTask(restoreCtx, sr)
			if err != nil {
				logger.Error(restoreCtx, "failed to restore sandbox for scheduled task",
					logit.Int64("sandboxId", sr.ID),
					logit.String("sandboxUUID", sr.SandboxUUID),
					logit.String("status", sr.Status),
					logit.String("scheduledTime", taskScheduledTime),
					logit.String("error", err.Error()))
				return
			}

			logger.Notice(restoreCtx, "scheduled task executed successfully (sandbox restored)",
				logit.Int64("sandboxId", sr.ID),
				logit.String("sandboxUUID", sr.SandboxUUID),
				logit.String("sandboxName", sr.Name),
				logit.String("originalStatus", sr.Status),
				logit.String("scheduledTime", taskScheduledTime))

			// 恢复成功后，软删除 at 和 every 类型的定时任务（只执行一次）
			ste.softDeleteOneTimeTask(restoreCtx, t, "after restore success")
		}(sandboxRuntime, task, task.ScheduledTime, taskLockKey)
	}

	logger.Notice(ctx, "scheduled task execution completed",
		logit.Int("total", len(tasks)),
		logit.Int("executed", executedCount),
		logit.Int("skipped", skippedCount))
}

// updateLastAccessForUpcomingTasks 提前更新即将执行的定时任务的沙箱时间戳
// 防止空闲检测器在定时任务执行前停止沙箱
// advanceMinutes: 提前的分钟数
// 例如：advanceMinutes=15，当前时间 8:00，查询 8:15 的任务
func (ste *ScheduledTaskExecutor) updateLastAccessForUpcomingTasks(ctx context.Context, currentTime time.Time, advanceMinutes int) {
	logger := resource.LoggerService

	// 计算提前的时间点（YYYY-MM-DD HH:MM格式）
	advanceTime := currentTime.Add(time.Duration(advanceMinutes) * time.Minute)
	advanceTimeStr := advanceTime.Format("2006-01-02 15:04")

	logger.Notice(ctx, "updating last_access for upcoming scheduled tasks",
		logit.Int("advanceMinutes", advanceMinutes),
		logit.String("currentTime", currentTime.Format("2006-01-02 15:04")),
		logit.String("targetTime", advanceTimeStr))

	// 查询指定时间点的定时任务
	scheduledTaskDao := dao.Use(resource.OrmMain).ScheduledTask
	tasks, err := scheduledTaskDao.Where(
		scheduledTaskDao.Deleted.Is(false),
		scheduledTaskDao.ScheduledTime.Eq(advanceTimeStr),
	).Find()

	if err != nil {
		logger.Error(ctx, "failed to query upcoming scheduled tasks", logit.String("error", err.Error()))
		return
	}

	if len(tasks) == 0 {
		logger.Notice(ctx, "no upcoming scheduled tasks found")
		return
	}

	logger.Notice(ctx, "found upcoming scheduled tasks",
		logit.Int("count", len(tasks)))

	// 提取所有沙箱ID,后续统计
	var sandboxIDs []int64
	for _, task := range tasks {
		sandboxIDs = append(sandboxIDs, task.SandboxID)
	}

	// 查询这些沙箱的状态（只查询 running 状态的）
	sandboxRuntimeDao := dao.Use(resource.OrmMain).SandboxRuntime
	sandboxes, err := sandboxRuntimeDao.Where(
		sandboxRuntimeDao.ID.In(sandboxIDs...),
		sandboxRuntimeDao.Status.Eq(constant.StatusRunning),
	).Find()

	if err != nil {
		logger.Error(ctx, "failed to query sandbox runtimes", logit.String("error", err.Error()))
		return
	}

	// 批量更新 running 状态沙箱的 last_access 时间戳
	updatedCount := 0
	for _, sandbox := range sandboxes {
		redisKey := "sandbox:last_access:" + sandbox.SandboxUUID
		err := resource.RedisClient.Set(ctx, redisKey, time.Now().Unix(), time.Hour*24).Err()
		if err != nil {
			logger.Error(ctx, "failed to update sandbox last_access (upcoming task)",
				logit.Int64("sandboxId", sandbox.ID),
				logit.String("sandboxUUID", sandbox.SandboxUUID),
				logit.String("error", err.Error()))
		} else {
			logger.Notice(ctx, "updated last_access for upcoming scheduled task",
				logit.Int64("sandboxId", sandbox.ID),
				logit.String("sandboxUUID", sandbox.SandboxUUID))
			updatedCount++
		}
	}

	logger.Notice(ctx, "updated last_access for upcoming scheduled tasks",
		logit.Int("updatedCount", updatedCount),
		logit.Int("totalUpcomingTasks", len(tasks)))
}

// softDeleteOneTimeTask 处理已执行的定时任务
// - at 和 every 类型：软删除（一次性任务）
// - cron 类型：将 scheduled_time 加 1 天（每日重复任务）
func (ste *ScheduledTaskExecutor) softDeleteOneTimeTask(ctx context.Context, task *model.ScheduledTask, reason string) {
	logger := resource.LoggerService
	scheduledTaskDao := dao.Use(resource.OrmMain).ScheduledTask

	// 处理 at 和 every 类型：软删除
	if task.RepeatMode == "at" || task.RepeatMode == "every" {
		_, err := scheduledTaskDao.Where(scheduledTaskDao.ID.Eq(task.ID)).Update(scheduledTaskDao.Deleted, true)
		if err != nil {
			logger.Error(ctx, "failed to soft delete scheduled task",
				logit.Int64("taskId", task.ID),
				logit.String("repeatMode", task.RepeatMode),
				logit.String("reason", reason),
				logit.String("error", err.Error()))
		} else {
			logger.Notice(ctx, "soft deleted scheduled task",
				logit.Int64("taskId", task.ID),
				logit.String("repeatMode", task.RepeatMode),
				logit.String("sandboxUUID", task.SandboxUUID),
				logit.String("reason", reason))
		}
		return
	}

	// 处理 cron 类型：将 scheduled_time 加 1 天
	if task.RepeatMode == "cron" {
		// 解析任务的 scheduled_time
		scheduledTime, err := time.Parse("2006-01-02 15:04", task.ScheduledTime)
		if err != nil {
			logger.Error(ctx, "failed to parse scheduled_time for cron task",
				logit.Int64("taskId", task.ID),
				logit.String("scheduledTime", task.ScheduledTime),
				logit.String("error", err.Error()))
			return
		}

		// 将 scheduled_time 加 1 天
		nextScheduledTime := scheduledTime.Add(24 * time.Hour)
		nextScheduledTimeStr := nextScheduledTime.Format("2006-01-02 15:04")

		// 更新数据库
		_, err = scheduledTaskDao.Where(scheduledTaskDao.ID.Eq(task.ID)).Update(scheduledTaskDao.ScheduledTime, nextScheduledTimeStr)
		if err != nil {
			logger.Error(ctx, "failed to update scheduled_time for cron task",
				logit.Int64("taskId", task.ID),
				logit.String("oldScheduledTime", task.ScheduledTime),
				logit.String("newScheduledTime", nextScheduledTimeStr),
				logit.String("error", err.Error()))
		} else {
			logger.Notice(ctx, "updated scheduled_time for cron task (+1 day)",
				logit.Int64("taskId", task.ID),
				logit.String("repeatMode", task.RepeatMode),
				logit.String("sandboxUUID", task.SandboxUUID),
				logit.String("oldScheduledTime", task.ScheduledTime),
				logit.String("newScheduledTime", nextScheduledTimeStr),
				logit.String("reason", reason))
		}
		return
	}

	// 其他未知类型，记录日志
	logger.Warning(ctx, "unknown repeat mode, no action taken",
		logit.Int64("taskId", task.ID),
		logit.String("repeatMode", task.RepeatMode),
		logit.String("sandboxUUID", task.SandboxUUID))
}

// restoreSandboxForScheduledTask 为定时任务恢复沙箱实例
func (ste *ScheduledTaskExecutor) restoreSandboxForScheduledTask(ctx context.Context, sandboxRuntime *model.SandboxRuntime) error {
	logger := resource.LoggerService

	// 创建包含用户信息的 context
	ctxWithUser := context.WithValue(ctx, constant.CtxLoginUserKey, sandboxRuntime.CreateUser)

	// 使用 SuperAdmin 角色调用 RestoreSandboxRuntime，明确传入触发类型为 scheduled
	sandboxRuntimeService := service.NewSandboxRuntimeService()
	_, err := sandboxRuntimeService.RestoreSandboxRuntime(ctxWithUser, sandboxRuntime.ID, constant.RoleSuperAdmin, constant.TriggerTypeScheduled, nil)
	if err != nil {
		return fmt.Errorf("failed to restore sandbox: %w", err)
	}

	logger.Notice(ctx, "sandbox restore initiated for scheduled task",
		logit.Int64("sandboxId", sandboxRuntime.ID),
		logit.String("sandboxUUID", sandboxRuntime.SandboxUUID))

	return nil
}
