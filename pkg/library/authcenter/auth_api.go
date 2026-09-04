package authcenter

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/common"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/common/config"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/resource"
	util "icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
	"icode.baidu.com/baidu/gdp/logit"
)

type Api struct {
	Config *config.Config
}

type BaseResponse struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Data    *UserInfoResponse `json:"data"`
}
type UserInfoResponse struct {
	AccessToken string `json:"accessToken"`
	ExpireAt    string `json:"expireAt"`
}

type TokenResult struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Desc string `json:"desc"`
	Data *Token `json:"data"`
}

type Token struct {
	Token        string          `json:"token"`
	SandboxToken json.RawMessage `json:"sandboxToken"`
	CreateAt     int64           `json:"createAt"`
	ExpireAt     int64           `json:"expireAt"`
}

// ValidateTokenResponse token校验响应结构
type ValidateTokenResponse struct {
	Code int                `json:"code"`
	Msg  string             `json:"msg"`
	Data *ValidateTokenData `json:"data"`
}

// ValidateTokenData token校验返回的数据
type ValidateTokenData struct {
	Username     string `json:"username"`
	Platform     string `json:"platform"`
	PlatformName string `json:"platformName"`
	ClientID     string `json:"client_id"`
}

// SandboxTokenStatusResponse 沙箱Token状态查询响应结构
type SandboxTokenStatusResponse struct {
	Code      int    `json:"code"`
	Msg       string `json:"msg"`
	TracingID string `json:"tracingId"`
	Result    bool   `json:"result"`
}

var eacAuthApi *Api = nil

func GetAuthCenterApi() *Api {
	if eacAuthApi == nil {
		eacAuthApi = &Api{
			Config: config.GetAuthCenterConfig(),
		}
	}
	return eacAuthApi
}

// GetDevopsToken 根据apiToken获取devops token
func (e *Api) GetDevopsToken(apiToken string) (*UserInfoResponse, error) {
	// 生成时间戳和随机数
	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	sRandom := fmt.Sprintf("%d", time.Now().UnixNano()%1000000)

	clientID := e.Config.ClientID
	secretKey := e.Config.ClientSecret

	// 生成签名
	signStr := fmt.Sprintf("client_id=%s&random==%s&timestamp=%s&secretKey=%s&identity=%s",
		clientID, sRandom, timestamp, secretKey, apiToken)

	// SHA256加密
	hash := sha256.Sum256([]byte(signStr))
	sign := hex.EncodeToString(hash[:])

	// 构建请求参数
	bodyData := make(map[string]any)
	bodyData["identity"] = apiToken
	bodyData["client_id"] = clientID
	bodyData["timestamp"] = timestamp
	bodyData["random"] = sRandom
	bodyData["sign"] = sign
	jsonStr, mError := json.Marshal(bodyData)
	if mError != nil {
		return nil, mError
	}
	res := &BaseResponse{}

	err := util.Backoff(util.DefaultRetry, func() (bool, error) {
		// 构建请求URL
		requestURL := fmt.Sprintf("%s/api/rest/v1/auth/user_auth_token", e.Config.Endpoint)

		// 创建请求
		req, err := http.NewRequest("POST", requestURL, bytes.NewReader(jsonStr))
		if err != nil {
			return false, nil // 会重试
		}

		req.Header.Set("Content-Type", "application/json")

		// 发送请求
		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			return false, nil // 会重试
		}
		defer resp.Body.Close()

		// 读取响应
		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return false, nil // 会重试
		}

		if err := json.Unmarshal(respBody, &res); err != nil {
			return false, nil // 会重试
		}

		// 检查响应是否包含用户信息
		if res.Code == "ok" {
			return true, nil
		}

		return false, fmt.Errorf("user not found or invalid response")
	})

	if err != nil || res == nil || res.Data == nil {
		return nil, fmt.Errorf("failed to get user info: %v", err)
	}

	return res.Data, nil
}

