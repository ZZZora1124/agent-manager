package httpserver

import (
	"context"
	"encoding/json"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util/response"
	"io"
	"net/http"
	"strconv"

	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/common"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/dao"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/instance"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/authcenter"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/resource"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/model"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util/constant"
	"icode.baidu.com/baidu/gdp/ghttp"
	"icode.baidu.com/baidu/gdp/logit"
)

// UUAPLoginRequest 登录请求参数
type UUAPLoginRequest struct {
	SandboxUuid string `json:"sandboxUuid"`
}

// UUAPLogoutRequest 登出请求参数
type UUAPLogoutRequest struct {
	SandboxUuid string `json:"sandboxUuid"`
}

// UUAPResponse 统一响应结构
type UUAPResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// handleSandboxAuthSwitch 处理沙箱认证切换请求（统一登录/登出接口）
// 路径: POST /api/sandboxes/auth/switch?sandboxId=xxx&login=true/false
// 功能: 根据 login 参数决定执行登录或登出逻辑
//   - login=true: 还原 ugate token 缓存和写入沙箱内部配置文件
//   - login=false: 清空 Redis 缓存和沙箱内部 UUAP 相关信息
func handleSandboxAuthSwitch(ctx context.Context, r ghttp.Request) ghttp.Response {
	logger := resource.LoggerService

	// 从 URL 查询参数获取 sandboxId
	sandboxId, ok := r.Query("sandboxId")
	if !ok || sandboxId == "" {
		logger.Error(ctx, "sandboxId query parameter is required")
		return ghttp.NewJSONResponse(http.StatusBadRequest, UUAPResponse{
			Code:    http.StatusBadRequest,
			Message: "sandboxId query parameter is required",
			Data:    nil,
		})
	}

	// 从 URL 查询参数获取 login
	loginStr, ok := r.Query("login")
	if !ok || loginStr == "" {
		logger.Error(ctx, "login query parameter is required")
		return ghttp.NewJSONResponse(http.StatusBadRequest, UUAPResponse{
			Code:    http.StatusBadRequest,
			Message: "login query parameter is required (true or false)",
			Data:    nil,
		})
	}

	// 解析 login 参数
	login, err := strconv.ParseBool(loginStr)
	if err != nil {
		logger.Error(ctx, "invalid login parameter",
			logit.String("login", loginStr),
			logit.String("error", err.Error()))
		return ghttp.NewJSONResponse(http.StatusBadRequest, UUAPResponse{
			Code:    http.StatusBadRequest,
			Message: "login parameter must be true or false",
			Data:    nil,
		})
	}

	// 根据 login 参数调用不同的处理逻辑
	if login {
		// 执行登录逻辑
		return handleUUAPLoginLogic(ctx, sandboxId, logger)
	}
	// 执行登出逻辑
	return handleUUAPLogoutLogic(ctx, sandboxId, logger)
}

