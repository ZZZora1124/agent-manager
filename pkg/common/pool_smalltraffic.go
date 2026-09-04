package common

import (
	"context"
	"encoding/json"
	"math/rand"
	"strconv"
)

const (
	// PoolSmallTrafficWhiteListRedisKey 沙箱池化小流量用户白名单 Redis Key
	// value 为 JSON 字符串数组，例如: ["user1", "user2"]
	PoolSmallTrafficWhiteListRedisKey = "sandbox:pool:small:traffic:whitelist"
	// PoolSmallTrafficPercentRedisKey 沙箱池化随机流量百分比 Redis Key
	// value 为 0-100 的整数字符串，例如: "30" 表示 30% 的流量通过
	PoolSmallTrafficPercentRedisKey = "sandbox:pool:small:traffic:percent"
)

// IsPoolSmallTrafficEnabled 判断指定用户是否命中沙箱池化小流量
//
// 判断逻辑（满足任一条件即通过）：
//  1. 用户名在 Redis 白名单数组中
//  2. 从 Redis 读取百分比配置（0-100），生成随机数，随机数 < 百分比 则通过
func IsPoolSmallTrafficEnabled(username string) bool {
	if RedisClient == nil {
		return false
	}
	ctx := context.Background()

	val, err := RedisClient.Get(ctx, PoolSmallTrafficWhiteListRedisKey).Result()
	if err == nil && val != "" {
		var users []string
		if json.Unmarshal([]byte(val), &users) == nil {
			for _, u := range users {
				if u == username {
					return true
				}
			}
		}
	}

	pval, err := RedisClient.Get(ctx, PoolSmallTrafficPercentRedisKey).Result()
	if err != nil || pval == "" {
		return false
	}
	percent, err := strconv.Atoi(pval)
	if err != nil || percent <= 0 {
		return false
	}
	if percent >= 100 {
		return true
	}
	return rand.Intn(100) < percent
}
