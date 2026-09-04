package card

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/common"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/common/config"
	"io"
	"log"
	"math"
	"net/http"
	"strings"
	"time"

	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/eacauth"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/resource"
	"icode.baidu.com/baidu/gdp/logit"

	"github.com/google/uuid"
)

// SendCardClient 发送卡片客户端
type SendCardClient struct {
	appKey    string
	appSecret string
	authURL   string
	cardURL   string
	client    *http.Client
}

// NewSendCardClient 创建发送卡片客户端
func NewSendCardClient(appKey, appSecret string) *SendCardClient {
	if appKey == "" || appSecret == "" {
		log.Fatal("appKey and appSecret cannot be empty")
	}

	return &SendCardClient{
		appKey:    appKey,
		appSecret: appSecret,
		authURL:   "http://apiin.im.baidu.com/api/v1/auth/app_access_token",
		cardURL:   "http://apiin.im.baidu.com/api/v1/card/sender/card_msg",
		client: &http.Client{
			Timeout: 3 * time.Second,
		},
	}
}

// NewSendCardClientWithConfig 使用自定义配置创建客户端
func NewSendCardClientWithConfig(appKey, appSecret, authURL, cardURL string, timeout time.Duration) *SendCardClient {
	if appKey == "" || appSecret == "" {
		log.Fatal("appKey and appSecret cannot be empty")
	}

	return &SendCardClient{
		appKey:    appKey,
		appSecret: appSecret,
		authURL:   authURL,
		cardURL:   cardURL,
		client: &http.Client{
			Timeout: timeout,
		},
	}
}

// NewSendCardClientFromConfig 从 eacauth 配置构建客户端
func NewSendCardClientFromConfig() *SendCardClient {
	cfg := eacauth.GetEacAuthConfig()
	return NewSendCardClientWithConfig(cfg.UserSendCardAppKey, cfg.UserSendCardAppSecret,
		cfg.UserSendCardHost+cardAuthURI,
		cfg.UserSendCardHost+cardSendURI,
		3*time.Second)
}

// ===== 数据结构定义 =====

const (
	ResponseSuccess        = "ok"
	ResponseCode           = "code"
	ResponseData           = "data"
	AppAccessToken         = "app_access_token"
	AppTokenPrefix         = "Bearer-"
	dynamicCardUpdateURI   = "/v1/msg/modifier/interactivity_personal_msg_content"
	cardAuthURI            = "/v1/auth/app_access_token"
	cardSendURI            = "/v1/card/sender/card_msg"
	dynamicCardUserAuthURI = "/v1/auth/uuap/user_access_token"
)

// UpdateCardRequest 更新工作卡请求
type UpdateCardRequest struct {
	ModifyToken           string                  `json:"modify_token"`
	NewPersonalMsgContent []UpdateCardPersonalMsg `json:"new_personal_msg_content"`
}

// UpdateCardPersonalMsg 个性化消息内容
type UpdateCardPersonalMsg struct {
	UserIDs            []string `json:"user_ids"`
	PersonalMsgContent any      `json:"personal_msg_content"`
}

// AppCertificate 应用凭证
type AppCertificate struct {
	AppKey    string `json:"app_key"`
	AppSecret string `json:"app_secret"`
}

// SendCardRequest 发送卡片请求
// 包含WorkCardUserData对象和其他发卡必填参数
// https://ku.baidu-int.com/knowledge/HFVrC7hq1Q/2tsPs8CtSd/Bu7DDg4dpB/ewRrO8RmSYGIYB
type SendCardRequest struct {
	// 动态卡中配置动态变量集合
	// 开发者搭建平台-动态变量列表-变量预览
	UserData any `json:"user_data"`

	// 卡片模板ID
	TemplateID string `json:"template_id"`

	// true: 用户im消息，此时必须使用user-access-token
	// false：服务号消息、机器人消息
	UserMsg bool `json:"user_msg"`

	// user： 用户
	// group：群聊
	// 服务号消息这里只能填user，机器人消息这里只能填group
	ReceiverType string `json:"receiver_type"`

	// receiver_type为user时：用户的user_id（百度邮箱前缀）
	ReceiverID string `json:"receiver_id"`

	// 任意的上下文数据，互动请求回调时会原样返回（长度限制2000）
	// 必须要传入template_id，处理用户操作的回调请求时用得到
	AppContext map[string]interface{} `json:"app_context,omitempty"`

	// 期望后续允许客户端发起互动的有效时长（单位秒）
	// 默认值：3个月
	InteractivityExpire int `json:"interactivity_expire"`

	// 期望后续修改消息个性化内容的有效时长（单位秒）
	// 默认值：3个月
	ModifyExpire int `json:"modify_expire"`

	// 消息预览的提示文字
	OfflineNotifyTxt string `json:"offline_notify_txt,omitempty"`
}

