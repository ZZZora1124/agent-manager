package httpserver

import (
	"context"
	"time"

	"icode.baidu.com/baidu/gdp/boot"
	"icode.baidu.com/baidu/gdp/ghttp"
)

// NewServer 创建一个新的 http server
func NewServer(ctx context.Context, cfg boot.ServerConfig) *ghttp.DefaultServer {
	ser := &ghttp.DefaultServer{
		Ctx:          ctx,
		ReadTimeout:  time.Millisecond * time.Duration(cfg.ReadTimeout),
		WriteTimeout: time.Millisecond * time.Duration(cfg.WriteTimeout),
		IdleTimeout:  time.Millisecond * time.Duration(cfg.IdleTimeout),
		MaxBodyBytes: 0,
		Logger:       nil,
	}

	ser.SetHandler(httpRouter(ser))
	return ser
}
