package response

import (
	"icode.baidu.com/baidu/gdp/ghttp"
)

// NewGResponse 返回包含成功响应的ghttp.JSONResponse
//
//	Param:
//
//	    data  any	响应数据
//	Return:
//		Response	Response对象
func NewGResponse(code int, data any) *ghttp.JSONResponse {
	return ghttp.NewJSONResponse(code, data)
}