// NewSendCardRequest 创建发送卡片请求
func NewSendCardRequest(userData any, templateID, receiverID string, offlineNotifyTxt string) *SendCardRequest {
	return &SendCardRequest{
		UserData:            userData,
		TemplateID:          templateID,
		ReceiverID:          receiverID,
		UserMsg:             false,
		ReceiverType:        "user",
		InteractivityExpire: 3600 * 24,
		ModifyExpire:        3600 * 24,
		OfflineNotifyTxt:    offlineNotifyTxt,
	}
}

// WorkCardUserData 工作卡用户数据（沙箱休眠通知）
type WorkCardUserData struct {
	Title               string `json:"title"`
	Img                 string `json:"img"`
	Content             string `json:"content"`
	CallbackButtonState string `json:"callbackButtonState"`
	CallbackButtonText  string `json:"callbackButtonText"`
}

// NewDefaultWorkCardUserData 创建默认工作卡用户数据
func NewDefaultWorkCardUserData() *WorkCardUserData {
	return &WorkCardUserData{
		Title:               "您的机器人要休息了",
		Img:                 "https://bj.bcebos.com/dev-platform/app/data/public/46ea711f9d25485eb7d48815cc296061",
		Content:             "检测到您的沙箱已空闲一段时间，看起\n来我可以去休眠一下。需要我为您服务的时候，\n请点击下面的『恢复服务』来唤醒我哦~",
		CallbackButtonState: "normal",
		CallbackButtonText:  "恢复服务",
	}
}

// AccessApplyCardUserData 沙箱访问权限申请卡片数据
type AccessApplyCardUserData struct {
	Title                 string `json:"title,omitempty"`
	Img                   string `json:"img,omitempty"`
	Text                  string `json:"text,omitempty"`
	Platform              string `json:"platform,omitempty"`
	DefaultButton         string `json:"defaultButton,omitempty"`
	ApplyUser             string `json:"applyUser,omitempty"`
	SandboxID             string `json:"sandboxId,omitempty"`
	AccessTime            string `json:"accessTime,omitempty"`
	ApproveStatus         string `json:"approveStatus,omitempty"`
	ShowAgreeButton       string `json:"showAgreeButton,omitempty"`
	ShowRejectButton      string `json:"showRejectButton,omitempty"`
	ShowApplyResultButton string `json:"showApplyResultButton,omitempty"`
	TitleImg              string `json:"title_img,omitempty"`
	ApplyReason           string `json:"applyReason,omitempty"`
	RiskWarning           string `json:"riskWarning,omitempty"`
}

// NewAccessApplyCardUserData 创建沙箱访问权限申请卡片数据
func NewAccessApplyCardUserData(applyUser, sandboxID, platform, applyReason string) *AccessApplyCardUserData {
	return &AccessApplyCardUserData{
		Title:                 "沙箱访问权限申请",
		Platform:              platform,
		ApplyUser:             applyUser,
		SandboxID:             sandboxID,
		AccessTime:            "1小时",
		ApproveStatus:         "",
		ShowAgreeButton:       "1",
		ShowRejectButton:      "1",
		ShowApplyResultButton: "0",
		TitleImg:              "https://bj.bcebos.com/dev-platform/app/data/public/46ea711f9d25485eb7d48815cc296061",
		ApplyReason:           applyReason,
		RiskWarning:           "谨慎同意：\n1.您的沙箱内已注入个人权限\n2.同意他人进入沙箱 → 对方将能够使用你的权限执行操作\n3.将视同为您本人或您授权的行为，相关后果将由您承担。",
	}
}

// APIResponse API响应结构
type APIResponse struct {
	Code string                 `json:"code"`
	Msg  string                 `json:"msg"`
	Data map[string]interface{} `json:"data"`
}

// sendCardResponseData 发卡响应 data 字段结构
type sendCardResponseData struct {
	Receivers []struct {
		ModifyToken string `json:"modify_token"`
	} `json:"receivers"`
}

