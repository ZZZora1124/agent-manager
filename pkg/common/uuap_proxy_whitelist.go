package common

import (
	"context"
	"encoding/json"
	"strconv"
)

// ClusterDnsConfig 集群 DNS 配置结构（支持 clusterLocalDns 和 autoLogin）
type ClusterDnsConfig struct {
	Users           []string          `json:"users"`
	LocalDns        string            `json:"localDns"`
	ClusterLocalDns map[string]string `json:"clusterLocalDns,omitempty"` // key: 集群名称, value: DNS 地址
	AutoLogin       bool              `json:"autoLogin"`                 // 是否自动登录
	SandboxMode     string            `json:"sandboxMode"`               // 沙箱模式: "user" (一个用户一个沙箱) 或 "task" (一个任务一个沙箱)
	Platform        string            `json:"platform,omitempty"`        // 平台标识
	SetRootCa       bool              `json:"setRootCa"`                 // 是否注入 Root CA 证书
	Privileged      bool              `json:"privileged"`                // 是否开启特权模式
}

const (
	// ClusterDnsConfigRedisKey 集群 DNS 配置的 Redis Hash Key
	ClusterDnsConfigRedisKey = "ugate:proxy:config"

	// SandboxModeUser 一个用户一个沙箱模式
	SandboxModeUser = "user"
	// SandboxModeTask 一个任务一个沙箱模式
	SandboxModeTask = "sandbox"
)

// GetClusterDnsConfig 通过 HGET 获取指定 appid 的集群 DNS 配置
// 参数:
//   - appid: 应用 ID（int32 类型，例如 15, 27）
//
// 返回:
//   - *ClusterDnsConfig: 配置信息，如果不存在则返回 nil
//   - error: 错误信息
//
// Redis Hash 结构示例:
//
//	HGET cluster:dns:config "15"
//	返回: {"users":["all"],"localDns":"172.16.41.108","image":"","clusterLocalDns":{"agent-sandbox-oc-prod":"172.100.0.98"}}
func GetClusterDnsConfig(appid int32) (*ClusterDnsConfig, error) {
	if RedisClient == nil {
		return nil, nil
	}

	// 使用 HGET 从 Redis Hash 中获取指定 appid 的配置
	appidStr := strconv.Itoa(int(appid))
	val, err := RedisClient.HGet(context.Background(), ClusterDnsConfigRedisKey, appidStr).Result()
	if err != nil {
		// redis.Nil 表示 field 不存在，返回 nil 而不是 error
		if err.Error() == "redis: nil" {
			return nil, nil
		}
		return nil, err
	}

	if val == "" {
		return nil, nil
	}

	// 解析 JSON 数据
	var config ClusterDnsConfig
	if err := json.Unmarshal([]byte(val), &config); err != nil {
		return nil, err
	}

	return &config, nil
}

// SetClusterDnsConfig 通过 HSET 设置指定 appid 的集群 DNS 配置
// 参数:
//   - appid: 应用 ID（int32 类型）
//   - config: 配置信息
//
// 返回:
//   - error: 错误信息
func SetClusterDnsConfig(appid int32, config *ClusterDnsConfig) error {
	if RedisClient == nil {
		return nil
	}

	// 将配置序列化为 JSON
	data, err := json.Marshal(config)
	if err != nil {
		return err
	}

	// 使用 HSET 存储到 Redis Hash
	appidStr := strconv.Itoa(int(appid))
	return RedisClient.HSet(context.Background(), ClusterDnsConfigRedisKey, appidStr, string(data)).Err()
}

// IsUserInClusterDnsWhitelist 判断用户是否在指定 appid 的集群 DNS 配置白名单中
// 参数:
//   - appid: 应用 ID（int32 类型）
//   - username: 用户名
//
// 返回:
//   - bool: 如果用户在白名单中返回 true，否则返回 false
//   - string: localDns 配置值，如果不在白名单或未配置则返回空字符串
//
// 注意: 此方法签名与 IsUserInUUAPProxyWhitelist 保持一致，便于替换
func IsUserInClusterDnsWhitelist(appid int32, username string) (bool, string) {
	config, err := GetClusterDnsConfig(appid)
	if err != nil || config == nil {
		return false, ""
	}

	// 检查用户是否在白名单中
	for _, user := range config.Users {
		if user == "all" || user == username {
			return true, config.LocalDns
		}
	}

	return false, ""
}

// IsUserInClusterDnsWhitelistWithPlatform 判断用户是否在指定 appid 的集群 DNS 配置白名单中，并返回 platform 字段
// 参数:
//   - appid: 应用 ID（int32 类型）
//   - username: 用户名
//
// 返回:
//   - bool: 如果用户在白名单中返回 true，否则返回 false
//   - string: platform 配置值，如果不在白名单或未配置则返回空字符串
//   - bool: 该业务是否开启特权模式
func IsUserInClusterDnsWhitelistWithPlatform(appid int32, username string) (bool, string, bool) {
	config, err := GetClusterDnsConfig(appid)
	if err != nil || config == nil {
		return false, "", false
	}

	// 检查用户是否在白名单中
	for _, user := range config.Users {
		if user == "all" || user == username {
			return true, config.Platform, config.Privileged
		}
	}

	return false, "", false
}

