package httpserver

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/common"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/dao"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/instance"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/card"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/eacauth"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/resource"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util/response"
	"icode.baidu.com/baidu/gdp/ghttp"
	"icode.baidu.com/baidu/gdp/logit"
)

// SandboxAccessHandler 沙箱访问权限验证处理器
type SandboxAccessHandler struct {
	validator *instance.TokenValidator
}

// NewSandboxAccessHandler 创建 SandboxAccessHandler 实例
func NewSandboxAccessHandler() *SandboxAccessHandler {
	return &SandboxAccessHandler{
		validator: instance.NewTokenValidator(*dao.Use(resource.OrmMain)),
	}
}

// ValidateSandboxAccessRequest 验证沙箱访问权限的 HTTP 请求参数
type ValidateSandboxAccessRequest struct {
	SandboxUUID string `json:"sandboxUUID"` // 沙箱唯一标识
	AccessToken string `json:"accessToken"` // 访问令牌
}

// ValidateSandboxAccessResponse 验证沙箱访问权限的 HTTP 响应
type ValidateSandboxAccessResponse struct {
	Code    int    `json:"code"`    // 响应码
	Message string `json:"message"` // 响应消息
	Data    any    `json:"data"`    // 响应数据
}

// ValidateSandboxAccess 验证用户是否有权限访问指定的沙箱
// POST /sandboxes/validate-access
func (h *SandboxAccessHandler) ValidateSandboxAccess(ctx context.Context, r ghttp.Request) ghttp.Response {
	logger := resource.LoggerService

	// 读取请求体
	body, err := io.ReadAll(r.Body())
	if err != nil {
		logger.Error(ctx, "read request body failed", logit.String("error", err.Error()))
		return ghttp.NewJSONResponse(http.StatusBadRequest, ValidateSandboxAccessResponse{
			Code:    http.StatusBadRequest,
			Message: "read request body failed",
			Data:    nil,
		})
	}

	// 解析请求参数
	var req ValidateSandboxAccessRequest
	if err := json.Unmarshal(body, &req); err != nil {
		logger.Error(ctx, "parse request json failed", logit.String("error", err.Error()))
		return ghttp.NewJSONResponse(http.StatusBadRequest, ValidateSandboxAccessResponse{
			Code:    http.StatusBadRequest,
			Message: "invalid json format",
			Data:    nil,
		})
	}

	// 记录请求日志
	logger.Notice(ctx, "received sandbox access validation request",
		logit.String("sandboxUUID", req.SandboxUUID))

	// 调用验证服务
	username, err := h.validator.ValidateSandboxAccess(ctx, req.SandboxUUID, req.AccessToken)
	if err != nil {
		logger.Error(ctx, "validate sandbox access failed",
			logit.String("sandboxUUID", req.SandboxUUID),
			logit.String("error", err.Error()))
		return ghttp.NewJSONResponse(http.StatusForbidden, ValidateSandboxAccessResponse{
			Code:    http.StatusForbidden,
			Message: err.Error(),
			Data:    nil,
		})
	}

	// 验证成功
	logger.Notice(ctx, "sandbox access validation completed successfully",
		logit.String("sandboxUUID", req.SandboxUUID),
		logit.String("username", username))

	return ghttp.NewJSONResponse(http.StatusOK, ValidateSandboxAccessResponse{
		Code:    http.StatusOK,
		Message: "validation successful",
		Data: map[string]string{
			"sandboxUUID": req.SandboxUUID,
			"username":    username,
		},
	})
}

// AccessRequestBody 申请访问他人沙箱的请求体
type AccessRequestBody struct {
	ApplyReason string `json:"applyReason"`
}