// GetAppToken 获取应用访问令牌
func (c *SendCardClient) GetAppToken() (string, error) {
	if c.appKey == "" || c.appSecret == "" {
		return "", fmt.Errorf("please configure app credentials, see https://ku.baidu-int.com/knowledge/HFVrC7hq1Q/2tsPs8CtSd/Bu7DDg4dpB/ewRrO8RmSYGIYB#anchor-60d85220-ed32-11ec-afbd-8f9f74ad5743")
	}

	// MD5加密app_secret
	md5Secret := MD5Encode(c.appSecret)

	appCert := AppCertificate{
		AppKey:    c.appKey,
		AppSecret: md5Secret,
	}

	// 序列化请求体
	jsonData, err := json.Marshal(appCert)
	if err != nil {
		return "", fmt.Errorf("failed to serialize request body: %w", err)
	}

	// 创建HTTP请求
	req, err := http.NewRequest(http.MethodPost, c.authURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// 发送请求
	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// 读取响应
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	// 解析响应
	var apiResp APIResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return "", fmt.Errorf("failed to parse response: %w, body=%s", err, string(body))
	}

	// 检查响应状态
	if apiResp.Code != ResponseSuccess {
		return "", fmt.Errorf("failed to get app token, code=%s, msg=%s", apiResp.Code, apiResp.Msg)
	}

	// 提取token
	tokenData, ok := apiResp.Data[AppAccessToken]
	if !ok {
		return "", fmt.Errorf("app_access_token field not found in response")
	}

	appToken, ok := tokenData.(string)
	if !ok {
		return "", fmt.Errorf("app_access_token is not a string type")
	}

	fullToken := AppTokenPrefix + appToken
	resource.LoggerService.Notice(context.Background(), "successfully got app token", logit.String("appToken", fullToken))
	return fullToken, nil
}

func (c *SendCardClient) SendCardMsg(receiver string, appKey string, appSecret string,
	sandboxID string) error {
	client := NewSendCardClient(appKey, appSecret)

	appToken, err := client.GetAppToken()
	if err != nil {
		resource.LoggerService.Notice(context.Background(), "failed to get app token", logit.String("error", err.Error()))
		return err
	}

	// 配置卡片参数
	templateID := "d2bd2fdf8617491ca482e10727ca7ca8"

	// 构造用户数据
	userData := NewDefaultWorkCardUserData()
	// 可以自定义用户数据
	// userData.Title = "自定义标题"
	// userData.Content = "自定义内容"

	// 构造请求体
	request := NewSendCardRequest(userData, templateID, receiver, "沙箱恢复")

	// 可选：设置app_context
	request.AppContext = map[string]interface{}{
		"sandboxID": sandboxID,
	}

	// 发送卡片
	if _, err := client.doSendCardMsg(appToken, request); err != nil {
		resource.LoggerService.Notice(context.Background(), "failed to send card", logit.String("error", err.Error()))
		return err
	}

	return nil
}

// SendCardMsg 发送卡片消息
func (c *SendCardClient) doSendCardMsg(appToken string, sendCardReq *SendCardRequest, extraHeaders ...map[string]string) (string, error) {
	if appToken == "" {
		return "", fmt.Errorf("appToken cannot be empty")
	}
	if sendCardReq == nil {
		return "", fmt.Errorf("sendCardRequest cannot be empty")
	}

	resource.LoggerService.Notice(context.Background(), "sending card request",
		logit.String("templateId", sendCardReq.TemplateID),
		logit.String("receiverId", sendCardReq.ReceiverID))

	// 序列化请求体
	jsonData, err := json.Marshal(sendCardReq)
	if err != nil {
		return "", fmt.Errorf("failed to serialize request body: %w", err)
	}

	// 创建HTTP请求
	req, err := http.NewRequest(http.MethodPost, c.cardURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	// 设置请求头
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", appToken)
	req.Header.Set("LOGID", GenerateLogID())
	for _, headers := range extraHeaders {
		for k, v := range headers {
			req.Header.Set(k, v)
		}
	}

	// 发送请求
	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// 读取响应
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	// 解析响应
	var apiResp APIResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return "", fmt.Errorf("failed to parse response: %w, body=%s", err, string(body))
	}

	resource.LoggerService.Notice(context.Background(), "send card response",
		logit.String("code", apiResp.Code),
		logit.String("msg", apiResp.Msg))

	// 检查响应状态
	if apiResp.Code != ResponseSuccess {
		return "", fmt.Errorf("failed to send card, code=%s, msg=%s", apiResp.Code, apiResp.Msg)
	}

	// 解析 modifyToken
	dataBytes, _ := json.Marshal(apiResp.Data)
	var respData sendCardResponseData
	if err := json.Unmarshal(dataBytes, &respData); err == nil && len(respData.Receivers) > 0 {
		return respData.Receivers[0].ModifyToken, nil
	}
	return "", nil
}

