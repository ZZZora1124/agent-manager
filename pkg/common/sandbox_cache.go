package common

import (
	"context"
	"encoding/json"
	"time"

	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util/constant"
	"icode.baidu.com/baidu/gdp/logit"
	"icode.baidu.com/baidu/gdp/redis"
)

// SandboxOwnerInfo 沙箱创建人信息
type SandboxOwnerInfo struct {
	Username    string `json:"username"`
	SandboxUUID string `json:"sandboxUUID"`
	UgateToken  string `json:"ugateToken"`
	Platform    string `json:"platform"` // 兼容老数据
	AppID       int32  `json:"appId"`    // 新数据直接存储appId
}

// SetSandboxOwnerCache 设置沙箱IP到Owner的缓存
// 用于高频查询场景，避免每次都查数据库
// TTL: 1小时
func SetSandboxOwnerCache(ctx context.Context, redisClient redis.Client, logger logit.Logger, ip, username, sandboxUUID, ugateToken string, appID int32, appPlatform string) error {
	if ip == "" || username == "" || sandboxUUID == "" {
		return nil // 参数为空，跳过缓存
	}

	// 存储完整信息（包含 ugateToken），过期时间1小时
	// 直接存储 appID，不再转换为 platform
	cacheKey := constant.SandboxIPToOwnerPrefix + ip
	ownerInfo := SandboxOwnerInfo{
		Username:    username,
		SandboxUUID: sandboxUUID,
		UgateToken:  ugateToken,
		AppID:       appID, // 直接存储 appID
		Platform:    appPlatform,
	}

	data, err := json.Marshal(ownerInfo)
	if err != nil {
		logger.Error(ctx, "marshal sandbox owner info failed",
			logit.String("ip", ip),
			logit.String("error", err.Error()))
		return err
	}

	err = redisClient.Set(ctx, cacheKey, string(data), time.Hour*1).Err()
	if err != nil {
		logger.Error(ctx, "set sandbox owner cache failed",
			logit.String("ip", ip),
			logit.String("error", err.Error()))
		return err
	}

	return nil
}

// DeleteSandboxOwnerCache 删除沙箱IP到Owner的缓存
// 在沙箱释放时调用，主动清理缓存避免IP复用时返回错误数据
func DeleteSandboxOwnerCache(ctx context.Context, redisClient redis.Client, logger logit.Logger, ip string) error {
	if ip == "" {
		return nil // IP为空，跳过删除
	}

	cacheKey := constant.SandboxIPToOwnerPrefix + ip
	err := redisClient.Del(ctx, cacheKey).Err()
	if err != nil {
		logger.Error(ctx, "delete sandbox owner cache failed",
			logit.String("ip", ip),
			logit.String("error", err.Error()))
		return err
	}

	return nil
}

// FuseOwnerInfo FuseOwner接口返回信息
type FuseOwnerInfo struct {
	ComateAuthToken string `json:"comateAuthToken"`
	Username        string `json:"username"`
}

// GetFuseOwnerFromCache 从缓存获取FuseOwner信息
func GetFuseOwnerFromCache(ctx context.Context, redisClient redis.Client, logger logit.Logger, sandboxUUID string) (*FuseOwnerInfo, bool) {
	if sandboxUUID == "" {
		return nil, false
	}

	cacheKey := constant.FuseOwnerCachePrefix + sandboxUUID
	data, err := redisClient.Get(ctx, cacheKey).Result()
	if err != nil {
		return nil, false
	}

	var info FuseOwnerInfo
	if err := json.Unmarshal([]byte(data), &info); err != nil {
		logger.Error(ctx, "unmarshal fuse owner info failed",
			logit.String("sandboxUUID", sandboxUUID),
			logit.String("error", err.Error()))
		return nil, false
	}

	return &info, true
}

// SetFuseOwnerCache 设置FuseOwner缓存，TTL: 2小时
func SetFuseOwnerCache(ctx context.Context, redisClient redis.Client, logger logit.Logger, sandboxUUID string, info *FuseOwnerInfo) error {
	if sandboxUUID == "" || info == nil {
		return nil
	}

	cacheKey := constant.FuseOwnerCachePrefix + sandboxUUID
	data, err := json.Marshal(info)
	if err != nil {
		logger.Error(ctx, "marshal fuse owner info failed",
			logit.String("sandboxUUID", sandboxUUID),
			logit.String("error", err.Error()))
		return err
	}

	err = redisClient.Set(ctx, cacheKey, string(data), time.Hour*2).Err()
	if err != nil {
		logger.Error(ctx, "set fuse owner cache failed",
			logit.String("sandboxUUID", sandboxUUID),
			logit.String("error", err.Error()))
		return err
	}

	return nil
}

// GetSandboxOwnerFromCache 从缓存获取沙箱Owner信息
// 返回值: username, sandboxUUID, ugateToken, platform, appID, found
// found=true 表示缓存命中，found=false 表示缓存未命中需要查DB
func GetSandboxOwnerFromCache(ctx context.Context, redisClient redis.Client, logger logit.Logger, ip string) (string, string, string, string, int32, bool) {
	if ip == "" {
		return "", "", "", "", 0, false
	}

	cacheKey := constant.SandboxIPToOwnerPrefix + ip
	data, err := redisClient.Get(ctx, cacheKey).Result()
	if err != nil {
		// 缓存未命中或Redis错误，返回 found=false
		return "", "", "", "", 0, false
	}

	var ownerInfo SandboxOwnerInfo
	if err := json.Unmarshal([]byte(data), &ownerInfo); err != nil {
		logger.Error(ctx, "unmarshal sandbox owner info failed",
			logit.String("ip", ip),
			logit.String("error", err.Error()))
		return "", "", "", "", 0, false
	}

	// 兼容老数据：如果没有 appId，说明是老数据，直接返回已有的 platform
	if ownerInfo.AppID == 0 {
		return ownerInfo.Username, ownerInfo.SandboxUUID, ownerInfo.UgateToken, ownerInfo.Platform, 0, true
	}

	// 新数据：根据 appID 从配置获取 platform
	platform := ownerInfo.Platform
	if platform == "" {
		_, platform, _ = IsUserInClusterDnsWhitelistWithPlatform(ownerInfo.AppID, ownerInfo.Username)
	}
	return ownerInfo.Username, ownerInfo.SandboxUUID, ownerInfo.UgateToken, platform, ownerInfo.AppID, true
}