// handleUUAPLoginLogic 登录逻辑实现
func handleUUAPLoginLogic(ctx context.Context, sandboxUuid string, logger logit.Logger) ghttp.Response {
	// 检查 Redis 客户端是否可用
	if common.RedisClient == nil {
		logger.Error(ctx, "redis client is not initialized")
		return ghttp.NewJSONResponse(http.StatusInternalServerError, UUAPResponse{
			Code:    http.StatusInternalServerError,
			Message: "redis client is not available",
			Data:    nil,
		})
	}

	// 查询沙箱运行时信息
	sandboxRuntimeDao := dao.Use(resource.OrmMain).SandboxRuntime
	runtime, queryErr := sandboxRuntimeDao.WithContext(ctx).Where(
		sandboxRuntimeDao.SandboxUUID.Eq(sandboxUuid),
		sandboxRuntimeDao.Deleted.Is(false),
	).First()

	if queryErr != nil {
		logger.Error(ctx, "query sandbox runtime failed",
			logit.String("sandboxUuid", sandboxUuid),
			logit.String("error", queryErr.Error()))
		return ghttp.NewJSONResponse(http.StatusInternalServerError, UUAPResponse{
			Code:    http.StatusInternalServerError,
			Message: "query sandbox runtime failed",
			Data:    nil,
		})
	}

	// 调用 GetSandboxUgateToken 获取 ugate token
	authCenterApi := authcenter.GetAuthCenterApi()
	// runtime.EnvirVars 是 JSON 字符串，直接使用
	sandboxToken, token := authCenterApi.GetSandboxUgateToken(runtime.SandboxUUID, runtime.CreateUser, runtime.AppID, runtime.EnvirVars)

	if sandboxToken == "" || token == "" {
		logger.Error(ctx, "get sandbox ugate token failed",
			logit.String("sandboxUuid", sandboxUuid),
			logit.String("username", runtime.CreateUser))
		return ghttp.NewJSONResponse(http.StatusInternalServerError, UUAPResponse{
			Code:    http.StatusInternalServerError,
			Message: "failed to get ugate token",
			Data:    nil,
		})
	}

	// 判断沙箱模式：用户模式（一个用户一个沙箱）还是任务模式（一个任务一个沙箱）
	isUserMode := common.IsUserSandboxMode(runtime.AppID)

	// 调用 SetSandboxOwnerCache 还原 IP 缓存
	// 用户模式：需要还原该用户在此 appid 的所有沙箱的 IP 缓存
	// 任务模式：只还原当前沙箱的 IP 缓存
	var sandboxesToRestore []*model.SandboxRuntime

	if isUserMode {
		// 用户沙箱模式：查询该用户在此 appid 的所有沙箱
		userRuntimes, err := sandboxRuntimeDao.WithContext(ctx).Where(
			sandboxRuntimeDao.AppID.Eq(runtime.AppID),
			sandboxRuntimeDao.CreateUser.Eq(runtime.CreateUser),
			sandboxRuntimeDao.Status.Eq(constant.StatusRunning),
			sandboxRuntimeDao.Deleted.Is(false),
		).Find()

		if err != nil {
			logger.Error(ctx, "query user's sandboxes failed (user mode)",
				logit.String("username", runtime.CreateUser),
				logit.Int32("appID", runtime.AppID),
				logit.String("error", err.Error()))
			return ghttp.NewJSONResponse(http.StatusInternalServerError, UUAPResponse{
				Code:    http.StatusInternalServerError,
				Message: "query user's sandboxes failed",
				Data:    nil,
			})
		}
		sandboxesToRestore = userRuntimes
	} else {
		// 任务沙箱模式：只还原当前沙箱的 IP 缓存
		sandboxesToRestore = []*model.SandboxRuntime{runtime}
	}

	appDao := dao.Use(resource.OrmMain).App
	app, err := appDao.Where(appDao.ID.Eq(runtime.AppID)).First()
	if err != nil {
		resource.LoggerService.Error(ctx, "get app info failed", logit.String("error", err.Error()))
		return response.NewGResponse(http.StatusBadRequest,
			response.QueryDbErr)
	}
	// 为这些沙箱都还原 IP 缓存
	for _, sb := range sandboxesToRestore {
		if err := common.SetSandboxOwnerCache(ctx, common.RedisClient, logger, sb.PortIP, sb.CreateUser, sb.SandboxUUID, token, sb.AppID, app.Platform); err != nil {
			logger.Error(ctx, "set sandbox owner cache failed",
				logit.String("sandboxUuid", sb.SandboxUUID),
				logit.String("portIP", sb.PortIP),
				logit.String("error", err.Error()))
			return ghttp.NewJSONResponse(http.StatusInternalServerError, UUAPResponse{
				Code:    http.StatusInternalServerError,
				Message: "failed to restore sandbox owner cache",
				Data:    nil,
			})
		}
	}

	// 写入沙箱内部配置文件 ~/.config/uuap/.eac_ugate_token_{username}
	// 需要兼容 dodo 业务：对所有需要还原的沙箱都写入 token 文件
	db := dao.Use(resource.OrmMain)
	uuapController := instance.NewUUAPLoginController(*db)

	for _, sb := range sandboxesToRestore {
		if err := uuapController.Login(ctx, instance.LoginParams{
			SandboxUUID: sb.SandboxUUID,
			Username:    sb.CreateUser,
			Token:       sandboxToken,
		}); err != nil {
			logger.Error(ctx, "write token file to sandbox failed",
				logit.String("sandboxUuid", sb.SandboxUUID),
				logit.String("error", err.Error()))

			// 对于任务沙箱模式，返回错误响应
			if !isUserMode {
				return ghttp.NewJSONResponse(http.StatusOK, UUAPResponse{
					Code:    http.StatusOK,
					Message: "login partially completed, caches restored but token file write failed",
					Data: map[string]string{
						"error": err.Error(),
					},
				})
			}
			// 用户沙箱模式：记录错误但继续为其他沙箱写入
		}
	}

	return ghttp.NewJSONResponse(http.StatusOK, UUAPResponse{
		Code:    http.StatusOK,
		Message: "login success, caches restored and token file written",
		Data: map[string]string{
			"platform": app.Platform,
		},
	})
}