// RequestSandboxAccess 申请访问他人沙箱权限，卡片发给沙箱 owner
// POST /rest/v1/sandbox/instances/:sandboxId/access-request
func (h *SandboxAccessHandler) RequestSandboxAccess(ctx context.Context, r ghttp.Request) ghttp.Response {
	logger := resource.LoggerService

	sandboxID := r.Param(":sandboxId")

	apiToken, _ := r.Header("api-token")
	applyUser, _ := r.Header("X-BAIDU-INT-USERNAME")
	if apiToken == "" {
		return ghttp.NewJSONResponse(http.StatusBadRequest, ValidateSandboxAccessResponse{
			Code: http.StatusBadRequest, Message: "api-token is required",
		})
	}

	var body AccessRequestBody
	if err := util.Bind(r, &body); err != nil {
		return response.NewGResponse(http.StatusBadRequest, response.ErrParams)
	}

	q := dao.Use(resource.OrmMain)

	// 查询沙箱，获取 owner 和 app_id
	sandbox, err := q.SandboxRuntime.Where(q.SandboxRuntime.SandboxUUID.Eq(sandboxID)).First()
	if err != nil {
		logger.Error(ctx, "sandbox not found", logit.String("sandboxId", sandboxID), logit.String("error", err.Error()))
		return ghttp.NewJSONResponse(http.StatusNotFound, ValidateSandboxAccessResponse{
			Code: http.StatusNotFound, Message: "sandbox not found",
		})
	}

	// 从 app 表获取 platform
	app, err := q.App.Where(q.App.ID.Eq(sandbox.AppID)).First()
	if err != nil {
		logger.Error(ctx, "app not found", logit.Int32("appId", sandbox.AppID), logit.String("error", err.Error()))
		return ghttp.NewJSONResponse(http.StatusInternalServerError, ValidateSandboxAccessResponse{
			Code: http.StatusInternalServerError, Message: "app not found",
		})
	}

	owner := sandbox.CreateUser
	logger.Notice(ctx, "sending access request card",
		logit.String("sandboxId", sandboxID),
		logit.String("owner", owner),
		logit.String("applyUser", applyUser))

	const templateID = "9ce3571bbbdf466995f6a59ed06a5403"

	userData := card.NewAccessApplyCardUserData(applyUser, sandboxID, app.Platform, body.ApplyReason)
	client := card.NewSendCardClientFromConfig()
	if err := client.SendCardMsgByUser(owner, apiToken, templateID, userData, sandboxID, applyUser, "沙箱访问权限申请"); err != nil {
		logger.Error(ctx, "send access request card failed", logit.String("error", err.Error()))
		return ghttp.NewJSONResponse(http.StatusInternalServerError, ValidateSandboxAccessResponse{
			Code: http.StatusInternalServerError, Message: err.Error(),
		})
	}

	return ghttp.NewJSONResponse(http.StatusOK, ValidateSandboxAccessResponse{
		Code:    http.StatusOK,
		Message: "access request sent successfully",
		Data:    map[string]string{"sandboxId": sandboxID, "owner": owner},
	})
}

