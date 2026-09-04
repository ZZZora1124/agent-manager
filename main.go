package main

import (
	"flag"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/bootstrap"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/server"
	"icode.baidu.com/baidu/gdp/boot"
	"icode.baidu.com/baidu/gdp/net/gserver"

	"log"
)

var appConfig = flag.String("conf", "./conf/app.toml", "app config file")

func main() {

	flag.Parse()
	boot.MustLoadAppConfig(*appConfig)

	ctx, shutdown := gserver.GraceContext()
	// shutdown 执行的时候，会自动执行由 gtask.BeforeShutdown 注册的回调方法
	defer shutdown()

	bootstrap.MustInit(ctx)

	err := server.Start(ctx)

	shutdown() // 由于 log.Fatalln 会调用 os.Exit，所以在此之前需要主动调用此方法

	log.Fatalln("server exit:", err)
}
