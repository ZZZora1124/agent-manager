package httpserver

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/resource"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/service"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util/middleware"
	"icode.baidu.com/baidu/gdp/ghttp"
	"icode.baidu.com/baidu/gdp/metrics/gmetrics"
)

// httpRouter 获取 HTTP Server 的路由信息
// 若是管理功能接口，请注册到 admin.go 的 adminRouter 里去
//
// HTTP Server 和 Router 的文档：
// https://gdp.baidu-int.com/gdp3/docs/examples/server/20_http_server/
func httpRouter(ser *ghttp.DefaultServer) ghttp.Router {
	logger := resource.LoggerService

	// 若 HTTP Server 内部出现异常，将通过此 logger 打印日志
	ghttp.DefaultLogger = logger

	router := ghttp.NewRouter()
	router.SetLogger(logger)

	logMiddleware := ghttp.NewLogMiddleWareFunc(logger, ghttp.DefaultServerLogFields)

	// 需要跳过访问日志的路径
	skipLogPaths := map[string]bool{
		"/health":              true,
		"/sandboxes/owner":     true,
		"/sandboxes/fuseOwner": true,
		// 在这里加入你想要跳过的高流量接口路径
	}

	// 注册 panic Recover 中间件，可以处理 handlerFunc 里出现的 panic
	// 避免程序整体 panic
	// 需要注意的是，若是使用 go xxx() 自己新开启的协程，是不能 recover 的
	router.Use(ghttp.NewRecoverMiddleWareFunc(logger, nil))

	// 注册日志中间件，初始化日志功能，打印访问日志( access_log )
	//  当前 logger 是使用配置文件 conf/logit/service.toml 初始化生成的
	//  日志文件打印到了 {gdp.LogDir}/service/service.log
	//  若需要对日志字段进行调整，请修改这里:
	//  可以使用自定义的 LogFields，也可以对 ghttp.DefaultServerLogFields 进行调整
	router.Use(func(ctx context.Context, w ghttp.Writer, req ghttp.Request, next ghttp.MiddleWareQueue) bool {
		if skipLogPaths[req.HTTPRequest().URL.Path] {
			// 跳过日志，直接进入后续中间件
			return next.Next(ctx, w, req)
		}
		return logMiddleware(ctx, w, req, next)
	})
	// 注册采集 HTTP 接口指标的中间件
	// 详见 https://gdp.baidu-int.com/api/baidu/gdp/metrics/gmetrics/
	router.Use(gmetrics.Default().CollectHTTPServer())

	if ser.WriteTimeout > 0 {
		// 若 server 有设置 WriteTimeout，则整体加上超时控制
		// 这样：
		// 1.日志中可以打印出由于 server 超时导致的504异常
		// 2.业务逻辑可以更及时的终止运行
		router.Use(ghttp.NewTimeoutMiddleWareFunc(ser.WriteTimeout, nil))
	}
	// 添加 TraceID 中间件
	router.Use(middleware.TraceIDMiddlewareFunc())
	// 命令执行相关路由
	router.HandleFunc("POST", "/sandboxes", handleCreateSandbox, middleware.RawAppKeyMiddlewareFunc())
	router.HandleFunc("GET", "/sandboxes", handleListSandbox, middleware.RawAppKeyMiddlewareFunc())
	router.HandleFunc("DELETE", "/sandboxes/:sandboxID", handleDeleteSandbox, middleware.RawAppKeyMiddlewareFunc())
	router.HandleFunc("GET", "/sandboxes/:sandboxID", handleGetSandbox, middleware.RawAppKeyMiddlewareFunc())
	router.HandleFunc("POST", "/sandboxes/:sandboxID/timeout", handleSetSandboxTimeout, middleware.RawAppKeyMiddlewareFunc())
	router.HandleFunc("POST", "/sandboxes/:sandboxID/restore", handleRestoreSandbox, middleware.RawAppKeyMiddlewareFunc())
	router.HandleFunc("DELETE", "/sandboxes/:sandboxID/pvc", handleReleaseSandboxPVC, middleware.RawAppKeyMiddlewareFunc())

	// 定时任务相关路由 (API Key 认证)
	router.HandleFunc("POST", "/sandboxes/:sandboxUuid/scheduled-tasks", handleCreateScheduledTask, middleware.RawAppKeyMiddlewareFunc())
	router.HandleFunc("GET", "/sandboxes/:sandboxUuid/scheduled-tasks", handleGetScheduledTasks, middleware.RawAppKeyMiddlewareFunc())
	router.HandleFunc("DELETE", "/sandboxes/:sandboxUuid/scheduled-tasks", handleDeleteScheduledTasksBySandbox, middleware.RawAppKeyMiddlewareFunc())
	router.HandleFunc("DELETE", "/sandboxes/scheduled-tasks/:taskID", handleDeleteScheduledTask, middleware.RawAppKeyMiddlewareFunc())

	// E2B SDK 迁移接口
	router.HandleFunc("POST", "/sandboxes/:sandboxID/migrate", handleMigrateSandbox, middleware.RawAppKeyMiddlewareFunc())
	router.HandleFunc("GET", "/sandboxes/:sandboxID/migrate/status", handleMigrateStatus, middleware.RawAppKeyMiddlewareFunc())

	router.HandleFunc("POST", "/artifacts/upload", uploadArtifactHandler)

	router.HandleFunc("POST", "/artifacts/download", downloadArtifactHandler)

	// 镜像预热相关路由
	router.HandleFunc("POST", "/preheat/jobs", handleCreatePreheatJob)

	router.HandleFunc("POST", "/pv/binding", handlePvBinding, middleware.RawAppKeyMiddlewareFunc())

	// app PVC实例管理
	pvcInstanceSvc := service.NewAppPvcInstanceService()
	pvcInstanceHandler := NewAppPvcInstanceHandler(pvcInstanceSvc)
	router.HandleFunc("POST", "/pvc/instances", pvcInstanceHandler.CreateAppPvcInstance, middleware.RawAppKeyMiddlewareFunc())
	router.HandleFunc("DELETE", "/pvc/instances/:instanceId", pvcInstanceHandler.DeleteAppPvcInstance, middleware.RawAppKeyMiddlewareFunc())
	router.HandleFunc("GET", "/pvc/instances", pvcInstanceHandler.ListAppPvcInstances, middleware.RawAppKeyMiddlewareFunc())

	router.HandleFunc(http.MethodGet, "/health", func(ctx context.Context, req ghttp.Request) ghttp.Response {
		return ghttp.NewTextResponse(200, []byte("OK"))
	})

	// Prometheus metrics 接口
	router.HandleFunc(http.MethodGet, "/metrics", func(ctx context.Context, req ghttp.Request) ghttp.Response {
		// 创建一个 httptest.ResponseRecorder 来捕获 promhttp.Handler 的输出
		recorder := httptest.NewRecorder()

		// 构建标准的 http.Request
		httpReq, _ := http.NewRequestWithContext(ctx, http.MethodGet, "/metrics", nil)

		// 调用 promhttp.Handler
		promhttp.Handler().ServeHTTP(recorder, httpReq)

		// 读取响应体
		body, _ := io.ReadAll(recorder.Body)

		// 返回 text/plain 格式的响应（Prometheus metrics 格式）
		return ghttp.NewTextResponse(http.StatusOK, body)
	})

	// 沙箱应用创建相关路由
	svc := service.NewTemplateService()
	h := NewTemplateHandler(svc)
	router.HandleFunc("POST", "/rest/v1/sandbox/templates", h.CreateTemplate, middleware.CloudUsernameMiddlewareFunc())
	router.HandleFunc("DELETE", "/rest/v1/sandbox/templates/:templateId", h.DeleteTemplate, middleware.CloudUsernameMiddlewareFunc())
	router.HandleFunc("GET", "/rest/v1/sandbox/templates/:templateId", h.SelectTemplate, middleware.CloudUsernameMiddlewareFunc())
	router.HandleFunc("GET", "/rest/v1/sandbox/templates", h.SelectTemplateAll, middleware.CloudUsernameMiddlewareFunc())
	router.HandleFunc("GET", "/rest/v1/sandbox/common/template", h.GetCommonTemplate, middleware.CloudUsernameMiddlewareFunc())
	router.HandleFunc("PUT", "/rest/v1/sandbox/templates", h.UpdateTemplate, middleware.CloudUsernameMiddlewareFunc())
	// 沙箱实例相关路由
	sandbox := service.NewSandboxRuntimeService()
	sand := NewSandboxHandler(sandbox)
	router.HandleFunc("POST", "/rest/v1/sandbox/instances", sand.CreateSandboxRuntime, middleware.CloudUsernameMiddlewareFunc())
	router.HandleFunc("DELETE", "/rest/v1/sandbox/instances/:sandboxId", sand.ReleaseSandboxRuntime, middleware.CloudUsernameMiddlewareFunc())
	router.HandleFunc("GET", "/rest/v1/sandbox/instances/:sandboxId", sand.SelectSandboxRuntime, middleware.CloudUsernameMiddlewareFunc())
	router.HandleFunc("GET", "/rest/v1/sandbox/instances", sand.SelectSandboxRuntimeAll, middleware.CloudUsernameMiddlewareFunc())
	router.HandleFunc("GET", "/rest/v1/sandbox/instances/conditions", sand.GetSandboxRuntimeByCondition, middleware.CloudUsernameMiddlewareFunc())
	router.HandleFunc("PUT", "/rest/v1/sandbox/instances/:sandboxId/renew", sand.RenewSandboxRuntime, middleware.CloudUsernameMiddlewareFunc())
	router.HandleFunc("POST", "/rest/v1/sandbox/instances/:sandboxId/restore", sand.RestoreSandboxRuntime, middleware.CloudUsernameMiddlewareFunc())
	router.HandleFunc("DELETE", "/rest/v1/sandbox/instances/:sandboxId/pvc", sand.ReleaseSandboxPVC, middleware.CloudUsernameMiddlewareFunc())

	// 管理侧迁移接口
	router.HandleFunc("POST", "/rest/v1/sandbox/instances/:sandboxId/migrate", sand.MigrateSandboxPVC, middleware.CloudUsernameMiddlewareFunc())
	router.HandleFunc("GET", "/rest/v1/sandbox/instances/:sandboxId/migrate/status", sand.GetMigrationStatus, middleware.CloudUsernameMiddlewareFunc())
	// 批量 local-path → CFS 迁移
	router.HandleFunc("POST", "/rest/v1/sandbox/instances/migrate/batch-to-cfs", sand.BatchMigrateToCFS, middleware.CloudUsernameMiddlewareFunc())

	// 查询公共镜像
	image := service.NewImageService()
	img := NewImageHandler(image)
	router.HandleFunc("GET", "/rest/v1/sandbox/common/image", img.GetPublicImage, middleware.CloudUsernameMiddlewareFunc())

	// 查询绑定关系
	app := service.NewAppService()
	a := NewAppHandler(app)
	router.HandleFunc("GET", "/rest/v1/sandbox/band/enquiry/:workspaceId", a.GetAppId, middleware.CloudUsernameMiddlewareFunc())
	// 查选apiKey
	router.HandleFunc("GET", "/rest/v1/sandbox/:workspaceId/apiKey", a.GetAppKey, middleware.CloudUsernameMiddlewareFunc())
	// 更新account,同步资源账号
	router.HandleFunc("PUT", "/rest/v1/sandbox/account/:workspaceId", a.UpdateAccount, middleware.CloudUsernameMiddlewareFunc())

	// 卡片相关接口
	router.HandleFunc("POST", "/card/callback", handleCardCallback)
	router.HandleFunc("POST", "/card/send", handleSendCard)

	// 消息相关接口
	router.HandleFunc("POST", "/message/send", handleSendMessage)

	// 实例内部操作相关接口
	instanceHandler := NewInstanceHandler()
	// 定时任务相关路由
	scheduledTaskSvc := service.NewScheduledTaskService()
	scheduledTaskHandler := NewScheduledTaskHandler(scheduledTaskSvc)
	router.HandleFunc("POST", "/rest/v1/sandbox/instances/:sandboxId/scheduled-tasks", scheduledTaskHandler.CreateScheduledTasks, middleware.CloudUsernameMiddlewareFunc())
	router.HandleFunc("GET", "/rest/v1/sandbox/instances/:sandboxId/scheduled-tasks", scheduledTaskHandler.GetScheduledTasks, middleware.CloudUsernameMiddlewareFunc())
	router.HandleFunc("DELETE", "/rest/v1/sandbox/instances/:sandboxId/scheduled-tasks", scheduledTaskHandler.DeleteScheduledTasksBySandbox, middleware.CloudUsernameMiddlewareFunc())
	router.HandleFunc("DELETE", "/rest/v1/sandbox/scheduled-tasks/:taskID", scheduledTaskHandler.DeleteScheduledTask, middleware.CloudUsernameMiddlewareFunc())

	// Skill 安装接口
	router.HandleFunc("POST", "/rest/v1/sandbox/skills/install", instanceHandler.InstallSkill, middleware.CloudUsernameMiddlewareFunc())

	// 沙箱访问权限验证接口（无需鉴权）
	sandboxAccess := NewSandboxAccessHandler()
	router.HandleFunc("POST", "/sandboxes/validate-access", sandboxAccess.ValidateSandboxAccess)
	router.HandleFunc("POST", "/rest/v1/sandbox/instances/:sandboxId/access-request", sandboxAccess.RequestSandboxAccess, middleware.CloudUsernameMiddlewareFunc())
	router.HandleFunc("POST", "/api/v1/sandbox/instances/access-request/callback", sandboxAccess.AccessRequestCallback)

	// 根据 sandboxIP 查询沙箱创建人接口（需要 ACCESS-TOKEN 认证）
	router.HandleFunc("POST", "/sandboxes/owner", sand.GetSandboxOwner)

	// 检查用户对沙箱的访问权限接口（需要 ACCESS-TOKEN 认证）
	router.HandleFunc("POST", "/sandboxes/permisson/check", sand.CheckSandboxPermission)

	// 根据 sandboxUUID 查询环境变量接口（需要 ACCESS-TOKEN 认证）
	router.HandleFunc("GET", "/sandboxes/infoflow/envs", sand.GetSandboxEnvirVars)

	// 检查沙箱是否需要重定向和是否为小流量（需要 ACCESS-TOKEN 认证）
	router.HandleFunc("GET", "/sandboxes/redirect/check", sand.CheckSandboxRedirect)

	// 验证 X-Access-Token 和 sandboxUuid 是否匹配（需要 ACCESS-TOKEN 认证）
	router.HandleFunc("GET", "/sandboxes/xaccesstoken/verify", sand.VerifyXAccessToken)

	// 设置和获取沙箱启动通知话术（需要 ACCESS-TOKEN 认证，运营配置接口）
	router.HandleFunc("POST", "/sandboxes/notification/message", sand.SetNotificationMessage)
	router.HandleFunc("GET", "/sandboxes/notification/message", sand.GetNotificationMessage)

	// 根据 sandboxUUID 查询 comateAuthToken 和用户名（需要 ACCESS-TOKEN 认证）
	router.HandleFunc("POST", "/sandboxes/fuseOwner", sand.GetFuseOwner)

	// Gateway 健康检测接口（综合检测：同时检测进程和端口）
	router.HandleFunc("POST", "/sandboxes/instances/:sandboxId/gateway/check", instanceHandler.CheckGateway, middleware.RawAppKeyMiddlewareFunc())

	// OpenClaw 配置格式化
	router.HandleFunc("POST", "/sandboxes/instances/:sandboxId/openclaw-config/initialize", instanceHandler.WriteOpenClawConfig, middleware.RawAppKeyMiddlewareFunc())

	// 查找孤立 pod 接口（需要 CloudUsernameMiddlewareFunc 认证）
	router.HandleFunc("GET", "/rest/v1/sandbox/orphaned-pods", sand.GetOrphanedPods, middleware.CloudUsernameMiddlewareFunc())

	// 按模版ID的镜像小流量管理接口（硬编码Token认证）
	imageSmallHandler := NewImageSmallHandler(image)
	router.HandleFunc("POST", "/api/image/small", imageSmallHandler.SetImageSmall, middleware.RawAppKeyMiddlewareFunc())
	router.HandleFunc("GET", "/api/image/small", imageSmallHandler.GetImageSmall, middleware.RawAppKeyMiddlewareFunc())
	router.HandleFunc("PUT", "/api/image", imageSmallHandler.UpdateImageByTplID, middleware.RawAppKeyMiddlewareFunc())

	// 集群节点扩容相关接口
	clusterSvc := service.NewClusterService()
	clusterHandler := NewClusterHandler(clusterSvc)
	router.HandleFunc("POST", "/api/cluster/expand", clusterHandler.ExpandCluster)
	router.HandleFunc("POST", "/api/cluster/sync_status", clusterHandler.SyncClusterStatus)

	// UUAP 统一认证切换接口（新接口，推荐使用）
	// 查询参数: sandboxId=xxx&login=true/false
	router.HandleFunc("POST", "/api/sandboxes/auth/switch", handleSandboxAuthSwitch)

	// 查询沙箱状态和所属平台接口
	router.HandleFunc("GET", "/api/sandboxes/:sandboxID/info", handleGetSandboxInfo)

	return router
}