// GetPersonalTokenByUsername 通过用户名获取个人访问令牌
func (e *Api) GetPersonalTokenByUsername(username string) string {
	if username == "" {
		return ""
	}

	// 解码用户名（加密）
	identity := e.aesEncrypt(username, e.Config.ComateClientSecret)

	// 生成时间戳和随机数
	timestamp := time.Now().UnixMilli()
	sRandom := uuid.New().String()

	clientID := e.Config.ComateClientID
	secretKey := e.Config.ComateClientSecret

	// 生成签名
	signStr := fmt.Sprintf("client_id=%s&random=%s&timestamp=%d&secretKey=%s&identity=%s",
		clientID, sRandom, timestamp, secretKey, identity)

	// SHA256加密
	hash := sha256.Sum256([]byte(signStr))
	sign := hex.EncodeToString(hash[:])

	// 构建请求参数
	bodyData := make(map[string]any)
	bodyData["identity"] = identity
	bodyData["client_id"] = clientID
	bodyData["timestamp"] = timestamp
	bodyData["random"] = sRandom
	bodyData["sign"] = sign
	jsonStr, _ := json.Marshal(bodyData)

	// 构建请求URL
	requestURL := fmt.Sprintf("%s/api/rest/v1/auth/user_auth_token", e.Config.Endpoint)

	// 创建请求
	req, _ := http.NewRequest("POST", requestURL, bytes.NewReader(jsonStr))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-request-id", uuid.New().String())

	// 发送请求
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	// 读取响应
	respBody, _ := io.ReadAll(resp.Body)

	// 解析响应
	var result TokenResult
	if err := json.Unmarshal(respBody, &result); err != nil {
		return ""
	}

	// 检查响应是否包含 token
	if result.Code != 200 || result.Data == nil || result.Data.Token == "" {
		return ""
	}

	return result.Data.Token
}

