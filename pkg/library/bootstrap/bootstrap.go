package bootstrap

import (
	"context"
	"encoding/base64"

	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/common"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/dao"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/agfs"
	_ "icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/background"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/k8s"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/resource"

	"log"
	"time"

	"icode.baidu.com/baidu/gdp/conf"
	"icode.baidu.com/baidu/gdp/extension/gtask"
	"icode.baidu.com/baidu/gdp/gorm_adapter"
	"icode.baidu.com/baidu/gdp/logit"
	"icode.baidu.com/baidu/gdp/metrics/gmetrics"
	"icode.baidu.com/baidu/gdp/mysql"
	"icode.baidu.com/baidu/gdp/net/gconn/conndump"
	"icode.baidu.com/baidu/gdp/net/netinit"
	"icode.baidu.com/baidu/gdp/redis"
)

const enableRPCDump = true
const (
	sandboxCheckInterval  = 15 * time.Second // 沙箱过期检查间隔
	sandboxExpireDuration = 24 * time.Hour   // 沙箱默认过期时间
)

func init() {
	log.SetFlags(log.LstdFlags | log.Lshortfile | log.Lmicroseconds)
}

// MustInit 组件初始化，若失败，会panic
func MustInit(ctx context.Context) {
	// 初始化指标统计组件
	// 会自动的给 RAL、MySQL、Redis 等 RPC Client 注册用于采集指标的 Interceptor
	// 详见 https://gdp.baidu-int.com/api/baidu/gdp/metrics/gmetrics/
	gmetrics.Default().Init()
	// 让  RPC Client 具备 RPC Dump 的能力
	// 集成 gdp/panel 后，可以在 /debug/panel/rpc_dump 页面进行管理
	// gdp/panel 会在 httpserver/router.go 里进行集成
	if enableRPCDump {
		conndump.Default().Inject()
	}
	initLoggers(ctx)
	netinit.MustContext(ctx)

	initMySQL(ctx)
	initGORM()

	initRedis(ctx)

	//res, _ := dao.Use(resource.OrmMain).App.Find()

	initCluster()
	initFeatureConfig()
	initAgfsConfig()

	close(resource.InitDone)
}

// initLoggers 初始化 logger
// https://gdp.baidu-int.com/gdp3/docs/examples/foundation/12_log/
func initLoggers(ctx context.Context) {
	newLogger := func(confName string) logit.Logger {
		lg, err := logit.NewLogger(ctx, logit.OptConfigFile("logit/"+confName))
		if err != nil {
			panic(err)
		}
		gtask.BeforeShutdown(lg) // 注册 closer,这样可以保证程序进程退出前，日志全部落盘
		return lg
	}

	resource.LoggerService = newLogger("service.toml")

	gtask.BeforeShutdown(func() {
		// PanicLogger 可能会被重置，所以这里用闭包方式
		_ = logit.TryCloseLogger(logit.PanicLogger)
	})
}

// initMySQL 初始化 MySQL Client
// https://gdp.baidu-int.com/gdp3/docs/examples/client/32_mysql/
func initMySQL(_ context.Context) {
	// client 初始化为单例，放到 resource 里去
	resource.MySQLClient = mustInitOneMySQL("mysql_main")
}

func mustInitOneMySQL(name any) mysql.Client {
	// MustNewClient 默认已集成 打印 ral-worker.log 的功能
	opts := []mysql.ClientOption{
		// 其他配置选项
	}
	return mysql.MustNewClient(name, opts...)
}

// 初始化 GORM
func initGORM() {
	var err error
	resource.OrmMain, err = gorm_adapter.NewGorm(resource.MySQLClient)
	if err != nil {
		panic(err.Error())
	}

}

func initCluster() {
	clusterDao := dao.Use(resource.OrmMain).Cluster
	clusters, err := clusterDao.Find()
	if err != nil {
		panic(err)
	}
	k8sClusters := make(map[string]*k8s.Cluster)
	for _, cluster := range clusters {
		decodedBytes, err := base64.StdEncoding.DecodeString(cluster.KubeConfig)
		if err != nil {
			continue
		}
		k8sCluster := k8s.NewCluster(string(decodedBytes), cluster.Namespace, cluster.NetworkType, cluster.NginxEndpoint, resource.RedisClient)
		k8sClusters[cluster.Name] = k8sCluster
	}
	resource.ClusterManager = &k8s.ClusterManager{
		Clusters: k8sClusters,
	}
}

func initRedis(_ context.Context) {
	// client 初始化为单例，放到 resource 里去
	cliOptList := []redis.ClientOption{
		redis.OptHooker(redis.NewLogHook()),
	}

	var err error
	resource.RedisClient, err = redis.NewClient("redis", cliOptList...)
	if err != nil {
		panic(err)
	}

	// 设置 common 包的 RedisClient，用于白名单等通用功能
	common.SetRedisClient(resource.RedisClient)
}

// appConfigWrapper 用于从 app.toml 中解析 [Feature] section
type appConfigWrapper struct {
	Feature resource.FeatureConfig `toml:"Feature"`
}

// initFeatureConfig 从 app.toml 加载功能开关配置
func initFeatureConfig() {
	var wrapper appConfigWrapper
	if err := conf.Parse("app.toml", &wrapper); err != nil {
		log.Printf("[WARN] parse feature config from app.toml failed: %v, using defaults", err)
		return
	}
	*resource.Feature = wrapper.Feature
	log.Printf("[INFO] feature config loaded: LazyMigrationToCFS=%v", resource.Feature.LazyMigrationToCFS)
}

// initAgfsConfig 从 agfs.toml 加载 AgFS 配置
func initAgfsConfig() {
	config := &agfs.Config{}
	if err := conf.Parse("agfs.toml", config); err != nil {
		panic("parse agfs config error: " + err.Error())
	}
	agfs.SetAgfsConfig(config)
}
