package util

import (
	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"icode.baidu.com/baidu/gdp/ghttp"
)

func Bind(req ghttp.Request, arg any) error {
	c := gin.Context{
		Request: req.HTTPRequest(),
	}
	return c.ShouldBindWith(arg, binding.Default(req.HTTPRequest().Method, c.ContentType()))
}
