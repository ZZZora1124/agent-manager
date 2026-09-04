package common

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/eacauth"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util/constant"

	"icode.baidu.com/baidu/gdp/logit"
	"icode.baidu.com/baidu/gdp/redis"
)

// SmallTrafficUserWhiteList 小流量用户白名单
type SmallTrafficUserWhiteList struct {
	Users      []string `json:"users"`
	SuperUsers []string `json:"superUsers"`
}

// TokenWhiteList Token 白名单（基于 AppID）
type TokenWhiteList struct {
	AppIDs []int32 `json:"appIds"`
}

// NetWhiteList 网络白名单
type NetWhiteList struct {
	AppIDs []int32  `json:"appIds"`
	Users  []string `json:"users"`
}

// RegistryWhiteList 镜像仓库白名单
type RegistryWhiteList struct {
	AppIDs []int32 `json:"appIds"`
}

// UnlimitedWhiteList 无限制白名单
type UnlimitedWhiteList struct {
	AppIDs []int32 `json:"appIds"`
}

type OpenClawImageV2Small struct {
	Users []string `json:"users"`
}

// OpenClawImageSmallGroup OpenClaw 小流量镜像组
type OpenClawImageSmallGroup struct {
	Users []string `json:"users"`
	Image string   `json:"image"`
}

type UUapProxySmall struct {
	Users []string `json:"users"`
}

// OpenClawImageSmallList OpenClaw 小流量镜像列表（支持多个镜像组）
type OpenClawImageSmallList []OpenClawImageSmallGroup

// noIdleReleaseCache 非休眠白名单缓存
type noIdleReleaseCache struct {
	whitelist  SmallTrafficUserWhiteList // 白名单数据
	expireTime time.Time                 // 缓存过期时间
	mu         sync.RWMutex              // 读写锁
}

// 全局缓存实例
var noIdleCache = &noIdleReleaseCache{}

// 缓存时间：30分钟
const noIdleReleaseCacheDuration = 30 * time.Minute

// RedisClient Redis 客户端实例
// 需要在程序初始化时通过 SetRedisClient 方法设置
var RedisClient redis.Client

// SetRedisClient 设置 Redis 客户端
func SetRedisClient(client redis.Client) {
	RedisClient = client
}

// IsSmallTrafficUser 判断用户是否为小流量用户（在通用小流量白名单中）
// 返回两个值：第一个表示是否在普通用户列表中，第二个表示是否在超级用户列表中
func IsSmallTrafficUser(username string) (bool, bool) {
	if RedisClient == nil {
		return false, false
	}

	val, err := RedisClient.Get(context.Background(), constant.SmallTrafficUserWhiteListRedisKey).Result()
	if err != nil || val == "" {
		return false, false
	}

	var wl SmallTrafficUserWhiteList
	if err := json.Unmarshal([]byte(val), &wl); err != nil {
		return false, false
	}

	isNormalUser := false
	isSuperUser := false

	// 检查是否在普通用户列表中
	for _, user := range wl.Users {
		if user == "all" || username == user {
			isNormalUser = true
			break
		}
	}

	// 检查是否在超级用户列表中
	for _, user := range wl.SuperUsers {
		if user == "all" || username == user {
			isSuperUser = true
			break
		}
	}

	return isNormalUser, isSuperUser
}

// IsCfsMigrateUser 判断用户是否在 CFS 迁移白名单中
func IsCfsMigrateUser(username string) bool {
	return IsUserInWhiteList(username, constant.CfsMigrateWhiteListRedisKey)
}

// IsCfsNewMigrateUser 判断用户是否在新 CFS 迁移白名单中
func IsCfsNewMigrateUser(username string) bool {
	return IsUserInWhiteList(username, constant.CfsNewMigrateWhiteListRedisKey)
}

// IsPersonalTokenUser 判断用户是否在 openclaw gw token 白名单中
// 优先检查全量开关，如果全量开关开启则所有用户都使用新接口；否则只检查白名单
func IsPersonalTokenUser(username string) bool {
	if RedisClient == nil {
		return false
	}

	// 先检查全量开关
	allUsersVal, err := RedisClient.Get(context.Background(), constant.OpenClawGWTokenAllUsersRedisKey).Result()
	if err == nil && allUsersVal == "true" {
		return true
	}

	// 全量开关未开启，检查用户是否在白名单中
	return IsUserInWhiteList(username, constant.OpenClawGWTokenWhiteListRedisKey)
}

