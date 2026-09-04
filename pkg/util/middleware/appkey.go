package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"gorm.io/gorm"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/dao"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/authcenter"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/resource"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util/constant"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util/response"
	"icode.baidu.com/baidu/gdp/ghttp"
	"icode.baidu.com/baidu/gdp/logit"
)

// getAccessTokenFromMetadata 从请求体的 metadata 中读取 access-token
func getAccessTokenFromMetadata(ctx context.Context, req ghttp.Request) string {
	httpReq := req.HTTPRequest()

	// 读取并缓存 Body
	bodyBytes, err := io.ReadAll(httpReq.Body)
	if err != nil {
		return ""
	}
	httpReq.Body.Close()

	// 重新设置 Body，让后续的 handler 可以再次读取
	httpReq.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

	// 定义一个通用的请求结构体，只包含 metadata 字段
	request := &struct {
		MetaData map[string]string `json:"metadata"`
	}{}

	// 解析请求体
	if err := json.Unmarshal(bodyBytes, request); err != nil {
		// 解析失败不记录日志，因为可能不是 JSON 请求或不包含 metadata
		return ""
	}

	if request.MetaData == nil {
		return ""
	}

	accessToken, ok := request.MetaData[constant.MetadataAccessToken]
	if !ok {
		return ""
	}

	return accessToken
}

// validateAndGetUsernameFromAccessToken 从请求的 metadata 中获取 access-token 并验证，返回 username
func validateAndGetUsernameFromAccessToken(ctx context.Context, req ghttp.Request) string {
	// 从请求体的 metadata 中读取 access-token
	accessToken := getAccessTokenFromMetadata(ctx, req)
	if accessToken == "" {
		return ""
	}

	resource.LoggerService.Notice(ctx, "attempting to validate access token from metadata",
		logit.String("accessToken", accessToken))

	// 调用 authcenter 验证 token
	authApi := authcenter.GetAuthCenterApi()
	tokenData, err := authApi.ValidateMultiToken(accessToken)
	if err != nil {
		resource.LoggerService.Warning(ctx, "validate access token failed",
			logit.String("accessToken", accessToken),
			logit.String("error", err.Error()))
		return ""
	}

	if tokenData == nil || tokenData.Username == "" {
		return ""
	}

	resource.LoggerService.Notice(ctx, "access token validated successfully",
		logit.String("username", tokenData.Username))

	return tokenData.Username
}

// RawAppKeyMiddlewareFunc 校验appKey,并且设置到山下文中
func RawAppKeyMiddlewareFunc() ghttp.MiddleWareFunc {
	return func(ctx context.Context, w ghttp.Writer, req ghttp.Request, next ghttp.MiddleWareQueue) bool {
		if apiKey, ok := req.Header("X-API-KEY"); ok {
			if apiKey == "" {
				response.NewGResponse(http.StatusForbidden,
					response.NotLoginErr).WriteTo(w)
				return false
			}

			appDao := dao.Use(resource.OrmMain).App
			username := validateAndGetUsernameFromAccessToken(ctx, req)
			platApp, err := appDao.Where(appDao.APIKey.Eq(apiKey), appDao.Type.In("plat", "mcp")).First()
			if username != "" {
				ctx = context.WithValue(ctx, constant.CtxAppIDKey, platApp.ID)
				ctx = context.WithValue(ctx, constant.CtxLoginUserKey, username)
				next.Next(ctx, w, req)
				return true
			} else {
				if err == nil && platApp != nil {
					resource.LoggerService.Notice(ctx, "platform app authenticated",
						logit.String("apiKey", apiKey),
						logit.Int32("appID", platApp.ID))
					ctx = context.WithValue(ctx, constant.CtxAppIDKey, platApp.ID)
					ctx = context.WithValue(ctx, constant.CtxLoginUserKey, "")
					next.Next(ctx, w, req)
					return true
				} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
					resource.LoggerService.Error(ctx, "query platform app failed",
						logit.String("apiKey", apiKey),
						logit.String("error", err.Error()))
				}
			}
			resource.LoggerService.Warning(ctx, "authentication failed",
				logit.String("apiKey", apiKey))
			response.NewGResponse(http.StatusForbidden,
				response.NotLoginErr).WriteTo(w)
			return false
		} else {
			resource.LoggerService.Warning(ctx, "missing API key in request")
			response.NewGResponse(http.StatusForbidden,
				response.NotLoginErr).WriteTo(w)
			return false
		}
	}
}
