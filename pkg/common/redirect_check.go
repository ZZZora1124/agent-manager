package common

import (
	"context"
	"encoding/json"
	"strconv"

	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util/constant"
	"icode.baidu.com/baidu/gdp/logit"
	"icode.baidu.com/baidu/gdp/redis"
)

// RedirectCheckConfig Redis中存储的重定向和小流量配置
// 格式: {"123": ["user1", "user2"], "456": ["user3"], "all": ["all"]}
// key 为 appId (字符串格式)，特殊值 "all" 表示全流量
// value 为 username 列表，特殊值 "all" 表示该 appId 下所有用户都是小流量
type RedirectCheckConfig map[string][]string

// CheckRedirectAndSmallFlow 根据 appID 和 username 检查是否需要重定向以及是否为小流量
// 从 Redis 中查询配置，一次性返回 redirect 和 smallFlow 两个判断结果
// redirect: 判断 appID 是否在配置中（只要 key 存在就返回 true）
// smallFlow: 判断该 appID 下的 username 是否在列表中
// 特殊规则：如果配置中存在 "all" key，则所有 appID 都 redirect=true 且 smallFlow=true
func CheckRedirectAndSmallFlow(ctx context.Context, client redis.Client, logger logit.Logger, appID int32, username string) (redirect bool, smallFlow bool) {
	if client == nil {
		if logger != nil {
			logger.Error(ctx, "redis client is nil when checking redirect and small flow")
		}
		return false, true
	}

	val, err := client.Get(ctx, constant.RedirectCheckRedisKey).Result()
	if err != nil {
		if logger != nil {
			logger.Error(ctx, "failed to get redirect config from redis",
				logit.String("error", err.Error()))
		}
		return false, true
	}

	if val == "" {
		return false, true
	}

	var redirectConfig RedirectCheckConfig
	if err := json.Unmarshal([]byte(val), &redirectConfig); err != nil {
		if logger != nil {
			logger.Error(ctx, "failed to unmarshal redirect config",
				logit.String("error", err.Error()),
				logit.String("value", val))
		}
		return false, true
	}

	// 检查是否为全流量：配置中包含 "all" key
	// 如果存在 "all"，则所有 appID 都需要 redirect 且 smallFlow 都为 true
	if _, exists := redirectConfig["all"]; exists {
		return true, true
	}

	// 检查指定 appID 是否在配置中
	appIDStr := strconv.FormatInt(int64(appID), 10)
	if users, exists := redirectConfig[appIDStr]; exists {
		redirect = true
		// 检查 username 是否在该 appID 的用户列表中
		// 如果用户列表中包含 "all"，表示该 appID 下所有用户都是小流量
		for _, user := range users {
			if user == "all" || user == username {
				smallFlow = true
				break
			}
		}
	}

	return redirect, true
}