// handleUUAPLogoutLogic 登出逻辑实现
func handleUUAPLogoutLogic(ctx context.Context, sandboxUuid string, logger logit.Logger) ghttp.Response {
	// 检查 Redis 客户端是否可用
	if common.RedisClient == nil {
		logger.Error(ctx, "redis client is not initialized")
		return ghttp.NewJSONResponse(http.StatusInternalServerError, UUAPResponse{
			Code:    http.StatusInternalServerError,
			Message: "redis client is not available",
			Data:    nil,
		})
	}

	// 清空 ugate token 缓存
	// 需要兼容 dodo 业务的特殊情况：
	// - 对于 dodo：key 格式为 sandbox:ugate_token:{appId}:{username}
	// - 对于其他：key 格式为 sandbox:ugate_token:{sandboxUuid}
	sandboxRuntimeDao := dao.Use(resource.OrmMain).SandboxRuntime
	runtime, queryErr := sandboxRuntimeDao.WithContext(ctx).Where(
		sandboxRuntimeDao.SandboxUUID.Eq(sandboxUuid),
		sandboxRuntimeDao.Deleted.Is(false),
	).First()

	if queryErr != nil {
		logger.Error(ctx, "query sandbox runtime failed",
			logit.String("sandboxUuid", sandboxUuid),
			logit.String("error", queryErr.Error()))
		return ghttp.NewJSONResponse(http.StatusInternalServerError, UUAPResponse{
			Code:    http.StatusInternalServerError,
			Message: "query sandbox runtime failed",
			Data:    nil,
		})
	}

	appDao := dao.Use(resource.OrmMain).App
	app, err := appDao.Where(appDao.ID.Eq(runtime.AppID)).First()
	if err != nil {
		resource.LoggerService.Error(ctx, "get app info failed", logit.String("error", err.Error()))
		return response.NewGResponse(http.StatusBadRequest,
			response.QueryDbErr)
	}
	// 判断沙箱模式：用户模式（一个用户一个沙箱）还是任务模式（一个任务一个沙箱）
	isUserMode := common.IsUserSandboxMode(runtime.AppID)

	// 删除 ugate token 缓存
	// 用户模式：使用 appId 和用户名作为 key
	// 任务模式：使用 sandboxUuid 作为 key
	if isUserMode {
		// 用户沙箱模式：使用 appId 和用户名作为 key
		ugateTokenKey := constant.UgateTokenCachePrefix + strconv.Itoa(int(runtime.AppID)) + ":" + runtime.CreateUser
		if err := common.RedisClient.Del(ctx, ugateTokenKey).Err(); err != nil {
			logger.Error(ctx, "delete ugate token cache for user mode failed",
				logit.String("sandboxUuid", sandboxUuid),
				logit.String("username", runtime.CreateUser),
				logit.Int32("appID", runtime.AppID),
				logit.String("key", ugateTokenKey),
				logit.String("error", err.Error()))
			return ghttp.NewJSONResponse(http.StatusInternalServerError, UUAPResponse{
				Code:    http.StatusInternalServerError,
				Message: "failed to delete ugate token cache",
				Data:    nil,
			})
		}

		// 删除用户模式缓存的 cookie
		authCenterApi := authcenter.GetAuthCenterApi()
		_, deleteErr := authCenterApi.DeleteBrowserLoginCookie(runtime.CreateUser, runtime.AppID)
		if deleteErr != nil {
			// 记录错误但不影响整体流程（cookie 删除失败不影响登出）
			logger.Error(ctx, "delete browser login cookie failed (user mode)",
				logit.String("sandboxUuid", sandboxUuid),
				logit.String("username", runtime.CreateUser),
				logit.Int32("appID", runtime.AppID),
				logit.String("error", deleteErr.Error()))
		}
	} else {
		// 任务沙箱模式：使用 sandboxUuid 作为 key
		ugateTokenKey := constant.UgateTokenCachePrefix + sandboxUuid
		if err := common.RedisClient.Del(ctx, ugateTokenKey).Err(); err != nil {
			logger.Error(ctx, "delete ugate token cache failed (task mode)",
				logit.String("sandboxUuid", sandboxUuid),
				logit.String("key", ugateTokenKey),
				logit.String("error", err.Error()))
			return ghttp.NewJSONResponse(http.StatusInternalServerError, UUAPResponse{
				Code:    http.StatusInternalServerError,
				Message: "failed to delete ugate token cache",
				Data:    nil,
			})
		}
	}

	// 清空 IP 到 Owner 的缓存
	// 用户沙箱模式：需要清理该用户在此 appid 的所有沙箱的 IP 缓存
	// 任务沙箱模式：只清理当前沙箱的 IP 缓存
	var sandboxesToClean []*model.SandboxRuntime

	if isUserMode {
		// 用户沙箱模式：查询该用户在此 appid 的所有沙箱，删除它们的 IP 缓存
		userRuntimes, err := sandboxRuntimeDao.WithContext(ctx).Where(
			sandboxRuntimeDao.AppID.Eq(runtime.AppID),
			sandboxRuntimeDao.CreateUser.Eq(runtime.CreateUser),
			sandboxRuntimeDao.Status.Eq(constant.StatusRunning),
			sandboxRuntimeDao.Deleted.Is(false),
		).Find()

		if err != nil {
			logger.Error(ctx, "query user's sandboxes failed (user mode)",
				logit.String("username", runtime.CreateUser),
				logit.Int32("appID", runtime.AppID),
				logit.String("error", err.Error()))
			// 不返回错误，继续执行
		} else {
			sandboxesToClean = userRuntimes
			// 删除这些沙箱的 IP 缓存
			for _, sb := range userRuntimes {
				if sb.PortIP != "" {
					ipToOwnerKey := constant.SandboxIPToOwnerPrefix + sb.PortIP
					if err := common.RedisClient.Del(ctx, ipToOwnerKey).Err(); err != nil {
						logger.Error(ctx, "delete ip_to_owner cache for user mode failed",
							logit.String("username", runtime.CreateUser),
							logit.String("portIP", sb.PortIP),
							logit.String("key", ipToOwnerKey),
							logit.String("error", err.Error()))
					}
				}
			}
		}
	} else {
		// 任务沙箱模式：只删除当前沙箱的 IP 缓存
		if runtime.PortIP != "" {
			ipToOwnerKey := constant.SandboxIPToOwnerPrefix + runtime.PortIP
			if err := common.RedisClient.Del(ctx, ipToOwnerKey).Err(); err != nil {
				logger.Error(ctx, "delete ip_to_owner cache failed (task mode)",
					logit.String("sandboxUuid", sandboxUuid),
					logit.String("portIP", runtime.PortIP),
					logit.String("key", ipToOwnerKey),
					logit.String("error", err.Error()))
			}
		}
		// 任务沙箱模式只清理当前沙箱
		sandboxesToClean = []*model.SandboxRuntime{runtime}
	}

	// 清理沙箱内部的 UUAP 相关信息
	// 用户沙箱模式：对用户的每个沙箱都执行清理
	// 任务沙箱模式：只清理当前沙箱
	// 1. 清理 ~/.config/uuap/ 目录
	// 2. 如果 ~/.tools/logout.py 存在，执行该脚本
	db := dao.Use(resource.OrmMain)
	uuapController := instance.NewUUAPLoginController(*db)

	for _, sb := range sandboxesToClean {

		if sb.Status != constant.StatusRunning {
			continue
		}
		if err := uuapController.Logout(ctx, instance.LogoutParams{
			SandboxUUID: sb.SandboxUUID,
		}); err != nil {
			// 记录错误但继续执行其他沙箱的清理
			logger.Error(ctx, "cleanup sandbox internal UUAP data failed",
				logit.String("sandboxUuid", sb.SandboxUUID),
				logit.String("error", err.Error()))

			// 对于任务沙箱模式，返回错误响应
			if !isUserMode {
				return ghttp.NewJSONResponse(http.StatusOK, UUAPResponse{
					Code:    http.StatusOK,
					Message: "logout partially completed, redis caches cleared but sandbox internal cleanup failed",
					Data: map[string]any{
						"platform": app.Platform,
						"error":    err.Error(),
					},
				})
			}
		}
	}

	return ghttp.NewJSONResponse(http.StatusOK, UUAPResponse{
		Code:    http.StatusOK,
		Message: "logout success, caches cleared",
		Data: map[string]string{
			"platform": app.Platform,
		},
	})
}

