package common

import (
	"context"
	"encoding/json"
	"math/rand"
	"strconv"
)

const (
	// AgfsSmallTrafficWhiteListRedisKey AgFS 功能小流量用户白名单 Redis Key
	// value 为 JSON 字符串数组，例如: ["user1", "user2"]
	AgfsSmallTrafficWhiteListRedisKey = "sandbox:agfs:small:traffic:whitelist"
	// AgfsSmallTrafficPercentRedisKey AgFS 功能随机流量百分比 Redis Key
	// value 为 0-100 的整数字符串，例如: "30" 表示 30% 的流量通过
	AgfsSmallTrafficPercentRedisKey = "sandbox:agfs:small:traffic:percent"
)

// IsAgfsSmallTrafficEnabled 判断指定用户是否开启 AgFS 小流量
//
// 判断逻辑（满足任一条件即通过）：
//  1. 用户名在 Redis 白名单数组中
//  2. 从 Redis 读取百分比配置（0-100），生成随机数，随机数 < 百分比 则通过
//
// Redis 数据格式：
//   - AgfsSmallTrafficWhiteListRedisKey: JSON 字符串数组，例如 ["user1","user2"]
//   - AgfsSmallTrafficPercentRedisKey:   整数字符串，例如 "30" 表示 30% 通过
func IsAgfsSmallTrafficEnabled(username string) bool {
	if RedisClient == nil {
		return false
	}
	ctx := context.Background()

	// 条件1：用户名命中白名单
	if isUserInAgfsWhitelist(ctx, username) {
		return true
	}

	// 条件2：随机百分比
	return isHitByAgfsPercent(ctx)
}

// isUserInAgfsWhitelist 检查用户是否在 AgFS 小流量白名单中
func isUserInAgfsWhitelist(ctx context.Context, username string) bool {
	val, err := RedisClient.Get(ctx, AgfsSmallTrafficWhiteListRedisKey).Result()
	if err != nil || val == "" {
		return false
	}

	var users []string
	if err := json.Unmarshal([]byte(val), &users); err != nil {
		return false
	}

	for _, u := range users {
		if u == username {
			return true
		}
	}
	return false
}

// isHitByAgfsPercent 按 Redis 中配置的百分比进行随机流量判断
// 返回 true 表示命中（随机数 < 百分比）
func isHitByAgfsPercent(ctx context.Context) bool {
	val, err := RedisClient.Get(ctx, AgfsSmallTrafficPercentRedisKey).Result()
	if err != nil || val == "" {
		return false
	}

	percent, err := strconv.Atoi(val)
	if err != nil || percent <= 0 {
		return false
	}
	if percent >= 100 {
		return true
	}

	// rand.Intn(100) 生成 [0, 99]，< percent 即命中
	return rand.Intn(100) < percent
}