// GetAutoLogin 获取指定 appid 的自动登录配置
// 参数:
//   - appid: 应用 ID（int32 类型）
//
// 返回:
//   - bool: 是否自动登录，如果未配置则返回 false
func IsAutoLogin(appid int32) bool {
	config, err := GetClusterDnsConfig(appid)
	if err != nil || config == nil {
		return false
	}
	return config.AutoLogin
}

// GetSandboxMode 获取指定 appid 的沙箱模式
// 参数:
//   - appid: 应用 ID（int32 类型）
//
// 返回:
//   - string: 沙箱模式 ("user" 或 "task")，如果未配置则返回空字符串
func GetSandboxMode(appid int32) string {
	config, err := GetClusterDnsConfig(appid)
	if err != nil || config == nil {
		return ""
	}
	return config.SandboxMode
}

// IsUserSandboxMode 判断指定 appid 是否为用户沙箱模式（一个用户一个沙箱）
// 参数:
//   - appid: 应用 ID（int32 类型）
//
// 返回:
//   - bool: 如果是用户沙箱模式返回 true，否则返回 false
func IsUserSandboxMode(appid int32) bool {
	return GetSandboxMode(appid) == SandboxModeUser
}

// IsTaskSandboxMode 判断指定 appid 是否为任务沙箱模式（一个任务一个沙箱）
// 参数:
//   - appid: 应用 ID（int32 类型）
//
// 返回:
//   - bool: 如果是任务沙箱模式返回 true，否则返回 false
func IsTaskSandboxMode(appid int32) bool {
	return GetSandboxMode(appid) == SandboxModeTask
}

// ShouldSetRootCa 判断指定 appid 是否需要注入 Root CA 证书
func ShouldSetRootCa(appid int32) bool {
	config, err := GetClusterDnsConfig(appid)
	if err != nil || config == nil {
		return false
	}
	return config.SetRootCa
}

// IsUserInClusterDnsWhitelistWithRootCa 判断用户是否在白名单中，并返回是否需要注入 Root CA 证书
// 参数:
//   - appid: 应用 ID（int32 类型）
//   - username: 用户名
//
// 返回:
//   - bool: 如果用户在白名单中返回 true，否则返回 false
//   - bool: 是否需要注入 Root CA 证书
func IsUserInClusterDnsWhitelistWithRootCa(appid int32, username string) (bool, bool) {
	config, err := GetClusterDnsConfig(appid)
	if err != nil || config == nil {
		return false, false
	}

	for _, user := range config.Users {
		if user == "all" || user == username {
			return true, config.SetRootCa
		}
	}

	return false, false
}

// GetClusterDnsForUser 获取指定用户在指定集群的 DNS 配置
// 参数:
//   - appid: 应用 ID
//   - username: 用户名
//   - clusterName: 集群名称（例如 "agent-sandbox-oc-prod"）
//
// 返回:
//   - bool: 用户是否在白名单中
//   - string: DNS 地址，优先返回 clusterLocalDns，其次返回 localDns
func GetClusterDnsForUser(appid int32, username, clusterName string) (bool, string) {
	config, err := GetClusterDnsConfig(appid)
	if err != nil || config == nil {
		return false, ""
	}

	// 检查用户是否在白名单中
	inWhitelist := false
	for _, user := range config.Users {
		if user == "all" || user == username {
			inWhitelist = true
			break
		}
	}

	if !inWhitelist {
		return false, ""
	}

	// 优先返回集群级别的 DNS 配置
	if config.ClusterLocalDns != nil {
		if dns, exists := config.ClusterLocalDns[clusterName]; exists && dns != "" {
			return true, dns
		}
	}

	// 如果没有集群级别配置，返回全局 localDns
	return true, config.LocalDns
}

// GetAllClusterDnsConfigs 通过 HGETALL 获取所有 appid 的集群 DNS 配置
// 返回:
//   - map[string]*ClusterDnsConfig: appid -> 配置信息的映射
//   - error: 错误信息
func GetAllClusterDnsConfigs() (map[string]*ClusterDnsConfig, error) {
	if RedisClient == nil {
		return nil, nil
	}

	// 使用 HGETALL 获取所有配置
	result, err := RedisClient.HGetAll(context.Background(), ClusterDnsConfigRedisKey).Result()
	if err != nil {
		return nil, err
	}

	configs := make(map[string]*ClusterDnsConfig)
	for appidStr, val := range result {
		var config ClusterDnsConfig
		if err := json.Unmarshal([]byte(val), &config); err != nil {
			// 跳过解析失败的条目
			continue
		}
		configs[appidStr] = &config
	}

	return configs, nil
}
