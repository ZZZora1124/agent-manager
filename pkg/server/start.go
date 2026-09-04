package server

import (
	"context"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/server/admin"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/server/httpserver"
	"icode.baidu.com/baidu/gdp/boot"
	"icode.baidu.com/baidu/gdp/net/gconn/conndump"
	"icode.baidu.com/baidu/gdp/net/gserver"
	"icode.baidu.com/baidu/gdp/net/gserver/protocols"
	"log"
	"net"
	"time"
)

// Start 启动所有 server
func Start(ctx context.Context) error {
	serverCfg := boot.MustLoadServerConfig("server.toml")

	// 使用同端口多协议能力，即可以在同一个端口，如 8080，上支持 HTTP、 NSHEAD、 PBRPC 等协议
	// https://gdp.baidu-int.com/gdp3/docs/examples/server/25_multi_protocol_server/
	mp := &gserver.MultiProtocolServer{}

	var mainAddr net.Addr

	// HTTP 、NSHead、PBRPC 3种协议的 server 使用同一个端口
	{
		cfg := serverCfg.MustGet("HTTPServer")
		mainAddr = cfg.MustListenAddr()

		// HTTP 协议的 Server
		serverHTTP := wrapServer("HTTPServer", httpserver.NewServer(ctx, cfg))

		mp.Bind(protocols.HTTPProtocol, mainAddr, serverHTTP)
		mp.BindDefault(mainAddr, serverHTTP)
	}

	serverList := gserver.List{
		&gserver.ServerListener{
			Name:   "Main Server",
			Addr:   mainAddr,
			Server: mp,
		},
	}

	// 另外一个独立端口，启动管理相关功能的 HTTP Server
	// 如 稳定性指标 /metrics,/debug/panel/ 等功能 都注册在这里
	// 这个 Server 的端口应该避免对外网暴露，以免产生安全问题
	// 若是部署在 CNAP 上：
	// CNAP 默认是只会暴露出一个端口，需要修改配置以让端口(若主端口是 8080,则此端口是 8081)能在容器外访问
	{
		cfg := serverCfg.MustGet("AdminServer")
		adminAddr := cfg.MustListenAddr()
		serverAdmin := &gserver.ServerListener{
			Name:   "Admin Server",
			Addr:   adminAddr,
			Server: adminserver.NewAdminServer(),
		}
		serverList = append(serverList, serverAdmin)
	}
	log.Println("serverList.StartGraceful ...")
	return serverList.StartGraceful(ctx, 3*time.Second)
}

// enableRPCDump 是否让 Server 集成 RPC Dump 的能力
//
//	集成 gdp/panel 后，可以在 /debug/panel/?tab=rpc_dump 页面进行管理
//	支持将 Server 和 Client 的 Request  和 Response dump 到文件
//	gdp/panel 会在 httpserver/router.go 里进行集成
//	可以使用 gdp reply 命令来回放流量，使用 gdp dumpcat 来查看保存的数据文件
//	可以通过 gdp help dumpcat 命令来查看详细参数和用法
const enableRPCDump = true

func wrapServer(name string, ser gserver.ServeShutdown) gserver.ServeShutdown {
	if !enableRPCDump {
		return ser
	}
	// 集成 rpc dump 能力，之后此 server 可通过界面开启 (UI 界面：/debug/panel/?tab=rpc_dump )
	// 支持将 Request  和 Response dump 到文件里
	return conndump.Default().WrapServer(name, ser)
}
