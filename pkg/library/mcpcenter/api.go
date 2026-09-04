package mcpcenter

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/authcenter"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/resource"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util"
	"icode.baidu.com/baidu/gdp/conf"
	"icode.baidu.com/baidu/gdp/env"
	"icode.baidu.com/baidu/gdp/logit"
	"io"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type Api struct {
	Config *ApiConfig
}

var mcpCenterApi *Api = nil
var mcpCenterConfig *ApiConfig = nil

func GetMcpCenterApi() *Api {

	if mcpCenterConfig == nil {
		config := &ApiConfig{}
		err := conf.Parse("mcp_center.toml", config)
		if err != nil {
			panic("parse magellan auth config error")
		}
		mcpCenterConfig = config
	}
	if mcpCenterApi == nil {
		mcpCenterApi = &Api{
			Config: mcpCenterConfig,
		}
	}
	return mcpCenterApi
}

func (a Api) UserHasSpacePermission(workspaceId int, username string) bool {
	if workspaceId < 0 || username == "" {
		return false
	}
	var validateRes ValidateResponse
	err := util.Backoff(util.DefaultRetry, func() (bool, error) {

		urlStr := fmt.Sprintf("%s/rest/v1/workspaces/%d/permission", a.Config.Endpoint, workspaceId)
		req, _ := http.NewRequest("GET", urlStr, nil)
		// TODO use openApi and ak/sk
		req.Header.Set("X-BAIDU-INT-USERNAME", username)
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
		return false
	}
	if validateRes.Code != 200 {
		return false
	}
	if validateRes.Data == nil {
		return false
	}
	if validateRes.Data.Admins == nil {
		return false
	}

	for _, adminUser := range validateRes.Data.Admins {
		if adminUser == username {
			return true
		}
	}
	return false
}

func (a Api) GetWorkspaceAdmins(workspaceId int, username string) ([]string, error) {
	if workspaceId <= 0 || username == "" {
		return nil, fmt.Errorf("workspaceId or username is empty")
	}

	var validateRes ValidateResponse
	err := util.Backoff(util.DefaultRetry, func() (bool, error) {
		urlStr := fmt.Sprintf("%s/rest/v1/workspaces/%d/permission", a.Config.Endpoint, workspaceId)
		req, _ := http.NewRequest("GET", urlStr, nil)
		req.Header.Set("X-BAIDU-INT-USERNAME", username)
		resp, err := (&http.Client{}).Do(req)
		if err != nil {
			return false, nil
		}
		defer resp.Body.Close()

		respByte, _ := io.ReadAll(resp.Body)
		err = json.Unmarshal(respByte, &validateRes)
		if err != nil {
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		return nil, fmt.Errorf("request workspace permission failed")
	}
	if validateRes.Code != 200 || validateRes.Data == nil {
		return nil, fmt.Errorf("workspace permission response invalid")
	}
	return validateRes.Data.Admins, nil
}

func (a Api) GetWorkspaceInfo(workspaceId int, username string) (bool, *WorkspaceResponse) {
	// 查询空间对应的资源关系
	var workInfoRes WorkspaceInfoResponse
	err := util.Backoff(util.DefaultRetry, func() (bool, error) {

		urlStr := fmt.Sprintf("%s/rest/v1/workspaces/%d", a.Config.Endpoint, workspaceId)
		req, _ := http.NewRequest("GET", urlStr, nil)
		// TODO use openApi and ak/sk
		req.Header.Set("X-BAIDU-INT-USERNAME", username)
		resp, err := (&http.Client{}).Do(req)
		if err != nil {
			return false, err
		}
		defer resp.Body.Close()

		respByte, _ := io.ReadAll(resp.Body)
		err = json.Unmarshal(respByte, &workInfoRes)
		if err != nil {
			return false, err
		}
		return true, nil
	})

	if err != nil {
		return false, nil
	}
	if workInfoRes.Code != 200 {
		return false, nil
	}
	if workInfoRes.Data == nil {
		return false, nil
	}
	return true, workInfoRes.Data
}

func (a Api) GetWorkspaceRole(workspaceId int, username string) (bool, string) {
	if workspaceId < 0 || username == "" {
		return false, "空间Id或用户名不能为空"
	}
	var validateRes UserPermissionRoleResponse
	err := util.Backoff(util.DefaultRetry, func() (bool, error) {
		urlStr := fmt.Sprintf("%s/api/permission/user?workspaceId=%d&username=%s", a.Config.Endpoint, workspaceId, username)
		req, _ := http.NewRequest("GET", urlStr, nil)
		// TODO use openApi and ak/sk
		req.Header.Set("X-BAIDU-INT-USERNAME", username)
		req.Header.Set("x-auth-client-id", username)
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
		return false, "获取用户角色失败, 请重试"
	}
	if validateRes.Code != 200 {
		return false, "获取用户角色失败, 请重试"
	}
	if validateRes.Data == nil {
		return false, "当前用户不属于该空间，请联系管理员"
	}
	return true, validateRes.Data.Role
}

func getHref(workspaceID int) string {
	if env.RunMode() == "release" {
		return fmt.Sprintf("https://console.cloud.baidu-int.com/onetool/space/%d?type=sandbox", workspaceID)
	}
	return fmt.Sprintf("http://console.cloud-sandbox.baidu-int.com/onetool/space/%d?type=sandbox", workspaceID)

}

func (a Api) UseAIRobot(username string, title string, name string, templateName string, status string, workspaceId int) (bool, string) {
	logger := resource.LoggerService
	clientID := "7f4182c3-9d61-4e05-bf2a-8c91a6312d54"
	clientSecret := "a4e59c2f-8b3d-4f6a-9e1c-3d8b7f5a2c6e"
	endpoint := "http://10.11.172.107:8001/agentic-infra/api/v1/notice/infoflow/text"
	timestamp := fmt.Sprintf("%d", time.Now().UnixNano()/1e6) // 毫秒

	raw := clientID + clientSecret + timestamp
	h := md5.New()
	io.WriteString(h, raw)
	sign := hex.EncodeToString(h.Sum(nil))
	var logID = rand.Int63()
	var textMsgResponse TextMsgResponse
	href := getHref(workspaceId)
	richText := RichText{
		Content: []RichBlock{
			{Type: "text", Text: "【通知】：" + title + "\n"},
			{Type: "text", Text: "实例名称：" + name + "\n"},
			{Type: "text", Text: "模版名称：" + templateName + "\n"},
			{Type: "text", Text: "状态说明：" + status + "\n"},
			{Type: "text", Text: "你可进入对应空间查看实例详情并处理：" + href + "\n"},
			{Type: "text", Text: "如需进一步协助，请联系值班人员提交问题：" + "https://zhiban.baidu-int.com/detail?id=6328a9bee8af4bf8842ed3e4d0086a32" + "\n"},
		},
	}
	err := util.Backoff(util.DefaultRetry, func() (bool, error) {
		body := TextMsg{
			LogID:    logID,
			ToUser:   username,
			MsgType:  "richtext",
			Richtext: richText,
		}
		jsonBytes := mustJSON(body)
		req, _ := http.NewRequest("POST", endpoint, bytes.NewReader(jsonBytes))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-auth-client-id", clientID)
		req.Header.Set("x-auth-timestamp", timestamp)
		req.Header.Set("x-auth-sign", sign)
		resp, err := (&http.Client{}).Do(req)
		if err != nil {
			// will retry
			return false, nil
		}

		defer resp.Body.Close()

		respByte, _ := io.ReadAll(resp.Body)
		err = json.Unmarshal(respByte, &textMsgResponse)
		if err != nil {
			// will retry
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		logger.Error(context.Background(),
			fmt.Sprintf("logID=%d code=%d message=%s requestId=%s err=%v",
				logID,
				textMsgResponse.Code,
				textMsgResponse.Message,
				textMsgResponse.RequestID,
				err),
		)
		return false, "网络错误, 请重试"
	}
	logger.Notice(context.Background(),
		fmt.Sprintf("logID=%d code=%d message=%s requestId=%s",
			logID,
			textMsgResponse.Code,
			textMsgResponse.Message,
			textMsgResponse.RequestID),
	)
	if textMsgResponse.Code != 200 {
		return false, "通知用户失败, 请重试"
	}
	return true, textMsgResponse.Message
}

func (a Api) GetToken(token string) (bool, string) {
	var validateRes TokenMsgResponse
	clientID := authcenter.GetAuthCenterApi().Config.ClientID
	secretKey := authcenter.GetAuthCenterApi().Config.ClientSecret
	endpoint := authcenter.GetAuthCenterApi().Config.Endpoint
	timestamp := fmt.Sprintf("%d", time.Now().UnixNano()/1e6)
	random := strconv.Itoa(rand.Intn(1_000_000))
	sign := GenerateSign(clientID, random, timestamp, secretKey, token)
	err := util.Backoff(util.DefaultRetry, func() (bool, error) {
		body := TokenMsg{
			Identity:  token,
			ClientId:  clientID,
			Timestamp: timestamp,
			Random:    random,
			Sign:      sign,
		}
		jsonBytes := mustJSON(body)
		urlStr := fmt.Sprintf("%s/api/rest/v1/auth/user_auth_token", endpoint)
		req, _ := http.NewRequest("POST", urlStr, bytes.NewReader(jsonBytes))
		xRequestID := hex.EncodeToString((func() []byte { b := make([]byte, 16); rand.Read(b); return b }()))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Request-ID", xRequestID)
		resp, err := (&http.Client{}).Do(req)
		fmt.Println(resp)
		if err != nil {
			return false, nil
		}

		defer resp.Body.Close()

		respByte, _ := io.ReadAll(resp.Body)
		fmt.Printf("raw=%s\n", string(respByte))
		err = json.Unmarshal(respByte, &validateRes)
		if err != nil {
			// will retry
			return false, nil
		}
		return true, nil
	})

	if err != nil || validateRes.Code != 200 || validateRes.Data == nil {
		logger := resource.LoggerService
		logger.Error(context.Background(),
			time.Now().Format("2006-01-02 15:04:05"),
			logit.String("response", fmt.Sprintf("%+v", validateRes)))
		return false, "预装Zulu与Ducc请求认证失败, 请重试"
	}
	return true, validateRes.Data.AccessToken
}

func mustJSON(v interface{}) json.RawMessage {
	b, _ := json.Marshal(v)
	return json.RawMessage(b)
}

// GenerateSign
func GenerateSign(clientID, random, timestamp, secretKey, identity string) string {
	raw := "client_id=" + clientID + "&random=" + random + "&timestamp=" + timestamp +
		"&secretKey=" + secretKey + "&identity=" + identity

	h := sha256.Sum256([]byte(raw))
	return strings.ToLower(fmt.Sprintf("%x", h)) // 小写十六进制
}
