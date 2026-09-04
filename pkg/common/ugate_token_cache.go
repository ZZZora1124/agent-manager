package common

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util/constant"
	"icode.baidu.com/baidu/gdp/logit"
)

// UgateTokenCacheInfo ugate token 缓存信息
type UgateTokenCacheInfo struct {
	SandboxToken string `json:"sandboxToken"` // 用于 createpod 的完整 JSON 字符串
	Token        string `json:"token"`        // 用于缓存的纯 token 值
}

// SetUgateTokenCache 设置 ugate token 缓存
// 使用全局 RedisClient，调用前需确保已通过 SetRedisClient 初始化
// TTL: 1小时
func SetUgateTokenCache(ctx context.Context, logger logit.Logger, appid int32, username, sandboxUUID, sandboxToken, token string) error {
	if RedisClient == nil {
		return nil // Redis 客户端未初始化，跳过缓存
	}

	if sandboxToken == "" || token == "" {
		return nil // 参数为空，跳过缓存
	}

	var cacheKey string
	// 根据沙箱模式决定缓存 key 的生成方式
	// user 模式（一个用户一个沙箱）: 使用 appid:username
	// task 模式（一个任务一个沙箱）: 使用 sandboxUUID
	if IsUserSandboxMode(appid) {
		if username == "" {
			return nil // 用户沙箱模式需要 username
		}
		cacheKey = constant.UgateTokenCachePrefix + strconv.Itoa(int(appid)) + ":" + username
	} else {
		if sandboxUUID == "" {
			return nil // 任务沙箱模式需要 sandboxUUID
		}
		cacheKey = constant.UgateTokenCachePrefix + sandboxUUID
	}

	cacheInfo := UgateTokenCacheInfo{
		SandboxToken: sandboxToken,
		Token:        token,
	}

	data, err := json.Marshal(cacheInfo)
	if err != nil {
		if logger != nil {
			logger.Error(ctx, "marshal ugate token cache info failed",
				logit.Int32("appid", appid),
				logit.String("username", username),
				logit.String("sandboxUUID", sandboxUUID),
				logit.String("error", err.Error()))
		}
		return err
	}

	err = RedisClient.Set(ctx, cacheKey, string(data), time.Hour*24*7).Err()
	if err != nil {
		if logger != nil {
			logger.Error(ctx, "set ugate token cache failed",
				logit.Int32("appid", appid),
				logit.String("username", username),
				logit.String("sandboxUUID", sandboxUUID),
				logit.String("error", err.Error()))
		}
		return err
	}

	return nil
}

// GetUgateTokenFromCache 从缓存获取 ugate token 信息
// 使用全局 RedisClient
// 返回值: sandboxToken, token, found
// found=true 表示缓存命中，found=false 表示缓存未命中需要调用接口
func GetUgateTokenFromCache(ctx context.Context, logger logit.Logger, appid int32, username, sandboxUUID string) (string, string, bool) {
	if RedisClient == nil {
		return "", "", false // Redis 客户端未初始化
	}

	var cacheKey string
	// 根据沙箱模式决定缓存 key 的生成方式
	// user 模式（一个用户一个沙箱）: 使用 appid:username
	// task 模式（一个任务一个沙箱）: 使用 sandboxUUID
	if IsUserSandboxMode(appid) {
		if username == "" {
			return "", "", false
		}
		cacheKey = constant.UgateTokenCachePrefix + strconv.Itoa(int(appid)) + ":" + username
	} else {
		if sandboxUUID == "" {
			return "", "", false
		}
		cacheKey = constant.UgateTokenCachePrefix + sandboxUUID
	}

	data, err := RedisClient.Get(ctx, cacheKey).Result()
	if err != nil {
		// 缓存未命中或Redis错误，返回 found=false
		return "", "", false
	}

	var cacheInfo UgateTokenCacheInfo
	if err := json.Unmarshal([]byte(data), &cacheInfo); err != nil {
		if logger != nil {
			logger.Error(ctx, "unmarshal ugate token cache info failed",
				logit.Int32("appid", appid),
				logit.String("username", username),
				logit.String("sandboxUUID", sandboxUUID),
				logit.String("error", err.Error()))
		}
		return "", "", false
	}

	return cacheInfo.SandboxToken, cacheInfo.Token, true
}

// DeleteUgateTokenCache 删除 ugate token 缓存
// 使用全局 RedisClient
// 在沙箱释放时调用，主动清理缓存
func DeleteUgateTokenCache(ctx context.Context, logger logit.Logger, sandboxUUID string) error {
	if RedisClient == nil {
		return nil // Redis 客户端未初始化，跳过删除
	}

	if sandboxUUID == "" {
		return nil // sandboxUUID为空，跳过删除
	}

	cacheKey := constant.UgateTokenCachePrefix + sandboxUUID
	err := RedisClient.Del(ctx, cacheKey).Err()
	if err != nil {
		if logger != nil {
			logger.Error(ctx, "delete ugate token cache failed",
				logit.String("sandboxUUID", sandboxUUID),
				logit.String("error", err.Error()))
		}
		return err
	}

	return nil
}
