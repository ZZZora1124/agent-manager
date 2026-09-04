package resource

import (
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/k8s"

	"icode.baidu.com/baidu/gdp/gorm_adapter"
	"icode.baidu.com/baidu/gdp/logit"
	"icode.baidu.com/baidu/gdp/mysql"
	"icode.baidu.com/baidu/gdp/redis"
)

// LoggerService 业务访问日志(HTTP Server access_log)：log/background/background.log
//
//	每个 HTTP 请求打印一条 Notice 日志和 N (N >= 0 )条其他（如 Warning）日志
//	Notice 日志有 HTTP 日志中间件打印，业务自己一定不可以使用此 Logger 打印 Notice 日志
//	否则，采用日志统计 PV 数据会不准确。
//	若是 打印 Warning、Fatal 级别的日志，可以运行打印。
//	若是要打印自己的日志，建议定义单独的一个全新的 logger
var LoggerService logit.Logger

var MySQLClient mysql.Client

// OrmMain 是用于查询mysql库的ORM单例
var OrmMain *gorm_adapter.GormDB

var ClusterManager *k8s.ClusterManager

var RedisClient redis.Client

var InitDone = make(chan struct{})

// PoolReplenishChan 预热池补充触发通道（接收templateID）
var PoolReplenishChan = make(chan int32, 100)

// PoolChangeChan 预热变更触发通道（接收templateID）
var PoolChangeChan = make(chan int32, 100)

// FeatureConfig 功能开关配置，从 app.toml [Feature] 段加载。
type FeatureConfig struct {
	// LazyMigrationToCFS 全量开关：沙箱释放时自动将 local-path PVC 迁移到 CFS
	LazyMigrationToCFS bool `toml:"LazyMigrationToCFS"`
}

// Feature 全局功能开关实例，bootstrap 时初始化。
var Feature = &FeatureConfig{}
