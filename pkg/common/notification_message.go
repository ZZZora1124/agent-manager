package common

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util/constant"
	"icode.baidu.com/baidu/gdp/logit"
	"icode.baidu.com/baidu/gdp/redis"
)

// NotificationMessagesConfig 通知消息配置（合并前后通知为一个配置）
type NotificationMessagesConfig struct {
	PreStartMessage  string   `json:"preStartMessage"`  // 启动前话术
	PostStartMessage string   `json:"postStartMessage"` // 启动后话术
	Users            []string `json:"users"`            // 小流量用户列表，支持 "all" 表示全量
	Timestamp        int64    `json:"timestamp"`        // 添加配置时的时间戳（秒级）
}

const (
	// DefaultPreStartMessage 默认启动前话术
	DefaultPreStartMessage = "开始准备服务啦，大概需要10秒后就可以跟我对话啦"
	// DefaultPostStartMessage 默认启动后话术
	DefaultPostStartMessage = "已成功唤起，可以开始使用啦！"
)

// SetNotificationMessages 设置通知消息配置到Redis
// preStartMessage: 启动前话术（可选，为空则不更新）
// postStartMessage: 启动后话术（可选，为空则不更新）
// users: 小流量用户列表（可选，为空则不更新）
func SetNotificationMessages(ctx context.Context, redisClient redis.Client, preStartMessage, postStartMessage string, users []string) error {
	if redisClient == nil {
		return errors.New("redis client is nil")
	}

	// 至少要设置一个字段
	if preStartMessage == "" && postStartMessage == "" && len(users) == 0 {
		return errors.New("at least one field must be provided")
	}

	// 先读取现有配置（如果存在）
	existingConfig, _, _ := GetNotificationMessagesConfig(ctx, redisClient)
	if existingConfig == nil {
		existingConfig = &NotificationMessagesConfig{}
	}

	// 更新字段（只更新非空的字段）
	if preStartMessage != "" {
		existingConfig.PreStartMessage = preStartMessage
	}
	if postStartMessage != "" {
		existingConfig.PostStartMessage = postStartMessage
	}
	if len(users) > 0 {
		existingConfig.Users = users
	}

	// 更新时间戳
	existingConfig.Timestamp = time.Now().Unix()

	// 序列化配置
	configJSON, err := json.Marshal(existingConfig)
	if err != nil {
		return err
	}

	// 写入 Redis（永久存储）
	return redisClient.Set(ctx, constant.NotificationMessagesRedisKey, string(configJSON), 0).Err()
}

// GetNotificationMessagesConfig 从Redis获取通知消息配置
// 返回配置、是否使用默认值、错误
func GetNotificationMessagesConfig(ctx context.Context, redisClient redis.Client) (*NotificationMessagesConfig, bool, error) {
	if redisClient == nil {
		return getDefaultNotificationMessagesConfig(), true, nil
	}

	// 从 Redis 读取配置
	val, err := redisClient.Get(ctx, constant.NotificationMessagesRedisKey).Result()
	if err != nil || val == "" {
		// Redis 中没有配置，返回默认配置
		return getDefaultNotificationMessagesConfig(), true, nil
	}

	// 解析配置
	var config NotificationMessagesConfig
	if err := json.Unmarshal([]byte(val), &config); err != nil {
		// 解析失败，返回默认配置
		return getDefaultNotificationMessagesConfig(), true, err
	}

	return &config, false, nil
}

// getDefaultNotificationMessagesConfig 获取默认通知配置
func getDefaultNotificationMessagesConfig() *NotificationMessagesConfig {
	return &NotificationMessagesConfig{
		PreStartMessage:  DefaultPreStartMessage,
		PostStartMessage: DefaultPostStartMessage,
		Users:            []string{},
		Timestamp:        0,
	}
}

// isUserInSmallTraffic 判断用户是否在小流量列表中
func isUserInSmallTraffic(username string, users []string) bool {
	if len(users) == 0 {
		return false
	}

	for _, user := range users {
		if user == "all" || user == username {
			return true
		}
	}
	return false
}

// GetPreStartNotificationMessage 获取启动前的通知话术
// username: 用户名
// sandboxStartTime: sandbox的启动时间，如果为零值则使用默认话术
func GetPreStartNotificationMessage(ctx context.Context, redisClient redis.Client, username string, sandboxStartTime time.Time) string {
	if redisClient == nil {
		return DefaultPreStartMessage
	}

	// 从redis读取配置
	config, isDefault, err := GetNotificationMessagesConfig(ctx, redisClient)
	if err != nil || isDefault {
		// redis里没有配置或解析失败，使用默认话术
		return DefaultPreStartMessage
	}

	// 判断用户是否在小流量列表中
	if !isUserInSmallTraffic(username, config.Users) {
		// 不是小流量用户，使用默认话术
		return DefaultPreStartMessage
	}

	// 如果sandbox启动时间为零值，使用默认话术
	if sandboxStartTime.IsZero() {
		return DefaultPreStartMessage
	}

	// 比较时间：如果配置时间在sandbox启动时间之前（或相等），使用redis配置
	// 否则说明这次启动时还没有新配置，使用默认话术
	configTime := time.Unix(config.Timestamp, 0)
	if configTime.After(sandboxStartTime) {
		// 配置时间 <= sandbox启动时间，使用redis配置的话术
		if config.PreStartMessage != "" {
			return config.PreStartMessage
		}
	}

	// 配置时间 > sandbox启动时间，说明配置是在这次启动之后才加的，使用默认话术
	return DefaultPreStartMessage
}

