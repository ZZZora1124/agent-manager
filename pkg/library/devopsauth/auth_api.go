package devopsauth

import (
	"encoding/json"
	"fmt"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util"
	"icode.baidu.com/baidu/gdp/conf"
	"io"
	"net/http"
	"net/url"
)

type AuthApi struct {
	Config *AuthApiConfig
}

var authApi *AuthApi = nil
var authConfig *AuthApiConfig = nil

func GetAuthApi() *AuthApi {

	if authConfig == nil {
		config := &AuthApiConfig{}
		err := conf.Parse("devops_auth.toml", config)
		if err != nil {
			panic("parse magellan auth config error")
		}
		authConfig = config
	}
	if authApi == nil {
		authApi = &AuthApi{
			Config: authConfig,
		}
	}
	return authApi
}

func (a AuthApi) ValidateUser(userToken string) string {
	var validateRes ValidateResponse
	err := util.Backoff(util.DefaultRetry, func() (bool, error) {
		// 创建 URL 参数
		params := url.Values{}
		params.Add("accessToken", userToken)

		urlStr := fmt.Sprintf("%s/api/rest/v1/auth/validate?%s", a.Config.AuthEndpoint, params.Encode())
		req, _ := http.NewRequest("GET", urlStr, nil)
		resp, err := (&http.Client{}).Do(req)
		if err != nil {
			// will retry
			return false, nil
		}

		defer resp.Body.Close()

		respByte, _ := io.ReadAll(resp.Body)
		err = json.Unmarshal(respByte, &validateRes)
		if err != nil {
			// will retry
			return false, nil
		}
		return true, nil
	})

	if err != nil {
		return ""
	}
	if validateRes.Code != 200 {
		return ""
	}
	if validateRes.Data == nil {
		return ""
	}
	return validateRes.Data.Username
}
