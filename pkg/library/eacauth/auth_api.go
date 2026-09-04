package eacauth

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	util "icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

type EacAuthApi struct {
	Config *Config
}

type BaseResponse struct {
	Code   int               `json:"code"`
	Desc   string            `json:"desc"`
	Result *UserInfoResponse `json:"result"`
}
type UserInfoResponse struct {
	Username       string `json:"username"`
	Email          string `json:"email"`
	OrgChainNameEn string `json:"orgChainNameEn"`
}

var eacAuthApi *EacAuthApi = nil

func GetEacAuthApi() *EacAuthApi {
	if eacAuthApi == nil {
		eacAuthApi = &EacAuthApi{
			Config: GetEacAuthConfig(),
		}
	}
	return eacAuthApi
}

// GetUserByUsername 通过用户邮箱获取用户信息
func (e *EacAuthApi) GetUserByUsername(username string, returnFields string) (*UserInfoResponse, error) {
	// 生成时间戳和随机数
	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	sRandom := fmt.Sprintf("%d", time.Now().UnixNano()%1000000)

	// 按照字典排序生成签名字符串
	params := map[string]string{
		"appKey":       e.Config.AccessKey,
		"returnFields": returnFields,
		"sRandom":      sRandom,
		"timestamp":    timestamp,
		"username":     username,
	}

	// 按key排序
	var keys []string
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// 构建签名字符串
	var signStr string
	for _, k := range keys {
		signStr += params[k]
	}
	signStr += e.Config.SecretKey

	// SHA256加密
	hash := sha256.Sum256([]byte(signStr))
	sign := hex.EncodeToString(hash[:])

	// 构建请求参数
	formData := url.Values{}
	formData.Set("username", username)
	formData.Set("returnFields", returnFields)
	formData.Set("appKey", e.Config.AccessKey)
	formData.Set("timestamp", timestamp)
	formData.Set("sRandom", sRandom)
	formData.Set("sign", sign)

	res := &BaseResponse{}

	err := util.Backoff(util.DefaultRetry, func() (bool, error) {
		// 构建请求URL
		requestURL := fmt.Sprintf("%s/restful/v1/userInfo/getUserByUsername", e.Config.AuthEndpoint)

		// 创建请求
		req, err := http.NewRequest("POST", requestURL, strings.NewReader(formData.Encode()))
		if err != nil {
			return false, nil // 会重试
		}

		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

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
		if res.Code == 200 {
			return true, nil
		}

		return false, fmt.Errorf("user not found or invalid response")
	})

	if err != nil || res == nil || res.Result == nil {
		return nil, fmt.Errorf("failed to get user info: %v", err)
	}

	return res.Result, nil
}
