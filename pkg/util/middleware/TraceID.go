package middleware

import (
	"context"
	"github.com/google/uuid"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util/constant"
	"icode.baidu.com/baidu/gdp/ghttp"
	"icode.baidu.com/baidu/gdp/logit"
)

func TraceIDMiddlewareFunc() ghttp.MiddleWareFunc {
	return func(ctx context.Context, w ghttp.Writer, req ghttp.Request, next ghttp.MiddleWareQueue) bool {
		if traceID, ok := req.Header("X-Trace-Id"); ok && len(traceID) > 0 {
			logit.AddMetaFields(ctx, logit.String(constant.CtxTraceIDKey, traceID))
			next.Next(ctx, w, req)
			return true
		} else {
			logit.AddMetaFields(ctx, logit.String(constant.CtxTraceIDKey, uuid.New().String()))
			next.Next(ctx, w, req)
			return true
		}
	}
}