// ValidateToken 校验 token 是否有效
func (e *Api) ValidateToken(accessToken string) (*ValidateTokenData, error) {
	if accessToken == "" {
		return nil, fmt.Errorf("accessToken is empty")
	}

	// 构建请求URL
	requestURL := fmt.Sprintf("%s/api/rest/v1/auth/validate?accessToken=%s", e.Config.Endpoint, accessToken)

	// 创建请求
	req, err := http.NewRequest("GET", requestURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// 设置请求头
	req.Header.Set("Content-Type", "application/json")

	// 发送请求
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// 读取响应
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// 解析响应
	var result ValidateTokenResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	// 检查响应状态码
	if result.Code != 200 {
		return nil, fmt.Errorf("validate token failed: code=%d, msg=%s", result.Code, result.Msg)
	}

	// 检查返回数据
	if result.Data == nil {
		return nil, fmt.Errorf("validate token failed: data is nil")
	}

	return result.Data, nil
}

// ValidateMultiToken 校验多种类型的 token (支持 Bearer-, cb_proxy_ 和 UUAP token)
// 使用与 GetDevopsToken 相同的签名方式进行鉴权
func (e *Api) ValidateMultiToken(accessToken string) (*ValidateTokenData, error) {
	if accessToken == "" {
		return nil, fmt.Errorf("accessToken is empty")
	}

	// 生成时间戳和随机数
	timestamp := time.Now().UnixMilli()
	sRandom := uuid.New().String()[:12]

	clientID := e.Config.ClientID
	secretKey := e.Config.ClientSecret

	// 生成签名 (与 GetDevopsToken 方法一致)
	signStr := fmt.Sprintf("client_id=%s&random=%s&timestamp=%d&secretKey=%s&identity=%s",
		clientID, sRandom, timestamp, secretKey, accessToken)

	// SHA256加密
	hash := sha256.Sum256([]byte(signStr))
	sign := hex.EncodeToString(hash[:])

	// 构建请求参数
	bodyData := make(map[string]any)
	bodyData["identity"] = accessToken
	bodyData["client_id"] = clientID
	bodyData["timestamp"] = timestamp
	bodyData["random"] = sRandom
	bodyData["sign"] = sign

	jsonStr, err := json.Marshal(bodyData)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// 构建请求URL
	requestURL := fmt.Sprintf("%s/api/rest/v1/auth/validate_multi_token", e.Config.Endpoint)

	// 创建请求
	req, err := http.NewRequest("POST", requestURL, bytes.NewReader(jsonStr))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// 设置请求头
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-request-id", uuid.New().String())

	// 发送请求
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// 读取响应
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// 解析响应
	var result ValidateTokenResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	// 检查响应状态码
	if result.Code != 200 {
		return nil, fmt.Errorf("validate multi token failed: code=%d, msg=%s", result.Code, result.Msg)
	}

	// 检查返回数据
	if result.Data == nil {
		return nil, fmt.Errorf("validate multi token failed: data is nil")
	}

	return result.Data, nil
}

// GetSandboxUgateTokenWithLoginUser 通过用户名获取 sandbox ugate token（包装方法）
// 判断逻辑：
//   - username 等于 loginUser，走正常 token 获取流程
//   - 或业务开启特权模式（privileged），走正常 token 获取流程
func (e *Api) GetSandboxUgateTokenWithLoginUser(sandboxUUID, username string, appID int32, envirVars, loginUser string) (sandboxToken string, token string) {
	if username == loginUser {
		return e.GetSandboxUgateToken(sandboxUUID, username, appID, envirVars)
	}

	_, _, privileged := common.IsUserInClusterDnsWhitelistWithPlatform(appID, username)
	if privileged {
		return e.GetSandboxUgateToken(sandboxUUID, username, appID, envirVars)
	}

	return "", ""
}

// GetSandboxUgateToken 通过用户名获取 sandbox ugate token
// 返回 sandboxToken（用于 createpod）和 token（用于缓存）
// 优先从 Redis 缓存读取，缓存未命中时调用接口并写入缓存
// 参数：
//   - sandboxUUID: 沙箱 UUID
//   - username: 用户名
//   - appID: 应用 ID
//   - envirVars: 环境变量 JSON 字符串，格式：{"INFOFLOW_ACCESS_TOKEN":"XXX","INFOFLOW_APP_KEY":"XXX","INFOFLOW_APP_SECRET":"XXX","INFOFLOW_ENCODING_AES_KEY":"XXX"}
func (e *Api) GetSandboxUgateToken(sandboxUUID, username string, appID int32, envirVars string) (sandboxToken string, token string) {
	if username == "" {
		return "", ""
	}

	ctx := context.Background()

	// 优先从缓存读取
	if sandboxUUID != "" {
		cachedSandboxToken, cachedToken, found := common.GetUgateTokenFromCache(ctx, nil, appID, username, sandboxUUID)
		if found {
			return cachedSandboxToken, cachedToken
		}
	}

	// 使用 IsUserInClusterDnsWhitelist 判断用户是否在集群 DNS 配置白名单中
	inWhitelist, platform, _ := common.IsUserInClusterDnsWhitelistWithPlatform(appID, username)
	if !inWhitelist {
		// 用户不在白名单中，不返回 token
		return "", ""
	}

	// 生成时间戳和随机数
	timestamp := time.Now().UnixMilli()
	sRandom := uuid.New().String()[:12] // 取前12位,与示例中的随机数格式一致

	clientID := e.Config.ComateClientID
	secretKey := e.Config.ComateClientSecret

	// 生成签名
	signStr := fmt.Sprintf("client_id=%s&random=%s&timestamp=%d&secretKey=%s&identity=%s",
		clientID, sRandom, timestamp, secretKey, username)

	// SHA256加密
	hash := sha256.Sum256([]byte(signStr))
	sign := hex.EncodeToString(hash[:])

	// 构建请求参数
	bodyData := make(map[string]any)
	bodyData["identity"] = username
	bodyData["client_id"] = clientID
	bodyData["timestamp"] = timestamp
	bodyData["random"] = sRandom
	bodyData["sign"] = sign
	bodyData["sandboxId"] = sandboxUUID
	bodyData["platform"] = platform
	jsonStr, _ := json.Marshal(bodyData)

	// 构建请求URL
	requestURL := fmt.Sprintf("%s/api/rest/v1/auth/sandbox_ugate_token", e.Config.Endpoint)

	// 创建请求
	req, _ := http.NewRequest("POST", requestURL, bytes.NewReader(jsonStr))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-request-id", uuid.New().String())

	// 发送请求
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", ""
	}
	defer resp.Body.Close()

	// 读取响应
	respBody, _ := io.ReadAll(resp.Body)

	// 解析响应
	var result TokenResult
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", ""
	}

	// 检查响应是否包含 sandboxToken
	if result.Code != 200 || result.Data == nil || len(result.Data.SandboxToken) == 0 {
		return "", ""
	}

	// 将 RawMessage 转为字符串，去掉外层的引号（如果有的话）
	var actualSandboxToken string
	var resultSandboxToken, resultToken string

	// 如果返回的是 JSON 字符串（带引号），需要反序列化一次得到真实内容
	if err := json.Unmarshal(result.Data.SandboxToken, &actualSandboxToken); err == nil {
		resultSandboxToken = actualSandboxToken
		resultToken = result.Data.Token
	} else {
		// 如果不是 JSON 字符串，直接返回原始内容
		resultSandboxToken = string(result.Data.SandboxToken)
		resultToken = result.Data.Token
	}

	// 写入缓存
	if (sandboxUUID != "" || common.IsUserSandboxMode(appID)) && resultSandboxToken != "" && resultToken != "" {
		_ = common.SetUgateTokenCache(ctx, nil, appID, username, sandboxUUID, resultSandboxToken, resultToken)
	}

	// 异步推送 Ugate Token 到 UUAP（如果有环境变量配置）
	if envirVars != "" && sandboxUUID != "" && resultToken != "" {
		go e.asyncPushSandboxUgateToken(sandboxUUID, resultToken, appID, platform, envirVars)
	}

	return resultSandboxToken, resultToken
}