// handleUUAPLogin 处理 UUAP 登录请求（保留旧接口以保持向后兼容）
// 路径: POST /api/uuap/login
// 功能: 还原 ugate token 缓存和写入沙箱内部配置文件
func handleUUAPLogin(ctx context.Context, r ghttp.Request) ghttp.Response {
	logger := resource.LoggerService

	// 读取请求体
	body, err := io.ReadAll(r.Body())
	if err != nil {
		logger.Error(ctx, "read request body failed", logit.String("error", err.Error()))
		return ghttp.NewJSONResponse(http.StatusBadRequest, UUAPResponse{
			Code:    http.StatusBadRequest,
			Message: "read request body failed",
			Data:    nil,
		})
	}

	// 解析请求参数
	var req UUAPLoginRequest
	if err := json.Unmarshal(body, &req); err != nil {
		logger.Error(ctx, "parse request json failed", logit.String("error", err.Error()))
		return ghttp.NewJSONResponse(http.StatusBadRequest, UUAPResponse{
			Code:    http.StatusBadRequest,
			Message: "invalid json format",
			Data:    nil,
		})
	}

	// 校验参数
	if req.SandboxUuid == "" {
		logger.Error(ctx, "sandboxUuid is required")
		return ghttp.NewJSONResponse(http.StatusBadRequest, UUAPResponse{
			Code:    http.StatusBadRequest,
			Message: "sandboxUuid is required",
			Data:    nil,
		})
	}

	// 调用统一的登录逻辑
	return handleUUAPLoginLogic(ctx, req.SandboxUuid, logger)
}