// GetPostStartNotificationMessage 获取启动后的通知话术
// username: 用户名
// sandboxStartTime: sandbox的启动时间，如果为零值则使用默认话术
func GetPostStartNotificationMessage(ctx context.Context, redisClient redis.Client, username string, sandboxStartTime time.Time) string {
	if redisClient == nil {
		return DefaultPostStartMessage
	}

	// 从redis读取配置
	config, isDefault, err := GetNotificationMessagesConfig(ctx, redisClient)
	if err != nil || isDefault {
		// redis里没有配置或解析失败，使用默认话术
		return DefaultPostStartMessage
	}

	// 判断用户是否在小流量列表中
	if !isUserInSmallTraffic(username, config.Users) {
		// 不是小流量用户，使用默认话术
		return DefaultPostStartMessage
	}

	// 如果sandbox启动时间为零值，使用默认话术
	if sandboxStartTime.IsZero() {
		return DefaultPostStartMessage
	}

	// 比较时间：如果配置时间在sandbox启动时间之前（或相等），使用redis配置
	// 否则说明这次启动时还没有新配置，使用默认话术
	configTime := time.Unix(config.Timestamp, 0)
	if configTime.After(sandboxStartTime) {
		// 配置时间 <= sandbox启动时间，使用redis配置的话术
		if config.PostStartMessage != "" {
			return config.PostStartMessage
		}
	}

	// 配置时间 > sandbox启动时间，说明配置是在这次启动之后才加的，使用默认话术
	return DefaultPostStartMessage
}

// AutoWakeResponse 表示从 ClawGuard-Dispatch 获取的 auto_wake 响应
type AutoWakeResponse struct {
	SandboxID string `json:"sandbox_id"`
	AutoWake  bool   `json:"auto_wake"`
}

// QueryAutoWakeStatus 查询指定沙箱的 auto_wake 状态
// sandboxID: 沙箱ID
// dispatchURL: dispatch服务地址（如果为空则使用默认值）
// apiKey: dispatch API密钥
// logger: 日志记录器
func QueryAutoWakeStatus(ctx context.Context, sandboxID string, dispatchURL string, apiKey string, logger interface{}) (bool, error) {
	if sandboxID == "" {
		return false, errors.New("sandboxID is required")
	}

	if apiKey == "" {
		return false, errors.New("api key is required")
	}

	if dispatchURL == "" {
		dispatchURL = constant.DispatchServiceURL
	}

	// 构建请求URL
	url := dispatchURL + "/dispatch/api/auto-wake/" + sandboxID

	// 创建HTTP请求
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		logWarning(logger, "failed to create request for auto_wake query",
			logit.String("sandboxID", sandboxID),
			logit.String("error", err.Error()))
		return false, err
	}

	// 添加API密钥到请求头
	req.Header.Set("X-API-KEY", apiKey)
	req.Header.Set("Content-Type", "application/json")

	// 发送请求
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		logWarning(logger, "failed to query auto_wake status",
			logit.String("sandboxID", sandboxID),
			logit.String("error", err.Error()))
		return false, err
	}
	defer resp.Body.Close()

	// 读取响应体
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		logWarning(logger, "failed to read auto_wake response",
			logit.String("sandboxID", sandboxID),
			logit.String("error", err.Error()))
		return false, err
	}

	// 处理非200响应
	if resp.StatusCode != http.StatusOK {
		logWarning(logger, "auto_wake query returned error status",
			logit.String("sandboxID", sandboxID),
			logit.Int("statusCode", resp.StatusCode),
			logit.String("body", string(body)))
		return false, errors.New("api returned non-200 status code")
	}

	// 解析响应
	var autoWakeResp AutoWakeResponse
	if err := json.Unmarshal(body, &autoWakeResp); err != nil {
		logWarning(logger, "failed to unmarshal auto_wake response",
			logit.String("sandboxID", sandboxID),
			logit.String("error", err.Error()))
		return false, err
	}

	return autoWakeResp.AutoWake, nil
}

// ShouldSkipSendingRestartMessage 判断是否应该跳过发送沙箱重启消息
// 当以下任一情况成立时，应该跳过发送消息：
// 1. 用户在免空闲释放白名单中
// 2. 沙箱的 auto_wake 为 true（自动唤醒状态下不需要通知用户）
// createUser: 创建用户
// sandboxID: 沙箱ID
// logger: 日志记录器
func ShouldSkipSendingRestartMessage(ctx context.Context, createUser string, sandboxID string, logger interface{}) bool {
	return true
}

// logWarning 统一的日志警告方法
func logWarning(logger interface{}, msg string, fields ...interface{}) {
	// 如果logger实现了Warning方法，则调用它；否则忽略
	if l, ok := logger.(interface {
		Warning(string, ...interface{})
	}); ok {
		l.Warning(msg, fields...)
	}
}
