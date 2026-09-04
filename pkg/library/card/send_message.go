package card

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/resource"
	"icode.baidu.com/baidu/gdp/logit"
)

// SendMessageClient 发送消息客户端
type SendMessageClient struct {
	appKey     string
	appSecret  string
	authURL    string
	messageURL string
	client     *http.Client
}

// NewSendMessageClient 创建发送消息客户端
func NewSendMessageClient(appKey, appSecret string) (*SendMessageClient, error) {
	if appKey == "" || appSecret == "" {
		return nil, fmt.Errorf("appKey and appSecret cannot be empty")
	}

	return &SendMessageClient{
		appKey:     appKey,
		appSecret:  appSecret,
		authURL:    "http://apiin.im.baidu.com/api/v1/auth/app_access_token",
		messageURL: "http://apiin.im.baidu.com/api/v1/app/message/send",
		client:     &http.Client{},
	}, nil
}

// SendMessageRequest 发送消息请求
type SendMessageRequest struct {
	// 接收消息的用户列表（uuapName，即邮箱前缀），多个用户用 | 分隔
	ToUser string `json:"touser"`

	// 消息类型，目前支持 text
	MsgType string `json:"msgtype"`

	// 文本消息内容
	Text *TextContent `json:"text,omitempty"`
}

// TextContent 文本消息内容
type TextContent struct {
	Content string `json:"content"`
}

// NewTextMessageRequest 创建文本消息请求
func NewTextMessageRequest(toUser, content string) *SendMessageRequest {
	return &SendMessageRequest{
		ToUser:  toUser,
		MsgType: "text",
		Text: &TextContent{
			Content: content,
		},
	}
}

// SendMessageResponse 发送消息响应
type SendMessageResponse struct {
	ErrCode     int    `json:"errcode"`
	ErrMsg      string `json:"errmsg"`
	MsgKey      string `json:"msgkey,omitempty"`
	InvalidUser string `json:"invaliduser,omitempty"`
}

// GetAppToken 获取应用访问令牌（复用 SendCardClient 的实现）
func (c *SendMessageClient) GetAppToken(ctx context.Context) (string, error) {
	if c.appKey == "" || c.appSecret == "" {
		return "", fmt.Errorf("please configure app credentials")
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
	resource.LoggerService.Notice(ctx, "successfully got app token", logit.String("appToken", fullToken))
	return fullToken, nil
}

// SendTextMessage 发送文本消息（便捷方法）
func (c *SendMessageClient) SendTextMessage(ctx context.Context, toUser string, content string, appKey string, appSecret string) error {
	client, err := NewSendMessageClient(appKey, appSecret)
	if err != nil {
		resource.LoggerService.Notice(ctx, "failed to create send message client", logit.String("error", err.Error()))
		return err
	}

	// 获取 app_access_token
	appToken, err := client.GetAppToken(ctx)
	if err != nil {
		resource.LoggerService.Notice(ctx, "failed to get app token", logit.String("error", err.Error()))
		return err
	}

	// 构造请求体
	request := NewTextMessageRequest(toUser, content)

	// 发送消息
	if err := client.doSendMessage(ctx, appToken, request); err != nil {
		resource.LoggerService.Notice(ctx, "failed to send message", logit.String("error", err.Error()))
		return err
	}

	return nil
}

// doSendMessage 发送消息（内部方法）
func (c *SendMessageClient) doSendMessage(ctx context.Context, appToken string, sendMsgReq *SendMessageRequest) error {
	if appToken == "" {
		return fmt.Errorf("appToken cannot be empty")
	}
	if sendMsgReq == nil {
		return fmt.Errorf("sendMessageRequest cannot be empty")
	}

	resource.LoggerService.Notice(ctx, "sending message request",
		logit.String("toUser", sendMsgReq.ToUser),
		logit.String("msgType", sendMsgReq.MsgType))

	// 序列化请求体
	jsonData, err := json.Marshal(sendMsgReq)
	if err != nil {
		return fmt.Errorf("failed to serialize request body: %w", err)
	}

	// 创建HTTP请求
	req, err := http.NewRequest(http.MethodPost, c.messageURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// 设置请求头
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", appToken)
	// 业务方发送时指定，用于问题定位。请传入具有唯一性的值
	req.Header.Set("LOGID", GenerateLogID())

	// 发送请求
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// 读取响应
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	// 解析响应
	var msgResp SendMessageResponse
	if err := json.Unmarshal(body, &msgResp); err != nil {
		return fmt.Errorf("failed to parse response: %w, body=%s", err, string(body))
	}

	resource.LoggerService.Notice(ctx, "send message response",
		logit.Int("errcode", msgResp.ErrCode),
		logit.String("errmsg", msgResp.ErrMsg),
		logit.String("msgkey", msgResp.MsgKey))

	// 检查响应状态
	if msgResp.ErrCode != 0 {
		return fmt.Errorf("failed to send message, errcode=%d, errmsg=%s, invaliduser=%s", msgResp.ErrCode, msgResp.ErrMsg, msgResp.InvalidUser)
	}

	return nil
}
