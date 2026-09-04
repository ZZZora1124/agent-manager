package background

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"icode.baidu.com/baidu/gdp/env"
	"icode.baidu.com/baidu/gdp/logit"

	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/common"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/dao"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/card"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/resource"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/model"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/service"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util/constant"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util/envvar"
)

const (
	// SandboxIdleCheckInterval 沙箱空闲检查间隔
	SandboxIdleCheckInterval = 1 * time.Minute
	// SandboxIdleThreshold 沙箱空闲超时阈值（统一使用3小时）
	// 通过调整 last_access 起始时间来实现不同的休眠时长：
	// - 定时唤醒：last_access = now() - 2h，实际1小时后休眠
	// - 手动唤醒：last_access = now()，实际3小时后休眠
	SandboxIdleThreshold = 3 * time.Hour
)

// SandboxIdleTimer 沙箱空闲超时检测器：定期检测沙箱的活跃度
type SandboxIdleTimer struct {
	ctx           context.Context
	cancelFunc    context.CancelFunc
	checkInterval time.Duration
	idleThreshold time.Duration
}

// NewSandboxIdleTimer 创建新的沙箱空闲检测器
func NewSandboxIdleTimer(checkInterval, idleThreshold time.Duration) *SandboxIdleTimer {
	ctx, cancel := context.WithCancel(context.Background())
	return &SandboxIdleTimer{
		ctx:           ctx,
		cancelFunc:    cancel,
		checkInterval: checkInterval,
		idleThreshold: idleThreshold,
	}
}

func init() {
	if envvar.IsDisableBackgroundTask() {
		return
	}
	// 创建沙箱空闲检测器，每分钟检查一次 app_id=1 且 status=running 的沙箱
	idleTimer := NewSandboxIdleTimer(SandboxIdleCheckInterval, SandboxIdleThreshold)
	idleTimer.Start()
}

// Start 启动沙箱空闲检测器
func (sit *SandboxIdleTimer) Start() {
	go sit.run()
}

// Stop 停止沙箱空闲检测器
func (sit *SandboxIdleTimer) Stop() {
	sit.cancelFunc()
}

// run 主工作逻辑
func (sit *SandboxIdleTimer) run() {
	<-resource.InitDone
	ticker := time.NewTicker(sit.checkInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			sit.checkIdleSandboxes()
		case <-sit.ctx.Done():
			return
		}
	}
}

// checkIdleSandboxes 检查空闲的沙箱
func (sit *SandboxIdleTimer) checkIdleSandboxes() {
	ctx := context.Background()
	resource.LoggerService.Notice(ctx, "starting sandbox idle check")

	lock, err := resource.RedisClient.SetNX(ctx, constant.SandboxIdleCheckLockKey, "1", sit.checkInterval).Result()
	if err != nil {
		resource.LoggerService.Error(ctx, "set sandbox idle check lock failed", logit.String("error", err.Error()))
		return
	}
	if !lock {
		resource.LoggerService.Warning(ctx, "sandbox idle check lock already exist")
		return
	}
	resource.LoggerService.Notice(ctx, "starting sandbox idle check, lock set success")

	sandboxRuntimeDao := dao.Use(resource.OrmMain).SandboxRuntime
	var appID int32
	if env.RunMode() == env.RunModeRelease {
		appID = 2071
	} else {
		appID = 7
	}
	// 查询 app_id=1 且 status=running 的沙箱
	runningSandboxes, err := sandboxRuntimeDao.Where(
		sandboxRuntimeDao.AppID.Eq(appID),
		sandboxRuntimeDao.Status.Eq(constant.StatusRunning),
	).Find()
	if err != nil {
		resource.LoggerService.Error(ctx, "query running sandboxes failed", logit.String("error", err.Error()))
		return
	}
	resource.LoggerService.Notice(ctx, "sandbox idle check", logit.Int("count", len(runningSandboxes)))

	idleCount := 0
	currentTime := time.Now()

	for _, sandbox := range runningSandboxes {
		// 检查创建人是否在非休眠白名单中
		if common.IsNoIdleReleaseUser(sandbox.CreateUser) {
			resource.LoggerService.Notice(ctx, "sandbox creator is in no-idle-release whitelist, skip",
				logit.String("sandboxID", sandbox.SandboxUUID),
				logit.String("createUser", sandbox.CreateUser))
			continue
		}

		// 构建 Redis key: sandbox:last_access:{sandbox_uuid}
		redisKey := "sandbox:last_access:" + sandbox.SandboxUUID

		// 获取 Redis 中的时间戳
		timestampStr, err := resource.RedisClient.Get(ctx, redisKey).Result()
		if err != nil {
			// key 不存在，认为需要执行操作
			resource.LoggerService.Notice(ctx, "sandbox last_access key not found, executing action",
				logit.String("sandboxID", sandbox.SandboxUUID),
				logit.String("redisKey", redisKey))
			sit.executeIdleAction(ctx, sandbox)
			idleCount++
			continue
		}

		// 解析时间戳
		timestamp, err := strconv.ParseInt(timestampStr, 10, 64)
		if err != nil {
			resource.LoggerService.Error(ctx, "parse timestamp failed",
				logit.String("sandboxID", sandbox.SandboxUUID),
				logit.String("timestamp", timestampStr),
				logit.String("error", err.Error()))
			continue
		}

		// 判断是否超过空闲阈值（统一使用3小时阈值）
		// 定时唤醒和手动唤醒通过调整 last_access 起始时间实现不同休眠时长：
		// - 定时唤醒：last_access = now() - 2h，配合3h阈值实现1小时后休眠
		// - 手动唤醒：last_access = now()，配合3h阈值实现3小时后休眠
		lastAccessTime := time.Unix(timestamp, 0)
		idleDuration := currentTime.Sub(lastAccessTime)
		idleThreshold := SandboxIdleThreshold

		if idleDuration >= idleThreshold {
			resource.LoggerService.Notice(ctx, "sandbox idle timeout, executing action",
				logit.String("sandboxID", sandbox.SandboxUUID),
				logit.String("lastAccess", lastAccessTime.Format(time.RFC3339)),
				logit.String("idleDuration", idleDuration.String()),
				logit.String("idleThreshold", idleThreshold.String()))
			sit.executeIdleAction(ctx, sandbox)
			idleCount++
		} else {
			resource.LoggerService.Debug(ctx, "sandbox is active",
				logit.String("sandboxID", sandbox.SandboxUUID),
				logit.String("idleDuration", idleDuration.String()),
				logit.String("idleThreshold", idleThreshold.String()))
		}
	}

	resource.LoggerService.Notice(ctx, "sandbox idle check completed", logit.Int("idleCount", idleCount))
}

