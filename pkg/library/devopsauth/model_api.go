package devopsauth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util"
	"io"
	"net/http"
	"net/url"
)

// ModelResponse 定义模型API返回的数据结构
type ModelResponse struct {
	RequestId  string `json:"requestId"`
	Code       int    `json:"code"`
	Message    string `json:"message"`
	ModelToken string `json:"modelToken"`
	UserName   string `json:"userName"`
}

// GetTokenRequest 定义获取个人token的请求体
type GetTokenRequest struct {
	Name           string `json:"name"`
	ExpiredTime    int    `json:"expired_time"`
	UnlimitedQuota bool   `json:"unlimited_quota"`
	Models         string `json:"models"`
}

// GetTokenResponse 定义获取个人token的响应
type GetTokenResponse struct {
	Success bool      `json:"success"`
	Message string    `json:"message"`
	Data    TokenData `json:"data"`
}

// TokenData 定义 token 数据结构
type TokenData struct {
	ID             int    `json:"id"`
	UserID         int    `json:"user_id"`
	Username       string `json:"username"`
	Key            string `json:"key"`
	Status         int    `json:"status"`
	Name           string `json:"name"`
	CreatedTime    int64  `json:"created_time"`
	AccessedTime   int64  `json:"accessed_time"`
	ExpiredTime    int64  `json:"expired_time"`
	RemainQuota    int    `json:"remain_quota"`
	UnlimitedQuota bool   `json:"unlimited_quota"`
	UsedQuota      int    `json:"used_quota"`
	Models         string `json:"models"`
	Subnet         string `json:"subnet"`
	Remark         string `json:"remark"`
}

// GetModelInfo 调用模型JWT API获取模型信息
// accessToken: 用户的访问令牌 (格式如: Bearer-eyJ...)
func (a AuthApi) GetModelInfo(accessToken string) (*ModelResponse, error) {
	var modelRes ModelResponse

	err := util.Backoff(util.DefaultRetry, func() (bool, error) {
		// 创建 URL 参数
		params := url.Values{}
		params.Add("accessToken", accessToken)

		urlStr := fmt.Sprintf("%s/api/rest/v1/jwt/model?%s", a.Config.ModelAuthEndpoint, params.Encode())
		req, err := http.NewRequest("GET", urlStr, nil)
		if err != nil {
			return false, err
		}

		// 从配置读取客户端令牌
		req.Header.Set("x-auth-client-token", a.Config.ClientToken)

		resp, err := (&http.Client{}).Do(req)
		if err != nil {
			// will retry
			return false, nil
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return false, nil
		}

		respByte, err := io.ReadAll(resp.Body)
		if err != nil {
			// will retry
			return false, nil
		}

		err = json.Unmarshal(respByte, &modelRes)
		if err != nil {
			// will retry
			return false, nil
		}
		return true, nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to get model info after retries: %w", err)
	}

	if modelRes.Code != 200 {
		return nil, fmt.Errorf("model api returned error code: %d, message: %s", modelRes.Code, modelRes.Message)
	}

	return &modelRes, nil
}

// GetOpenclawGWToken 调用 OpenClaw GW API 获取个人 token
// userName: 用户名
// authorization: Authorization header 值
func (a AuthApi) GetOpenclawGWToken(userName string) (string, error) {
	var tokenRes GetTokenResponse

	reqBody := GetTokenRequest{
		Name:           userName,
		ExpiredTime:    -1,
		UnlimitedQuota: true,
		Models:         "",
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request body failed: %w", err)
	}

	err = util.Backoff(util.DefaultRetry, func() (bool, error) {
		urlStr := "https://openclaw-gw.baidu-int.com/openapi/get_token"
		req, err := http.NewRequest("POST", urlStr, bytes.NewBuffer(bodyBytes))
		if err != nil {
			return false, err
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "f4907910194640278f770b77232c92fc")

		resp, err := (&http.Client{}).Do(req)
		if err != nil {
			// will retry
			return false, nil
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return false, nil
		}

		respByte, err := io.ReadAll(resp.Body)
		if err != nil {
			// will retry
			return false, nil
		}

		err = json.Unmarshal(respByte, &tokenRes)
		if err != nil {
			// will retry
			return false, nil
		}
		return true, nil
	})

	if err != nil {
		return "", fmt.Errorf("failed to get personal token after retries: %w", err)
	}

	if !tokenRes.Success {
		return "", fmt.Errorf("openclaw gw api returned error: %s", tokenRes.Message)
	}

	return tokenRes.Data.Key, nil
}