// handleUUAPLogout 处理 UUAP 登出请求（保留旧接口以保持向后兼容）
// 路径: POST /api/uuap/logout
// 功能: 清空指定沙箱相关的 Redis 缓存
//  1. 清空 sandbox:ip_to_owner: 前缀的缓存
//  2. 清空 sandbox:ugate_token: 前缀的缓存
func handleUUAPLogout(ctx context.Context, r ghttp.Request) ghttp.Response {
	logger := resource.LoggerService

	// 读取请求体
	body, err := io.ReadAll(r.Body())
	if err != nil {
		logger.Error(ctx, "read request body failed", logit.String("error", err.Error()))
		return ghttp.NewJSONResponse(http.StatusBadRequest, UUAPResponse{
			Code:    http.StatusBadRequest,
			Message: "read request body failed",
			Data:    nil,
		})
	}

	// 解析请求参数
	var req UUAPLogoutRequest
	if err := json.Unmarshal(body, &req); err != nil {
		logger.Error(ctx, "parse request json failed", logit.String("error", err.Error()))
		return ghttp.NewJSONResponse(http.StatusBadRequest, UUAPResponse{
			Code:    http.StatusBadRequest,
			Message: "invalid json format",
			Data:    nil,
		})
	}

	// 校验参数
	if req.SandboxUuid == "" {
		logger.Error(ctx, "sandboxUuid is required")
		return ghttp.NewJSONResponse(http.StatusBadRequest, UUAPResponse{
			Code:    http.StatusBadRequest,
			Message: "sandboxUuid is required",
			Data:    nil,
		})
	}

	// 调用统一的登出逻辑
	return handleUUAPLogoutLogic(ctx, req.SandboxUuid, logger)
}