// UpdateCardByOAuthCode 用回调带来的 oauthCode 换取 userAccessToken，然后更新卡片内容
func (c *SendCardClient) UpdateCardByOAuthCode(ctx context.Context, oauthCode, modifyToken, applyUser string, newUserData any) error {
	userAccessToken, err := c.getUserAccessTokenByOAuthCode(oauthCode)
	if err != nil {
		return fmt.Errorf("get userAccessToken by oauthCode failed: %w", err)
	}

	updateReq := &UpdateCardRequest{
		ModifyToken: modifyToken,
		NewPersonalMsgContent: []UpdateCardPersonalMsg{
			{UserIDs: []string{applyUser}, PersonalMsgContent: newUserData},
		},
	}

	jsonData, err := json.Marshal(updateReq)
	if err != nil {
		return fmt.Errorf("marshal update request failed: %w", err)
	}

	updateURL := eacauth.GetEacAuthConfig().UserSendCardHost + dynamicCardUpdateURI
	req, err := http.NewRequest(http.MethodPost, updateURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", AppTokenPrefix+userAccessToken)
	req.Header.Set("LOGID", GenerateLogID())

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var apiResp APIResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return fmt.Errorf("parse update response failed: body=%s", string(body))
	}
	if apiResp.Code != ResponseSuccess {
		return fmt.Errorf("update card failed, code=%s, msg=%s", apiResp.Code, apiResp.Msg)
	}
	resource.LoggerService.Notice(ctx, "update card success", logit.String("applyUser", applyUser))
	return nil
}