// ClawGuardDispatch 查询沙箱的 auto_wake 状态
func ClawGuardDispatch(sandboxID string) bool {
	url := fmt.Sprintf("http://sandbox-clawguard-dispatch.baidu-int.com/dispatch/api/auto-wake/%s", sandboxID)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return false
	}

	req.Header.Set("X-API-KEY", "75i1o27472uf3i1o2745ie63eqs9")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		var result struct {
			SandboxID string `json:"sandbox_id"`
			AutoWake  bool   `json:"auto_wake"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return false
		}
		return result.AutoWake
	case http.StatusForbidden:
		return false
	case http.StatusBadRequest:
		return false
	case http.StatusNotFound:
		return false
	case http.StatusInternalServerError:
		return false
	default:
		return false
	}
}

// ShouldSkipMessage 判断是否跳过发送消息（任一条件为 true 则跳过）
func ShouldSkipMessage(username, sandboxID string, appID int32) bool {
	return true
}

// IsNoIdleReleaseUser 判断用户是否在非休眠白名单中（该名单中的用户创建的 sandbox 不会被空闲释放）
// 使用本地缓存减少 Redis 查询压力，缓存时间为 30 分钟
func IsNoIdleReleaseUser(username string) bool {
	// 先尝试读锁检查缓存
	noIdleCache.mu.RLock()
	if time.Now().Before(noIdleCache.expireTime) {
		// 缓存有效，直接使用缓存数据
		users := noIdleCache.whitelist.Users
		noIdleCache.mu.RUnlock()

		for _, user := range users {
			if user == "all" || username == user {
				return true
			}
		}
		return false
	}
	noIdleCache.mu.RUnlock()

	// 缓存失效，需要刷新
	noIdleCache.mu.Lock()
	defer noIdleCache.mu.Unlock()

	// Double check: 可能在等待锁的过程中，其他协程已经刷新了缓存
	if time.Now().Before(noIdleCache.expireTime) {
		users := noIdleCache.whitelist.Users
		for _, user := range users {
			if user == "all" || username == user {
				return true
			}
		}
		return false
	}

	// 从 Redis 加载白名单
	if RedisClient == nil {
		noIdleCache.expireTime = time.Now().Add(noIdleReleaseCacheDuration)
		return false
	}

	val, err := RedisClient.Get(context.Background(), constant.NoIdleReleaseWhiteListRedisKey).Result()
	if err != nil || val == "" {
		noIdleCache.expireTime = time.Now().Add(noIdleReleaseCacheDuration)
		return false
	}

	var wl SmallTrafficUserWhiteList
	if err := json.Unmarshal([]byte(val), &wl); err != nil {
		noIdleCache.expireTime = time.Now().Add(noIdleReleaseCacheDuration)
		return false
	}

	// 更新缓存
	noIdleCache.whitelist = wl
	noIdleCache.expireTime = time.Now().Add(noIdleReleaseCacheDuration)

	// 在新加载的数据中查找
	for _, user := range wl.Users {
		if user == "all" || username == user {
			return true
		}
	}
	return false
}

// IsUserInWhiteList 通用白名单判断：从 Redis 读取指定 key 的用户列表，判断用户是否在其中。
// Redis 值格式：{"users":["user1","user2"]}
func IsUserInWhiteList(username, redisKey string) bool {
	if RedisClient == nil {
		return false
	}

	val, err := RedisClient.Get(context.Background(), redisKey).Result()
	if err != nil || val == "" {
		return false
	}

	var wl SmallTrafficUserWhiteList
	if err := json.Unmarshal([]byte(val), &wl); err != nil {
		return false
	}
	for _, user := range wl.Users {
		if user == "all" || username == user {
			return true
		}
	}
	return false
}

// GetDefaultOpenClawSkills 从 Redis 获取默认的 OpenClaw Skills 列表，如果获取失败则返回兜底值
func GetDefaultOpenClawSkills() string {
	if RedisClient == nil {
		return constant.DefaultOpenClawSkills
	}

	skillsStr, err := RedisClient.Get(context.Background(), constant.DefaultOpenClawSkillsRedisKey).Result()
	if err != nil || skillsStr == "" {
		// Redis 获取失败或为空，返回兜底值
		return constant.DefaultOpenClawSkills
	}
	return skillsStr
}

// AppInRegistryWhiteList 检查 appID 是否在镜像仓库白名单中
func AppInRegistryWhiteList(appID int32) bool {
	if RedisClient == nil {
		return false
	}

	registryWhiteListStr, err := RedisClient.Get(context.Background(), constant.RegistryWhiteListRedisKey).Result()
	if err != nil || registryWhiteListStr == "" {
		return false
	}

	var registryWhiteList RegistryWhiteList
	if err := json.Unmarshal([]byte(registryWhiteListStr), &registryWhiteList); err != nil {
		return false
	}

	if registryWhiteList.AppIDs == nil {
		return false
	}
	for _, wAppID := range registryWhiteList.AppIDs {
		if appID == wAppID {
			return true
		}
	}
	return false
}

// AppInUnlimitedWhiteList 检查 appID 是否在无限制白名单中
func AppInUnlimitedWhiteList(appID int32) bool {
	if RedisClient == nil {
		return false
	}

	unlimitedWhiteListStr, err := RedisClient.Get(context.Background(), constant.UnlimitedWhiteListRedisKey).Result()
	if err != nil || unlimitedWhiteListStr == "" {
		return false
	}

	var unlimitedWhiteList UnlimitedWhiteList
	if err := json.Unmarshal([]byte(unlimitedWhiteListStr), &unlimitedWhiteList); err != nil {
		return false
	}
	if unlimitedWhiteList.AppIDs == nil {
		return false
	}
	for _, wAppID := range unlimitedWhiteList.AppIDs {
		if appID == wAppID {
			return true
		}
	}
	return false
}

// AppInNetPolicyWhiteList 检查 appID 是否在网络策略白名单中
func AppInNetPolicyWhiteList(appID int32) bool {
	if RedisClient == nil {
		return false
	}
	val, err := RedisClient.Get(context.Background(), constant.NetWhiteListRedisKey).Result()
	if err != nil || val == "" {
		return false
	}
	var appIDs []int32
	if err := json.Unmarshal([]byte(val), &appIDs); err != nil {
		return false
	}
	for _, id := range appIDs {
		if id == appID {
			return true
		}
	}
	return false
}

// IsInWhiteList 检查 appID 或用户是否在网络白名单中
func (n NetWhiteList) IsInWhiteList(appID int32, user string) bool {
	for _, app := range n.AppIDs {
		if app == appID {
			return true
		}
	}
	for _, u := range n.Users {
		if u == user {
			return true
		}
	}
	return false
}

// AppInTokenWhiteList 检查 appID 是否在 token 白名单中
func AppInTokenWhiteList(appID int32) bool {
	if RedisClient == nil {
		return false
	}

	tokenWhiteListStr, err := RedisClient.Get(context.Background(), constant.TokenWhiteListRedisKey).Result()
	if err != nil || tokenWhiteListStr == "" {
		return false
	}

	var tokenWhiteList TokenWhiteList
	if err := json.Unmarshal([]byte(tokenWhiteListStr), &tokenWhiteList); err != nil {
		return false
	}

	if tokenWhiteList.AppIDs == nil {
		return false
	}
	for _, wAppID := range tokenWhiteList.AppIDs {
		if appID == wAppID {
			return true
		}
	}
	return false
}

// GetOpenClawImageSmall 获取用户的 OpenClaw 小流量镜像
// 返回两个值：第一个表示是否使用小镜像，第二个表示使用的镜像名称
// 适配新的Redis数据格式: {"user1": "image1", "user2": "image2"}
func GetOpenClawImageSmall(username string) (bool, string) {
	if RedisClient == nil {
		return false, ""
	}

	openClawImageSmallStr, err := RedisClient.Get(context.Background(), constant.OpenClawImageSmallRedisKey).Result()
	if err != nil || openClawImageSmallStr == "" {
		return false, ""
	}

	var userToImageMap map[string]string
	if err := json.Unmarshal([]byte(openClawImageSmallStr), &userToImageMap); err != nil {
		return false, ""
	}

	// 如果用户存在于map中，返回对应的镜像
	if imageURL, exists := userToImageMap[username]; exists {
		return true, imageURL
	}

	return false, ""
}

// AddOpenClawUserImages 为用户添加或更新OpenClaw镜像映射
// mode: "merge" 表示合并（默认），"replace" 表示完全替换
func AddOpenClawUserImages(ctx context.Context, redisClient redis.Client, logger logit.Logger, users []string, image, mode string) error {
	if redisClient == nil {
		return errors.New("redis client is nil")
	}

	// 获取现有数据
	openClawImageSmallStr, err := redisClient.Get(ctx, constant.OpenClawImageSmallRedisKey).Result()
	if err != nil {
		// 如果只是key不存在，继续创建新map
		if err.Error() != "redis: nil" {
			logger.Error(ctx, "get openclaw image small from redis failed",
				logit.String("error", err.Error()))
			return err
		}
	}

	// 解析现有数据或创建新map
	userToImageMap := make(map[string]string)
	if openClawImageSmallStr != "" {
		if err := json.Unmarshal([]byte(openClawImageSmallStr), &userToImageMap); err != nil {
			logger.Error(ctx, "unmarshal openclaw image small failed",
				logit.String("error", err.Error()))
			return err
		}
	}

	// 根据模式处理
	if mode == "replace" {
		// replace 模式：完全替换为新的映射
		userToImageMap = make(map[string]string)
		for _, user := range users {
			userToImageMap[user] = image
		}
	} else {
		// merge 模式（默认）：合并或更新
		for _, user := range users {
			userToImageMap[user] = image
		}
	}

	// 序列化并写回Redis
	data, err := json.Marshal(userToImageMap)
	if err != nil {
		logger.Error(ctx, "marshal openclaw image small failed",
			logit.String("error", err.Error()))
		return err
	}

	err = redisClient.Set(ctx, constant.OpenClawImageSmallRedisKey, string(data), 0).Err()
	if err != nil {
		logger.Error(ctx, "set openclaw image small to redis failed",
			logit.String("error", err.Error()))
		return err
	}

	return nil
}

// DeleteOpenClawUserImages 删除用户的OpenClaw镜像映射
func DeleteOpenClawUserImages(ctx context.Context, redisClient redis.Client, logger logit.Logger, users []string) error {
	if redisClient == nil {
		return errors.New("redis client is nil")
	}

	// 获取现有数据
	openClawImageSmallStr, err := redisClient.Get(ctx, constant.OpenClawImageSmallRedisKey).Result()
	if err != nil {
		// 如果key不存在，直接返回成功
		if err.Error() == "redis: nil" {
			return nil
		}
		logger.Error(ctx, "get openclaw image small from redis failed",
			logit.String("error", err.Error()))
		return err
	}

	// 如果没有数据，直接返回成功
	if openClawImageSmallStr == "" {
		return nil
	}

	// 解析现有数据
	userToImageMap := make(map[string]string)
	if err := json.Unmarshal([]byte(openClawImageSmallStr), &userToImageMap); err != nil {
		logger.Error(ctx, "unmarshal openclaw image small failed",
			logit.String("error", err.Error()))
		return err
	}

	// 删除用户映射
	for _, user := range users {
		delete(userToImageMap, user)
	}

	// 序列化并写回Redis（即使map为空也保持空对象）
	data, err := json.Marshal(userToImageMap)
	if err != nil {
		logger.Error(ctx, "marshal openclaw image small failed",
			logit.String("error", err.Error()))
		return err
	}

	err = redisClient.Set(ctx, constant.OpenClawImageSmallRedisKey, string(data), 0).Err()
	if err != nil {
		logger.Error(ctx, "set openclaw image small to redis failed",
			logit.String("error", err.Error()))
		return err
	}

	return nil
}

// QueryOpenClawUserImages 查询用户的OpenClaw镜像映射
func QueryOpenClawUserImages(ctx context.Context, redisClient redis.Client, logger logit.Logger, users []string) (map[string]string, error) {
	if redisClient == nil {
		return nil, errors.New("redis client is nil")
	}

	// 获取现有数据
	openClawImageSmallStr, err := redisClient.Get(ctx, constant.OpenClawImageSmallRedisKey).Result()
	if err != nil {
		// 如果key不存在，返回空map
		if err.Error() == "redis: nil" {
			return make(map[string]string), nil
		}
		logger.Error(ctx, "get openclaw image small from redis failed",
			logit.String("error", err.Error()))
		return nil, err
	}

	// 如果没有数据，返回空map
	if openClawImageSmallStr == "" {
		return make(map[string]string), nil
	}

	// 解析现有数据
	userToImageMap := make(map[string]string)
	if err := json.Unmarshal([]byte(openClawImageSmallStr), &userToImageMap); err != nil {
		logger.Error(ctx, "unmarshal openclaw image small failed",
			logit.String("error", err.Error()))
		return nil, err
	}

	// 只返回请求的用户映射
	result := make(map[string]string)
	for _, user := range users {
		if imageURL, exists := userToImageMap[user]; exists {
			result[user] = imageURL
		}
	}

	return result, nil
}

// QueryAllOpenClawUserImages 查询所有用户的OpenClaw镜像映射
func QueryAllOpenClawUserImages(ctx context.Context, redisClient redis.Client, logger logit.Logger) (map[string]string, error) {
	if redisClient == nil {
		return nil, errors.New("redis client is nil")
	}

	// 获取现有数据
	openClawImageSmallStr, err := redisClient.Get(ctx, constant.OpenClawImageSmallRedisKey).Result()
	if err != nil {
		// 如果key不存在，返回空map
		if err.Error() == "redis: nil" {
			return make(map[string]string), nil
		}
		logger.Error(ctx, "get openclaw image small from redis failed",
			logit.String("error", err.Error()))
		return nil, err
	}

	// 如果没有数据，返回空map
	if openClawImageSmallStr == "" {
		return make(map[string]string), nil
	}

	// 解析现有数据
	userToImageMap := make(map[string]string)
	if err := json.Unmarshal([]byte(openClawImageSmallStr), &userToImageMap); err != nil {
		logger.Error(ctx, "unmarshal openclaw image small failed",
			logit.String("error", err.Error()))
		return nil, err
	}

	return userToImageMap, nil
}

// ImageSmallConfig 按模版ID的小流量镜像配置（旧格式，保留兼容）
type ImageSmallConfig struct {
	Users       []string `json:"users"`
	Departments []string `json:"departments"`
	Image       string   `json:"image"`
}

// ImageSmallData 按模版ID的小流量镜像数据（分类Map格式）
type ImageSmallData struct {
	Users       map[string]string `json:"users"`
	Departments map[string]string `json:"departments"`
	Sandboxes   map[string]string `json:"sandboxes"`
}

// GetOpenClawImageSmallByTplID 根据模版ID获取小流量镜像
// 优先级：沙箱ID > 用户名 > 部门前缀（优先匹配更长的前缀）
// sandboxID 为空时仅匹配用户名和部门
func GetOpenClawImageSmallByTplID(appID int32, template string, username string, sandboxID string) (bool, string) {
	if RedisClient == nil {
		return false, ""
	}

	key := constant.ImageSmallByTplIDRedisKeyPrefix + strconv.Itoa(int(appID)) + ":" + template
	val, err := RedisClient.Get(context.Background(), key).Result()
	if err != nil || val == "" {
		return false, ""
	}

	var data ImageSmallData
	if err := json.Unmarshal([]byte(val), &data); err != nil {
		return false, ""
	}

	// 优先匹配沙箱ID（仅恢复沙箱时使用）
	if sandboxID != "" && data.Sandboxes != nil {
		if imageURL, exists := data.Sandboxes[sandboxID]; exists {
			return true, imageURL
		}
	}

	// 匹配用户名
	if data.Users != nil {
		if imageURL, exists := data.Users[username]; exists {
			return true, imageURL
		}
	}

	// 部门前缀匹配，优先匹配更长的前缀
	if data.Departments != nil && username != "" {
		userInfo, err := eacauth.GetEacAuthApi().GetUserByUsername(username, "username,orgChainNameEn")
		if err != nil {
			return false, ""
		}
		userDept := userInfo.OrgChainNameEn

		matchedPrefix := ""
		matchedImage := ""
		for dept, imageURL := range data.Departments {
			if strings.HasPrefix(userDept, dept) && len(dept) > len(matchedPrefix) {
				matchedPrefix = dept
				matchedImage = imageURL
			}
		}
		if matchedPrefix != "" {
			return true, matchedImage
		}
	}

	return false, ""
}

// SetImageSmallByTplID 设置按模版ID的小流量镜像配置到Redis
// mode: "merge" 表示合并（默认），"replace" 表示完全替换
// userImages/departmentImages/sandboxImages: 每个key指定各自的image，优先级高于 users+image 等旧字段
func SetImageSmallByTplID(ctx context.Context, redisClient redis.Client, logger logit.Logger, appID int32, template string, users, departments, sandboxes []string, image string,
	userImages, departmentImages, sandboxImages map[string]string, mode string) error {
	if redisClient == nil {
		return errors.New("redis client is nil")
	}

	key := constant.ImageSmallByTplIDRedisKeyPrefix + strconv.Itoa(int(appID)) + ":" + template

	var data ImageSmallData

	if mode == "replace" {
		// replace 模式：清空，只用当前设置的值
		data = ImageSmallData{
			Users:       make(map[string]string),
			Departments: make(map[string]string),
			Sandboxes:   make(map[string]string),
		}
	} else {
		// merge 模式：读取已有数据并合并
		val, err := redisClient.Get(ctx, key).Result()
		if err != nil && err.Error() != "redis: nil" {
			logger.Error(ctx, "get image small config from redis failed",
				logit.Int32("appID", appID),
				logit.String("template", template),
				logit.String("error", err.Error()))
			return err
		}
		if val != "" {
			if err := json.Unmarshal([]byte(val), &data); err != nil {
				logger.Error(ctx, "unmarshal image small data failed, will overwrite with new data",
					logit.Int32("appID", appID),
					logit.String("template", template),
					logit.String("error", err.Error()))
				data = ImageSmallData{}
			}
		}
		if data.Users == nil {
			data.Users = make(map[string]string)
		}
		if data.Departments == nil {
			data.Departments = make(map[string]string)
		}
		if data.Sandboxes == nil {
			data.Sandboxes = make(map[string]string)
		}
	}

	// 写入 userImages（优先级高于 users+image）
	for user, img := range userImages {
		data.Users[user] = img
	}
	// 写入 users（旧字段，image 统一应用到所有用户）
	for _, user := range users {
		data.Users[user] = image
	}
	// 写入 departmentImages（优先级高于 departments+image）
	for dept, img := range departmentImages {
		data.Departments[dept] = img
	}
	// 写入 departments（旧字段）
	for _, dept := range departments {
		data.Departments[dept] = image
	}
	// 写入 sandboxImages（优先级高于 sandboxes+image）
	for sbx, img := range sandboxImages {
		data.Sandboxes[sbx] = img
	}
	// 写入 sandboxes（旧字段）
	for _, sbx := range sandboxes {
		data.Sandboxes[sbx] = image
	}

	serialized, err := json.Marshal(data)
	if err != nil {
		logger.Error(ctx, "marshal image small data failed",
			logit.Int32("appID", appID),
			logit.String("template", template),
			logit.String("error", err.Error()))
		return err
	}

	err = redisClient.Set(ctx, key, string(serialized), 0).Err()
	if err != nil {
		logger.Error(ctx, "set image small data to redis failed",
			logit.Int32("appID", appID),
			logit.String("template", template),
			logit.String("error", err.Error()))
		return err
	}

	return nil
}

// GetImageSmallByTplID 从Redis获取按模版ID的小流量镜像配置
func GetImageSmallByTplID(ctx context.Context, redisClient redis.Client, logger logit.Logger, appID int32, template string) (*ImageSmallData, error) {
	if redisClient == nil {
		return nil, errors.New("redis client is nil")
	}

	key := constant.ImageSmallByTplIDRedisKeyPrefix + strconv.Itoa(int(appID)) + ":" + template
	val, err := redisClient.Get(ctx, key).Result()
	if err != nil {
		if err.Error() == "redis: nil" {
			return &ImageSmallData{
				Users:       make(map[string]string),
				Departments: make(map[string]string),
				Sandboxes:   make(map[string]string),
			}, nil
		}
		logger.Error(ctx, "get image small data from redis failed",
			logit.Int32("appID", appID),
			logit.String("template", template),
			logit.String("error", err.Error()))
		return nil, err
	}

	if val == "" {
		return &ImageSmallData{
			Users:       make(map[string]string),
			Departments: make(map[string]string),
			Sandboxes:   make(map[string]string),
		}, nil
	}

	var data ImageSmallData
	if err := json.Unmarshal([]byte(val), &data); err != nil {
		logger.Error(ctx, "unmarshal image small data failed",
			logit.Int32("appID", appID),
			logit.String("template", template),
			logit.String("error", err.Error()))
		return nil, err
	}

	if data.Users == nil {
		data.Users = make(map[string]string)
	}
	if data.Departments == nil {
		data.Departments = make(map[string]string)
	}
	if data.Sandboxes == nil {
		data.Sandboxes = make(map[string]string)
	}

	return &data, nil
}