// asyncPushSandboxUgateToken 异步推送沙箱 Ugate Token 到 UUAP
// 从环境变量中解析如流机器人配置，异步调用推送接口
// 当 appID 等于 dodo 时，botAppkey 和 botSecret 可以为空，此时推送的值都为 "dodo"
func (e *Api) asyncPushSandboxUgateToken(sandboxUUID, token string, appID int32, platform string, envirVars string) {
	ctx := context.Background()
	logger := resource.LoggerService

	// 解析环境变量 JSON
	var envMap map[string]string
	if err := json.Unmarshal([]byte(envirVars), &envMap); err != nil {
		logger.Error(ctx, "failed to parse envirVars",
			logit.String("sandboxUuid", sandboxUUID),
			logit.String("error", err.Error()))
		return
	}

	// 获取如流机器人配置
	botAppkey := envMap["INFOFLOW_APP_KEY"]
	botSecrect := envMap["INFOFLOW_APP_SECRET"]

	if botAppkey == "" {
		botAppkey = "botAppkey"
	}
	if botSecrect == "" {
		botSecrect = "botSecrect"
	}

	// 构建推送请求
	pushReq := &PushSandboxUgateRequest{
		Token:      token,
		Platform:   platform,
		BotAppkey:  botAppkey,
		BotSecrect: botSecrect,
		SandboxID:  sandboxUUID,
	}

	// 调用推送接口
	_, err := PushSandboxUgateToken(ctx, logger, pushReq)
	if err != nil {
		logger.Error(ctx, "async push sandbox ugate token failed",
			logit.String("sandboxUuid", sandboxUUID),
			logit.String("platform", platform),
			logit.String("error", err.Error()))
		return
	}

	logger.Notice(ctx, "async push sandbox ugate token completed",
		logit.String("sandboxUuid", sandboxUUID),
		logit.String("platform", platform))
}

// BothTokensResult 同时返回两个 token 的结果
type PersonalTokensResult struct {
	PersonalToken     string `json:"personal_token"`
	SandboxUgateToken string `json:"sandbox_ugate_token"`
}

// GetBothTokens 同时获取 personal token 和 sandbox ugate token
func (e *Api) GetPersonalTokens(username string, appID int32) *PersonalTokensResult {
	if username == "" {
		return nil
	}

	sandboxUgateToken, _ := e.GetSandboxUgateToken("", username, appID, "")
	result := &PersonalTokensResult{
		PersonalToken:     e.GetPersonalTokenByUsername(username),
		SandboxUgateToken: sandboxUgateToken,
	}

	return result
}

// aesEncrypt AES加密 (ECB模式，为了兼容Java端的实现)
func (e *Api) aesEncrypt(plainText, key string) string {
	// key必须是16/24/32字节
	block, err := aes.NewCipher([]byte(key))
	if err != nil {
		return ""
	}

	// PKCS7 Padding: 补齐到blockSize的整数倍
	blockSize := block.BlockSize()
	plainBytes := []byte(plainText)
	padding := blockSize - len(plainBytes)%blockSize
	plainBytes = append(plainBytes, bytes.Repeat([]byte{byte(padding)}, padding)...)

	// ECB模式加密 (手动分块加密，Go标准库不提供ECB因其不安全)
	ciphertext := make([]byte, len(plainBytes))
	for i := 0; i < len(plainBytes); i += blockSize {
		block.Encrypt(ciphertext[i:i+blockSize], plainBytes[i:i+blockSize])
	}

	// Base64编码
	return base64.StdEncoding.EncodeToString(ciphertext)
}

// QuerySandboxTokenStatus 查询沙箱 Ugate-Token 状态
// 参数：
//   - sandboxId: 沙箱实例唯一标识
//   - username: 用户名
//   - platform: 平台标识，可选值：dodo / openclaw
//
// 返回：
//   - bool: Token状态，true表示有效，false表示无效
//   - error: 错误信息
func (e *Api) QuerySandboxTokenStatus(sandboxID, username, platform string) (bool, error) {
	if sandboxID == "" || username == "" || platform == "" {
		return false, fmt.Errorf("sandboxId, username and platform are required")
	}

	// 构建请求URL
	requestURL := fmt.Sprintf("%s?sandboxId=%s&username=%s&platform=%s",
		e.Config.SandboxTokenStatusEndpoint, sandboxID, username, platform)

	// 创建请求
	req, err := http.NewRequest("GET", requestURL, nil)
	if err != nil {
		return false, fmt.Errorf("failed to create request: %w", err)
	}

	// 设置请求头
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-request-id", uuid.New().String())

	// 发送请求
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// 读取响应
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, fmt.Errorf("failed to read response: %w", err)
	}

	// 解析响应
	var result SandboxTokenStatusResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return false, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	// 检查响应状态码
	if result.Code != 200 {
		return false, fmt.Errorf("query sandbox token status failed: code=%d, msg=%s, tracingId=%s",
			result.Code, result.Msg, result.TracingID)
	}

	return result.Result, nil
}