// AccessRequestCallback 沙箱访问权限申请卡片点击回调
// POST /rest/v1/sandbox/instances/access-request/callback
func (h *SandboxAccessHandler) AccessRequestCallback(ctx context.Context, req ghttp.Request) ghttp.Response {
	if echoStr, _ := req.Query("echostr"); echoStr != "" {
		return ghttp.NewTextResponse(http.StatusOK, []byte(echoStr))
	}

	appKey := req.PostFormDefault("app_key", "")
	eventID := req.PostFormDefault("event_id", "")
	userID := req.PostFormDefault("user_id", "")
	encryptedMsgJSON := req.PostFormDefault("msg_json", "")
	logID, _ := req.Header("logid")
	if logID == "" {
		logID, _ = req.Header("LOGID")
	}

	resource.LoggerService.Notice(ctx, "access request callback",
		logit.String("logID", logID),
		logit.String("appKey", appKey),
		logit.String("eventID", eventID),
		logit.String("userID", userID))

	aesKey := eacauth.GetEacAuthConfig().CardCallbackAESKey
	handler := card.NewCallbackHandler(aesKey, nil)

	approveStatus := "已拒绝"

	if encryptedMsgJSON != "" {
		decrypted, err := handler.DecryptMsgJSON(encryptedMsgJSON)
		if err != nil {
			resource.LoggerService.Error(ctx, "decrypt msg_json failed", logit.String("error", err.Error()))
		} else {
			var msgJSON card.CallbackMsgJSON
			if err := json.Unmarshal([]byte(decrypted), &msgJSON); err != nil {
				resource.LoggerService.Error(ctx, "parse msg_json failed", logit.String("error", err.Error()))
			} else {
				switch msgJSON.InteractivityID {
				case "agree_click":
					approveStatus = "已通过"
					var appCtx map[string]any
					if err := json.Unmarshal([]byte(msgJSON.AppContext), &appCtx); err == nil {
						sandboxID, _ := appCtx["sandboxID"].(string)
						applyUser, _ := appCtx["applyUser"].(string)
						if userID == applyUser {
							resource.LoggerService.Error(ctx, "user cannot approve their own request",
								logit.String("userID", userID), logit.String("applyUser", applyUser))
							callbackResp, err := handler.BuildFailureResponse(ctx, "不能审批自己的申请")
							if err != nil {
								return response.NewGResponse(http.StatusInternalServerError,
									response.New(err.Error(), http.StatusInternalServerError))
							}
							return response.NewGResponse(http.StatusOK, callbackResp)
						}
						if sandboxID != "" && applyUser != "" {
							if err := common.SetSandboxGrantCache(ctx, resource.RedisClient, sandboxID, applyUser); err != nil {
								resource.LoggerService.Error(ctx, "set sandbox grant cache failed",
									logit.String("sandboxID", sandboxID), logit.String("applyUser", applyUser), logit.String("error", err.Error()))
							} else {
								resource.LoggerService.Notice(ctx, "sandbox grant cache set",
									logit.String("sandboxID", sandboxID), logit.String("applyUser", applyUser))
							}
							go asyncUpdateCard(msgJSON.OAuthCode, sandboxID, applyUser, "已通过")
						}
					}
				case "reject_click":
					approveStatus = "已拒绝"
					var appCtx map[string]any
					if err := json.Unmarshal([]byte(msgJSON.AppContext), &appCtx); err == nil {
						sandboxID, _ := appCtx["sandboxID"].(string)
						applyUser, _ := appCtx["applyUser"].(string)
						if sandboxID != "" && applyUser != "" {
							go asyncUpdateCard(msgJSON.OAuthCode, sandboxID, applyUser, "已拒绝")
						}
					}
				}
			}
		}
	}

	data := map[string]interface{}{
		"showAgreeButton":       "0",
		"showRejectButton":      "0",
		"showApplyResultButton": "1",
		"approveStatus":         approveStatus,
	}
	callbackResp, err := handler.BuildCallbackResponse(data, "操作成功")
	if err != nil {
		resource.LoggerService.Error(ctx, "build callback response failed", logit.String("error", err.Error()))
		return response.NewGResponse(http.StatusInternalServerError,
			response.New(err.Error(), http.StatusInternalServerError))
	}

	return response.NewGResponse(http.StatusOK, callbackResp)
}

// asyncUpdateCard 异步更新申请卡片状态
func asyncUpdateCard(oauthCode, sandboxID, applyUser, approveStatus string) {
	ctx := context.Background()
	record, err := common.GetApplyCardRecord(ctx, resource.RedisClient, sandboxID, applyUser)
	if err != nil || record == nil {
		resource.LoggerService.Error(ctx, "get apply card record failed",
			logit.String("sandboxID", sandboxID), logit.String("applyUser", applyUser))
		return
	}
	cardClient := card.NewSendCardClientFromConfig()
	newUserData := &card.AccessApplyCardUserData{
		ShowAgreeButton:       "0",
		ShowRejectButton:      "0",
		ShowApplyResultButton: "1",
		ApproveStatus:         approveStatus,
	}
	if err := cardClient.UpdateCardByOAuthCode(ctx, oauthCode, record.ModifyToken, applyUser, newUserData); err != nil {
		resource.LoggerService.Error(ctx, "update card failed",
			logit.String("sandboxID", sandboxID), logit.String("error", err.Error()))
	}
}