// SandboxInfoResponse 沙箱信息响应结构
type SandboxInfoResponse struct {
	SandboxID string `json:"sandboxId"`
	Status    string `json:"status"`
	Platform  string `json:"platform"`
}

// handleGetSandboxInfo 查询沙箱状态和所属平台
// 路径: GET /api/sandboxes/:sandboxID/info
// 功能: 根据 sandboxID 查询沙箱的运行状态和所属平台
func handleGetSandboxInfo(ctx context.Context, r ghttp.Request) ghttp.Response {
	logger := resource.LoggerService

	// 从路径参数获取 sandboxID
	sandboxID := r.Param(":sandboxID")
	if sandboxID == "" {
		logger.Error(ctx, "sandboxID path parameter is required")
		return ghttp.NewJSONResponse(http.StatusBadRequest, UUAPResponse{
			Code:    http.StatusBadRequest,
			Message: "sandboxID path parameter is required",
			Data:    nil,
		})
	}

	// 查询沙箱运行时信息
	sandboxRuntimeDao := dao.Use(resource.OrmMain).SandboxRuntime
	runtime, queryErr := sandboxRuntimeDao.WithContext(ctx).Where(
		sandboxRuntimeDao.SandboxUUID.Eq(sandboxID),
		sandboxRuntimeDao.Deleted.Is(false),
	).First()

	if queryErr != nil {
		logger.Error(ctx, "query sandbox runtime failed",
			logit.String("sandboxID", sandboxID),
			logit.String("error", queryErr.Error()))
		return ghttp.NewJSONResponse(http.StatusNotFound, UUAPResponse{
			Code:    http.StatusNotFound,
			Message: "sandbox not found or already deleted",
			Data:    nil,
		})
	}

	// 使用 GetPlatformByAppID 获取所属平台
	appDao := dao.Use(resource.OrmMain).App
	app, err := appDao.Where(appDao.ID.Eq(runtime.AppID)).First()
	if err != nil {
		resource.LoggerService.Error(ctx, "get app info failed", logit.String("error", err.Error()))
		return response.NewGResponse(http.StatusBadRequest,
			response.QueryDbErr)
	}

	// 构造响应数据
	sandboxInfo := SandboxInfoResponse{
		SandboxID: runtime.SandboxUUID,
		Status:    runtime.Status,
		Platform:  app.Platform,
	}

	return ghttp.NewJSONResponse(http.StatusOK, UUAPResponse{
		Code:    http.StatusOK,
		Message: "success",
		Data:    sandboxInfo,
	})
}
