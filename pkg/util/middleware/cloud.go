package middleware

import (
	"context"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util/constant"
	"icode.baidu.com/baidu/gdp/ghttp"
)

const CloudUsernameHeader = "X-BAIDU-INT-USERNAME"

func CloudUsernameMiddlewareFunc() ghttp.MiddleWareFunc {
	return func(ctx context.Context, w ghttp.Writer, req ghttp.Request, next ghttp.MiddleWareQueue) bool {

		username, ok := req.Header(CloudUsernameHeader)
		if !ok || username == "" {
			return false
		}
		ctx = context.WithValue(ctx, constant.CtxLoginUserKey, username)

		next.Next(ctx, w, req)
		return true
	}
}
