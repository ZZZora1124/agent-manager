package authcenter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/common/config"
)

// BrowserAutoLoginRequest 浏览器自动登录Cookie刷新请求参数
type BrowserAutoLoginRequest struct {
	SandboxID string `json:"sandboxId"`
	AuthToken string `json:"authToken"` // 用来鉴权
	Username  string `json:"username"`
}

// BrowserAutoLoginResponse 浏览器自动登录Cookie刷新响应
type BrowserAutoLoginResponse struct {
	Code int                   `json:"code"`
	Msg  string                `json:"msg"`
	Data *BrowserAutoLoginData `json:"data,omitempty"`
}

// BrowserAutoLoginData 浏览器自动登录返回的数据
type BrowserAutoLoginData struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
}

// CookieDeleteRequest 删除浏览器自动登录Cookie的请求参数
type CookieDeleteRequest struct {
	AuthToken string `json:"authToken"` // 用来鉴权
	Username  string `json:"username"`
}

// CookieDeleteResponse 删除浏览器自动登录Cookie的响应
type CookieDeleteResponse struct {
	Code    int               `json:"code"`
	Message string            `json:"message"`
	Data    *CookieDeleteData `json:"data,omitempty"`
}

// CookieDeleteData 删除Cookie返回的数据
type CookieDeleteData struct {
	Message string `json:"message"`
}

// RefreshBrowserLoginCookie 刷新浏览器自动登录的Cookie
// sandboxId: 沙箱ID
// username: 用户名
// appID: 应用ID，只有当 appID == DodoAppId 时才执行刷新
// authToken 从配置文件 e.Config.DodoAccessToken 中读取
func (e *Api) RefreshBrowserLoginCookie(sandboxId, username string, appID int32) (*BrowserAutoLoginResponse, error) {
	// 只处理 Dodo 应用的请求
	if appID != config.GetDodoAppId() {
		return nil, nil
	}

	if sandboxId == "" {
		return nil, fmt.Errorf("sandboxId is empty")
	}
	if username == "" {
		return nil, fmt.Errorf("username is empty")
	}

	// 从配置中获取 authToken
	authToken := e.Config.DodoAccessToken
	if authToken == "" {
		return nil, fmt.Errorf("dodoAccessToken is not configured")
	}

	// 构建请求体
	reqBody := BrowserAutoLoginRequest{
		SandboxID: sandboxId,
		AuthToken: authToken,
		Username:  username,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}

	// 构建请求URL
	// 注意：这里的endpoint需要从配置中获取或作为参数传入
	// 暂时使用示例中的地址，实际使用时应该从 e.Config 中读取
	requestURL := fmt.Sprintf("%s/api/v1/browser/auto-login/cookie-refresh", e.Config.BrowserAutoLoginEndpoint)

	// 创建请求
	req, err := http.NewRequest("POST", requestURL, bytes.NewReader(jsonData))
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
	var result BrowserAutoLoginResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	// 检查响应状态
	if resp.StatusCode != http.StatusOK {
		return &result, fmt.Errorf("request failed: status=%d, code=%d, msg=%s",
			resp.StatusCode, result.Code, result.Msg)
	}

	return &result, nil
}

// DeleteBrowserLoginCookie 删除浏览器自动登录的Cookie
// username: 用户名
// appID: 应用ID，只有当 appID == DodoAppId 时才执行删除
// authToken 从配置文件 e.Config.DodoAccessToken 中读取
func (e *Api) DeleteBrowserLoginCookie(username string, appID int32) (*CookieDeleteResponse, error) {
	// 只处理 Dodo 应用的请求
	if appID != config.GetDodoAppId() {
		return nil, nil
	}

	if username == "" {
		return nil, fmt.Errorf("username is empty")
	}

	// 从配置中获取 authToken
	authToken := e.Config.DodoAccessToken
	if authToken == "" {
		return nil, fmt.Errorf("dodoAccessToken is not configured")
	}

	// 构建请求体
	reqBody := CookieDeleteRequest{
		AuthToken: authToken,
		Username:  username,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}

	// 构建请求URL
	requestURL := fmt.Sprintf("%s/api/v1/browser/auto-login/cookie-delete", e.Config.BrowserAutoLoginEndpoint)

	// 创建请求
	req, err := http.NewRequest("POST", requestURL, bytes.NewReader(jsonData))
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
	var result CookieDeleteResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	// 检查响应状态
	if resp.StatusCode != http.StatusOK {
		return &result, fmt.Errorf("request failed: status=%d, code=%d, message=%s",
			resp.StatusCode, result.Code, result.Message)
	}

	return &result, nil
}
