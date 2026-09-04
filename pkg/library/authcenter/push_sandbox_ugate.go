package authcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/common/config"
	"icode.baidu.com/baidu/gdp/logit"
)

// PushSandboxUgateRequest 推送沙箱 Ugate Token 请求参数
type PushSandboxUgateRequest struct {
	Token      string // AI网关 ugate-token
	Platform   string // Agent 类型：openclaw、dodo
	BotAppkey  string // 如流机器人 AK
	BotSecrect string // 如流机器人 SK（注意：接口文档中拼写为 botSecrect，保持一致）
	SandboxID  string // 沙箱 ID
}

// PushSandboxUgateResponse 推送沙箱 Ugate Token 响应
type PushSandboxUgateResponse struct {
	Code      int    `json:"code"`
	Msg       string `json:"msg"`
	Language  string `json:"language"`
	TracingID string `json:"tracingId"`
	Result    struct {
		CurrentTime        int64 `json:"currentTime"`        // 毫秒时间戳
		CurrentTimeSeconds int64 `json:"currentTimeSeconds"` // 秒级时间戳
	} `json:"result"`
}

// PushSandboxUgateToken 推送沙箱 Ugate Token 到 UUAP
// 用于将 AI 网关 Agent 信息推送到 UUAP 系统
func PushSandboxUgateToken(ctx context.Context, logger logit.Logger, req *PushSandboxUgateRequest) (*PushSandboxUgateResponse, error) {
	authConfig := config.GetAuthCenterConfig()

	// 获取推送接口配置
	endpoint := authConfig.UuapPushEndpoint
	appKey := authConfig.UuapPushAppKey
	secretKey := authConfig.UuapPushSecretKey

	// 参数校验
	if endpoint == "" {
		return nil, fmt.Errorf("uuapPushEndpoint is not configured")
	}
	if appKey == "" {
		return nil, fmt.Errorf("uuapPushAppKey is not configured")
	}
	if secretKey == "" {
		return nil, fmt.Errorf("uuapPushSecretKey is not configured")
	}

	// 构建请求参数
	bodyParams := &url.Values{}
	bodyParams.Set("token", req.Token)
	bodyParams.Set("platform", req.Platform)
	bodyParams.Set("botAppKey", req.BotAppkey)
	bodyParams.Set("botSecret", req.BotSecrect) // 注意：接口文档中拼写为 botSecrect
	bodyParams.Set("sandboxId", req.SandboxID)

	// 生成签名（会自动添加 appKey, timestamp, sRandom, sign）
	sign := GenSign(appKey, secretKey, bodyParams)
	bodyParams.Set("sign", sign)

	// 记录请求日志（不记录敏感信息）
	logger.Notice(ctx, "push sandbox ugate token to UUAP",
		logit.String("endpoint", endpoint),
		logit.String("sandboxId", req.SandboxID),
		logit.String("platform", req.Platform),
	)

	// 发送 HTTP 请求
	httpClient := &http.Client{
		Timeout: 30 * time.Second,
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(bodyParams.Encode()))
	if err != nil {
		logger.Error(ctx, "failed to create http request",
			logit.String("error", err.Error()))
		return nil, fmt.Errorf("failed to create http request: %w", err)
	}

	// 设置 Content-Type
	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	// 发送请求
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		logger.Error(ctx, "failed to send push request",
			logit.String("error", err.Error()))
		return nil, fmt.Errorf("failed to send push request: %w", err)
	}
	defer resp.Body.Close()

	// 读取响应
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Error(ctx, "failed to read response body",
			logit.String("error", err.Error()))
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// 检查 HTTP 状态码
	if resp.StatusCode != http.StatusOK {
		logger.Error(ctx, "push request failed with non-200 status",
			logit.Int("statusCode", resp.StatusCode),
			logit.String("responseBody", string(body)))
		return nil, fmt.Errorf("push request failed with status %d: %s", resp.StatusCode, string(body))
	}

	// 解析响应
	var pushResp PushSandboxUgateResponse
	if err := json.Unmarshal(body, &pushResp); err != nil {
		logger.Error(ctx, "failed to parse response json",
			logit.String("error", err.Error()),
			logit.String("responseBody", string(body)))
		return nil, fmt.Errorf("failed to parse response json: %w", err)
	}

	// 检查业务返回码
	if pushResp.Code != 200 {
		logger.Error(ctx, "push request failed with business error",
			logit.Int("code", pushResp.Code),
			logit.String("msg", pushResp.Msg),
			logit.String("tracingId", pushResp.TracingID))
		return &pushResp, fmt.Errorf("push request failed: code=%d, msg=%s", pushResp.Code, pushResp.Msg)
	}

	logger.Notice(ctx, "successfully pushed sandbox ugate token to UUAP",
		logit.String("sandboxId", req.SandboxID),
		logit.String("tracingId", pushResp.TracingID))

	return &pushResp, nil
}