// executeIdleAction 执行空闲沙箱的操作
func (sit *SandboxIdleTimer) executeIdleAction(ctx context.Context, sandbox *model.SandboxRuntime) {
	resource.LoggerService.Notice(ctx, "executing idle action for sandbox",
		logit.String("sandboxID", sandbox.SandboxUUID),
		logit.Int64("sandboxID_db", sandbox.ID),
		logit.Int32("appID", sandbox.AppID),
		logit.String("status", sandbox.Status))

	var envirVars map[string]any
	if err := json.Unmarshal([]byte(sandbox.EnvirVars), &envirVars); err != nil {
		resource.LoggerService.Error(ctx, "failed to parse envir_vars",
			logit.String("sandboxID", sandbox.SandboxUUID),
			logit.String("envirVars", sandbox.EnvirVars),
			logit.String("error", err.Error()))
		return
	}

	// 获取 ak 和 sk
	appKey, ok := envirVars["INFOFLOW_APP_KEY"].(string)
	if !ok || appKey == "" {
		envirVarsJSON, _ := json.Marshal(envirVars)
		resource.LoggerService.Error(ctx, "INFOFLOW_APP_KEY not found in envir_vars",
			logit.String("sandboxID", sandbox.SandboxUUID),
			logit.String("envirVars", string(envirVarsJSON)))
		return
	}

	appSecret, ok := envirVars["INFOFLOW_APP_SECRET"].(string)
	if !ok || appSecret == "" {
		envirVarsJSON, _ := json.Marshal(envirVars)
		resource.LoggerService.Error(ctx, "INFOFLOW_APP_SECRET not found in envir_vars",
			logit.String("sandboxID", sandbox.SandboxUUID),
			logit.String("envirVars", string(envirVarsJSON)))
		return
	}

	// 检查是否应该跳过发送消息（包括白名单和auto_wake检查）
	if common.ShouldSkipSendingRestartMessage(ctx, sandbox.CreateUser, sandbox.SandboxUUID, resource.LoggerService) {
		resource.LoggerService.Notice(ctx, "skip sending idle warning card due to whitelist or auto_wake status",
			logit.String("sandboxID", sandbox.SandboxUUID),
			logit.String("createUser", sandbox.CreateUser))
	} else {
		// 只在不跳过时才发送卡片
		go func() {
			client := &card.SendCardClient{}
			sandboxID := strconv.FormatInt(sandbox.ID, 10)
			err := client.SendCardMsg(sandbox.CreateUser, appKey, appSecret, sandboxID)
			if err != nil {
				resource.LoggerService.Error(ctx, "send idle warning card failed",
					logit.String("sandboxID", sandbox.SandboxUUID),
					logit.String("createUser", sandbox.CreateUser),
					logit.String("error", err.Error()))
			} else {
				resource.LoggerService.Notice(ctx, "send idle warning card success",
					logit.String("sandboxID", sandbox.SandboxUUID),
					logit.String("receiver", sandbox.CreateUser))
			}
		}()
	}

	// 释放沙箱
	ctx = context.WithValue(ctx, constant.CtxLoginUserKey, sandbox.CreateUser)
	sandboxRuntimeService := service.NewSandboxRuntimeService()
	_, err := sandboxRuntimeService.ReleaseSandboxRuntime(ctx, sandbox.ID, "", "idle_kill", false)
	if err != nil {
		resource.LoggerService.Error(ctx, "release sandbox runtime failed",
			logit.String("sandboxID", sandbox.SandboxUUID),
			logit.Int64("sandboxID_db", sandbox.ID),
			logit.String("error", err.Error()))
		return
	}

	resource.LoggerService.Notice(ctx, "release sandbox runtime success",
		logit.String("sandboxID", sandbox.SandboxUUID),
		logit.Int64("sandboxID_db", sandbox.ID))
}