// getUserAccessTokenByOAuthCode 用 oauthCode 换取 user_access_token（对应 Java getUserAccessTokenByOauthCode）
func (c *SendCardClient) getUserAccessTokenByOAuthCode(oauthCode string) (string, error) {
	const retryTime = 3
	userTokenURL := eacauth.GetEacAuthConfig().UserSendCardHost + "/v1/auth/oauth/user_access_token"

	for i := 0; i < retryTime; i++ {
		appToken, err := c.GetAppToken()
		if err != nil {
			return "", err
		}

		req, err := http.NewRequest(http.MethodGet, userTokenURL, nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("Authorization", appToken)
		q := req.URL.Query()
		q.Set("code", oauthCode)
		req.URL.RawQuery = q.Encode()

		resp, err := c.client.Do(req)
		if err != nil {
			return "", err
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		bodyStr := string(body)
		if strings.Contains(bodyStr, "auth.invalidOAuthCode") {
			return "", fmt.Errorf("invalid oauthCode")
		}
		if strings.Contains(bodyStr, "token invalid") {
			resource.LoggerService.Notice(context.Background(), "appToken invalid, retrying", logit.Int("round", i))
			continue
		}

		var result struct {
			Code string `json:"code"`
			Data struct {
				UserAccessToken string `json:"user_access_token"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &result); err != nil {
			return "", fmt.Errorf("parse response failed: body=%s", bodyStr)
		}
		if strings.EqualFold(result.Code, "ok") && result.Data.UserAccessToken != "" {
			return result.Data.UserAccessToken, nil
		}
		return "", fmt.Errorf("get user_access_token by oauthCode failed, body=%s", bodyStr)
	}
	return "", fmt.Errorf("failed to get user_access_token after %d retries", retryTime)
}

// SendCardMsgByUser 以用户身份发送沙箱访问权限申请卡片
// uGateToken: 申请人的 ugate token，先换取 user_access_token 再发送
func (c *SendCardClient) SendCardMsgByUser(receiver, uGateToken, templateID string, userData any, sandboxID string, applyUser string, offlineNotifyTxt string) error {
	if uGateToken == "" {
		return fmt.Errorf("uGateToken cannot be empty")
	}

	userAccessToken, err := c.convertUGateToken(uGateToken)
	if err != nil {
		return fmt.Errorf("convert ugate token failed: %w", err)
	}

	request := NewSendCardRequest(userData, templateID, receiver, offlineNotifyTxt)
	request.UserMsg = true
	request.AppContext = map[string]any{
		"sandboxID": sandboxID,
		"applyUser": applyUser,
	}

	modifyToken, err := c.doSendCardMsg(AppTokenPrefix+userAccessToken, request)
	if err != nil {
		return err
	}

	// 发送成功后记录到 Redis，供后续更新卡片使用
	ctx := context.Background()
	record := &common.ApplyCardRecord{
		Receiver:    receiver,
		ModifyToken: modifyToken,
	}
	if redisErr := common.SetApplyCardRecord(ctx, resource.RedisClient, sandboxID, applyUser, record); redisErr != nil {
		resource.LoggerService.Notice(ctx, "save apply card record to redis failed",
			logit.String("sandboxID", sandboxID),
			logit.String("applyUser", applyUser),
			logit.String("error", redisErr.Error()))
	}
	return nil
}

// convertUGateToken 通过 uuap jwt 换取 user_access_token（参考 Java CardTokenService.getUserTokenByJwt）
// 步骤1: 调用 convert_token 将 ugate token 转换为 uuap jwt
// 步骤2: 用 uuap jwt 调如流接口换取 user_access_token
func (c *SendCardClient) convertUGateToken(uGateToken string) (string, error) {
	// Step1: convert ugate token -> uuap jwt
	authCfg := config.GetAuthCenterConfig()
	convertURL := fmt.Sprintf("%s/api/rest/v1/uuap/convert_token?authUGateToken=%s&uuapTargetAppKey=%s",
		authCfg.Endpoint, uGateToken, authCfg.UserSendCardTargetAppKey)
	convertReq, err := http.NewRequest(http.MethodPost, convertURL, nil)
	if err != nil {
		return "", err
	}
	convertReq.Header.Set("X-Request-ID", GenerateLogID())
	convertResp, err := c.client.Do(convertReq)
	if err != nil {
		return "", fmt.Errorf("convert_token request failed: %w", err)
	}
	convertBody, _ := io.ReadAll(convertResp.Body)
	convertResp.Body.Close()

	var convertResult struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data string `json:"data"`
	}
	if err := json.Unmarshal(convertBody, &convertResult); err != nil {
		return "", fmt.Errorf("parse convert_token response failed: %w, body=%s", err, string(convertBody))
	}
	if convertResult.Code != 200 {
		return "", fmt.Errorf("convert_token failed, code=%d, msg=%s", convertResult.Code, convertResult.Msg)
	}
	uuapJwt := convertResult.Data

	// Step2: uuap jwt -> user_access_token（参考 Java getUserTokenByJwt，重试3次）
	const retryTime = 3
	userTokenURL := eacauth.GetEacAuthConfig().UserSendCardHost + "/v1/auth/uuap/user_access_token"

	for i := 0; i < retryTime; i++ {
		appToken, err := c.GetAppToken()
		if err != nil {
			return "", fmt.Errorf("get app token failed: %w", err)
		}

		req, err := http.NewRequest(http.MethodGet, userTokenURL, nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("Authorization", appToken)
		req.Header.Set("uuap-token", uuapJwt)

		resp, err := c.client.Do(req)
		if err != nil {
			return "", err
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		var result struct {
			Code string `json:"code"`
			Data struct {
				UserAccessToken string `json:"user_access_token"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &result); err != nil {
			return "", fmt.Errorf("parse response failed: %w, body=%s", err, string(body))
		}

		bodyStr := string(body)
		if strings.Contains(bodyStr, "token invalid") || strings.Contains(bodyStr, "authorization header check invalid") {
			resource.LoggerService.Notice(context.Background(), "appToken invalid, retrying", logit.Int("round", i))
			continue
		}
		if !strings.EqualFold(result.Code, "ok") {
			return "", fmt.Errorf("get user token failed, body=%s", bodyStr)
		}

		if result.Data.UserAccessToken == "" {
			resource.LoggerService.Notice(context.Background(), "uuap jwt invalid, retrying", logit.Int("round", i))
			continue
		}
		return result.Data.UserAccessToken, nil
	}
	return "", fmt.Errorf("failed to get user_access_token after %d retries", retryTime)
}

// ===== 工具方法 =====

// MD5Encode MD5编码
func MD5Encode(raw string) string {
	hash := md5.New()
	hash.Write([]byte(raw))
	bytes := hash.Sum(nil)
	return hex.EncodeToString(bytes)
}

// GenerateLogID 生成日志ID
// 使用UUID生成唯一的日志ID
func GenerateLogID() string {
	uid := uuid.New()
	// 将UUID的两个部分相加取绝对值作为logID
	mostSig := int64(uid.ID())
	leastSig := int64(uid.ClockSequence())
	logID := math.Abs(float64(mostSig + leastSig))
	return fmt.Sprintf("%.0f", logID)
}

// GenerateUUIDLogID 生成UUID格式的日志ID（推荐使用）
func GenerateUUIDLogID() string {
	return uuid.New().String()
}
