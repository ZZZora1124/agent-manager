package magellanauth

import (
	"encoding/json"
	"fmt"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util"
	"icode.baidu.com/baidu/gdp/conf"
	"icode.baidu.com/baidu/gdp/env"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type AuthApi struct {
	DevConfig *AuthApiConfig
	Config    *AuthApiConfig
}

var authApi *AuthApi = nil
var authConfig *AuthApiConfig = nil
var devAuthConfig *AuthApiConfig = nil

func GetAuthApi() *AuthApi {

	if authConfig == nil {
		config := &AuthApiConfig{}
		err := conf.Parse("magellan_auth.toml", config)
		if err != nil {
			panic("parse magellan auth config error")
		}
		authConfig = config
	}
	if devAuthConfig == nil {
		config := &AuthApiConfig{}
		err := conf.Parse("magellan_auth_dev.toml", config)
		if err != nil {
			panic("parse magellan auth config error")
		}
		devAuthConfig = config
	}
	if authApi == nil {
		authApi = &AuthApi{
			Config:    authConfig,
			DevConfig: devAuthConfig,
		}
	}
	return authApi
}

func (a AuthApi) GetCodeAuthJwt(repoType string, module string) string {
	cabs := []CodeAuthBean{
		{
			RepoType:   repoType,
			Repo:       module,
			ExpireHour: 6,
			Actions: []string{
				"read",
			},
		},
	}
	body, err := json.Marshal(cabs)
	if err != nil {
		return ""
	}

	var authConfig *AuthApiConfig
	if env.RunMode() == env.RunModeRelease {
		authConfig = a.Config
	} else {
		authConfig = a.DevConfig
	}
	var authResp []CodeAuthBean
	err = util.Backoff(util.DefaultRetry, func() (bool, error) {
		urlStr := fmt.Sprintf("%s/auth/rest/v1/namespaces/%s/code-auths", authConfig.Endpoint, authConfig.Namespace)
		req, _ := http.NewRequest("POST", urlStr, strings.NewReader(string(body)))
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", authConfig.Token))
		req.Header.Set("Content-Type", "application/json")
		resp, err := (&http.Client{}).Do(req)
		if err != nil {
			// will retry
			return false, nil
		}

		defer resp.Body.Close()
		respByte, _ := io.ReadAll(resp.Body)
		err = json.Unmarshal(respByte, &authResp)
		if err != nil {
			// will retry
			return false, nil
		}
		return true, nil
	})

	if len(authResp) == 0 {
		return ""
	}

	return authResp[0].Jwt
}

func (a AuthApi) GetLogAuthJwt(logNamespace string, logUuid string) string {
	cabs := []LogJwtRequest{
		{
			Uuid:       logUuid,
			ExpireHour: 24 * 7,
		},
	}
	body, err := json.Marshal(cabs)
	if err != nil {
		return ""
	}

	var jwtResp []LogJwtResponse

	err = util.Backoff(util.DefaultRetry, func() (bool, error) {
		urlStr := fmt.Sprintf("%s/auth/rest/v1/namespaces/%s/log-group-auths", a.Config.Endpoint, logNamespace)
		req, _ := http.NewRequest("POST", urlStr, strings.NewReader(string(body)))
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", a.Config.Token))
		req.Header.Set("Content-Type", "application/json")
		resp, err := (&http.Client{}).Do(req)
		if err != nil {
			// will retry
			return false, nil
		}

		defer resp.Body.Close()

		respByte, _ := io.ReadAll(resp.Body)
		err = json.Unmarshal(respByte, &jwtResp)
		if err != nil {
			// will retry
			return false, nil
		}
		return true, nil
	})

	if jwtResp == nil || len(jwtResp) == 0 {
		return ""
	}

	return jwtResp[0].Jwt

}

func (a AuthApi) GetLogURL(logUuid string, podName string) string {
	return fmt.Sprintf("%s/log/rest/v1/namespaces/log/log-groups/%s/log-content?logName=%s/main",
		a.Config.Endpoint, logUuid, podName)
}

func (a AuthApi) GetICodeSSHKey(username string, repo string) (*SSHKeyResponse, error) {
	jwt := a.GetCodeAuthJwt("ICODE", repo)
	if jwt == "" {
		return nil, fmt.Errorf("get code auth jwt failed for repo %s", repo)
	}

	var authConfig *AuthApiConfig
	if env.RunMode() == env.RunModeRelease {
		authConfig = a.Config
	} else {
		authConfig = a.DevConfig
	}

	codeAuthResponse := &CodeAuthResponse{}
	err := util.Backoff(util.DefaultRetry, func() (bool, error) {
		urlStr := fmt.Sprintf("%s/auth/rest/v2/thirdparty/code/sshKey?namespace=%s&username=%s&repo=%s&repoType=ICODE",
			authConfig.Endpoint, authConfig.Namespace, username, url.QueryEscape(repo))
		req, _ := http.NewRequest("GET", urlStr, nil)
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", jwt))
		resp, err := (&http.Client{}).Do(req)
		if err != nil {
			return false, nil
		}

		defer resp.Body.Close()
		respByte, _ := io.ReadAll(resp.Body)
		err = json.Unmarshal(respByte, codeAuthResponse)
		if err != nil {
			return false, nil
		}
		return true, nil
	})

	if err != nil {
		return nil, err
	}
	if codeAuthResponse == nil {
		return nil, fmt.Errorf("codeAuthResponse is nil")
	}
	if codeAuthResponse.Status != "OK" {
		return nil, fmt.Errorf("codeAuthResponse status is not ok, status: %s, message: %s", codeAuthResponse.Status, codeAuthResponse.Message)
	}

	return &codeAuthResponse.Data, nil
}
