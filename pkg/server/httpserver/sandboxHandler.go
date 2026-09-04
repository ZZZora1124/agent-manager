package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util/envvar"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util/response"
	"io"
	"net/http"
	"strconv"
	"strings"

	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/common"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/dao"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/authcenter"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/devopsauth"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/resource"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/service"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util/constant"
	"icode.baidu.com/baidu/gdp/ghttp"
	"icode.baidu.com/baidu/gdp/logit"
)

type SandboxHandler struct {
	svc service.SandboxRuntimeService
}

func NewSandboxHandler(svc service.SandboxRuntimeService) *SandboxHandler {
	return &SandboxHandler{svc: svc}
}

// parseForceParam 解析 force 查询参数
// 支持: true/false, 1/0, yes/no（大小写不敏感）
// 默认返回 false（普通释放）
func parseForceParam(r ghttp.Request) (bool, error) {
	forceStr, ok := r.Query("force")
	if !ok || forceStr == "" {
		return false, nil // 默认false，保持原行为
	}

	switch strings.ToLower(forceStr) {
	case "true", "1", "yes":
		return true, nil
	case "false", "0", "no":
		return false, nil
	default:
		return false, fmt.Errorf("无效的force参数值: %s (支持: true/false, 1/0, yes/no)", forceStr)
	}
}

// 1. 创建实例  POST /rest/v1/sandbox/instances
func (h *SandboxHandler) CreateSandboxRuntime(ctx context.Context, r ghttp.Request) ghttp.Response {
	var req service.SandboxRuntimeRequest

	body, err := io.ReadAll(r.Body())
	if err != nil {
		return getResponse(http.StatusBadRequest, "read body error", nil)
	}

	if err := json.Unmarshal(body, &req); err != nil {
		return getResponse(http.StatusBadRequest, "invalid json", nil)
	}
	k, v := getPermissionByAppID(ctx, req.AppID)
	if !k {
		return getResponse(http.StatusBadRequest, v, nil)
	}
	if v != constant.RoleAdmin && v != constant.RoleMember && v != constant.RoleSuperAdmin {
		return getResponse(http.StatusForbidden, "用户权限不足", nil)
	}
	// templateId 为 2263 时，成员角色禁止创建
	if req.TemplateID == 2263 && v == constant.RoleMember {
		return getResponse(http.StatusForbidden, "成员角色无权限创建该模版实例", nil)
	}
	resp, err := h.svc.CreateSandboxRuntime(ctx, req)
	if err != nil {
		return getResponse(http.StatusInternalServerError, err.Error(), resp)
	}
	return getResponse(http.StatusOK, "SUCCESS", resp)
}

// 2. 释放实例  DELETE /rest/v1/sandbox/instances/:sandboxId
// 支持完全释放: DELETE /rest/v1/sandbox/instances/:sandboxId?force=true
func (h *SandboxHandler) ReleaseSandboxRuntime(ctx context.Context, r ghttp.Request) ghttp.Response {
	// 1. 解析sandboxId
	idStr := r.Param(":sandboxId")
	if idStr == "" {
		return getResponse(http.StatusBadRequest, "请传入沙箱实例Id", nil)
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		return getResponse(http.StatusBadRequest, "无效的沙箱实例Id", nil)
	}

	// 2. 解析force参数
	force, err := parseForceParam(r)
	if err != nil {
		return getResponse(http.StatusBadRequest, err.Error(), nil)
	}

	// 3. 权限检查
	k, v := getPermissionBySandboxRuntimeId(ctx, id)
	if !k {
		return getResponse(http.StatusBadRequest, v, nil)
	}
	if v != constant.RoleAdmin && v != constant.RoleMember && v != constant.RoleSuperAdmin {
		return getResponse(http.StatusForbidden, "用户权限不足", nil)
	}

	// 4. 日志记录
	resource.LoggerService.Notice(ctx, "release sandbox request",
		logit.Int64("sandboxRuntimeID", id),
		logit.Bool("force", force),
		logit.String("user", ctx.Value(constant.CtxLoginUserKey).(string)),
		logit.String("role", v))

	// 5. 调用统一的service方法
	status := "user_kill"
	if force {
		status = "released"
	}
	_, err = h.svc.ReleaseSandboxRuntime(ctx, id, v, status, force)
	if err != nil {
		resource.LoggerService.Error(ctx, "release sandbox failed",
			logit.Int64("sandboxRuntimeID", id),
			logit.Bool("force", force),
			logit.String("error", err.Error()))
		return getResponse(http.StatusInternalServerError, err.Error(), nil)
	}

	resource.LoggerService.Notice(ctx, "release sandbox success",
		logit.Int64("sandboxRuntimeID", id),
		logit.Bool("force", force))
	return getResponse(http.StatusOK, "SUCCESS", nil)
}

// 3. 单条查询  GET /rest/v1/sandbox/instances/:sandboxId
func (h *SandboxHandler) SelectSandboxRuntime(ctx context.Context, r ghttp.Request) ghttp.Response {
	idStr := r.Param(":sandboxId") // 取出字符串
	if idStr == "" {
		return getResponse(http.StatusBadRequest, "请传入沙箱实例Id", nil)
	}
	id, err := strconv.Atoi(idStr)
	if err != nil || id < 0 {
		return getResponse(http.StatusBadRequest, "无效的沙箱实例Id", nil)
	}
	k, v := getPermissionBySandboxRuntimeId(ctx, int64(id))
	if !k {
		return getResponse(http.StatusBadRequest, v, nil)
	}
	if v != constant.RoleAdmin && v != constant.RoleMember && v != constant.RoleSuperAdmin {
		return getResponse(http.StatusForbidden, "用户权限不足", nil)
	}
	resp, err := h.svc.SelectSandboxRuntime(ctx, int64(id), v)
	if err != nil {
		return getResponse(http.StatusInternalServerError, err.Error(), nil)
	}
	return getResponse(http.StatusOK, "SUCCESS", resp)
}

// 4. 根据 templateID 批量查  GET /rest/v1/sandbox/instances
func (h *SandboxHandler) SelectSandboxRuntimeAll(ctx context.Context, r ghttp.Request) ghttp.Response {
	idStr, ok := r.Query("templateId") // 取出字符串
	if !ok {
		return getResponse(http.StatusBadRequest, "请传入沙箱应用Id", nil)
	}
	id, err := strconv.Atoi(idStr)
	if err != nil || id < 0 {
		return getResponse(http.StatusBadRequest, "无效的沙箱应用Id", nil)
	}
	k, v := getPermissionByTemplate(ctx, int32(id))
	if !k {
		return getResponse(http.StatusBadRequest, v, nil)
	}
	if v != constant.RoleAdmin && v != constant.RoleMember && v != constant.RoleSuperAdmin {
		return getResponse(http.StatusForbidden, "用户权限不足", nil)
	}
	resp, err := h.svc.SelectSandboxRuntimeAll(ctx, int32(id), v)
	if err != nil {
		return getResponse(http.StatusInternalServerError, err.Error(), nil)
	}
	return getResponse(http.StatusOK, "SUCCESS", resp)
}

// 5. 根据条件查询  GET /rest/v1/sandbox/instances/conditions
func (h *SandboxHandler) GetSandboxRuntimeByCondition(ctx context.Context, r ghttp.Request) ghttp.Response {
	// 查询条件参数(改为可选)
	idStr, _ := r.Query("templateId")
	createUser, _ := r.Query("createUser")
	sandboxRuntimeName, _ := r.Query("sandboxRuntimeName")
	status, _ := r.Query("status")
	startTimeBegin, _ := r.Query("startTimeBegin") // 启动时间范围-开始
	startTimeEnd, _ := r.Query("startTimeEnd")     // 启动时间范围-结束

	// 解析 templateId(如果提供了)
	var templateID int32
	var role string // 声明 role 变量，用于接收权限信息
	if idStr != "" {
		id, err := strconv.Atoi(idStr)
		if err != nil || id < 0 {
			return getResponse(http.StatusBadRequest, "无效的沙箱模版Id", nil)
		}
		templateID = int32(id)

		// 权限检查(只有提供了 templateId 才检查)
		k, v := getPermissionByTemplate(ctx, templateID)
		if !k {
			return getResponse(http.StatusBadRequest, v, nil)
		}
		if v != constant.RoleAdmin && v != constant.RoleMember && v != constant.RoleSuperAdmin {
			return getResponse(http.StatusForbidden, "用户权限不足", nil)
		}
		role = v // 将权限信息赋值给外部变量
	} else {
		return getResponse(http.StatusBadRequest, "无效的沙箱模版Id", nil)
	}

	// 分页参数(可选,默认值)
	page := 1
	pageSize := 20
	if pageStr, ok := r.Query("page"); ok {
		if p, err := strconv.Atoi(pageStr); err == nil && p >= 1 {
			page = p
		} else if err != nil {
			return getResponse(http.StatusBadRequest, "无效的页码", nil)
		}
	}
	if sizeStr, ok := r.Query("pageSize"); ok {
		if s, err := strconv.Atoi(sizeStr); err == nil && s >= 1 && s <= 100 {
			pageSize = s
		} else if err != nil {
			return getResponse(http.StatusBadRequest, "无效的每页条数", nil)
		} else if s > 100 {
			return getResponse(http.StatusBadRequest, "每页条数不能超过100", nil)
		}
	}

	// 排序参数(可选,默认值)
	sortField, _ := r.Query("sortField")
	sortOrder, _ := r.Query("sortOrder")

	// 排序字段白名单校验
	if sortField != "" {
		validFields := map[string]bool{
			"startTime":   true,
			"createTime":  true,
			"releaseTime": true,
			"timeoutTime": true,
		}
		if !validFields[sortField] {
			return getResponse(http.StatusBadRequest, "无效的排序字段,允许的值: startTime, createTime, releaseTime, timeoutTime", nil)
		}
	} else {
		sortField = "startTime" // 默认按启动时间排序
	}

	// 排序方向校验
	if sortOrder != "" {
		if sortOrder != "asc" && sortOrder != "desc" {
			return getResponse(http.StatusBadRequest, "无效的排序方向,允许的值: asc, desc", nil)
		}
	} else {
		sortOrder = "desc" // 默认降序
	}

	resp, err := h.svc.GetSandboxRuntimeByCondition(ctx, service.SelectSandboxRuntimeConditionRequest{
		TemplateID:         templateID,
		CreateUser:         createUser,
		SandboxRuntimeName: sandboxRuntimeName,
		Status:             status,
		StartTimeBegin:     startTimeBegin,
		StartTimeEnd:       startTimeEnd,
		Page:               page,
		PageSize:           pageSize,
		SortField:          sortField,
		SortOrder:          sortOrder,
	}, role)
	if err != nil {
		return getResponse(http.StatusInternalServerError, err.Error(), nil)
	}
	return getResponse(http.StatusOK, "SUCCESS", resp)
}

// 6. 恢复实例  POST /rest/v1/sandbox/instances/:sandboxId/restore
func (h *SandboxHandler) RestoreSandboxRuntime(ctx context.Context, r ghttp.Request) ghttp.Response {
	idStr := r.Param(":sandboxId")
	if idStr == "" {
		return getResponse(http.StatusBadRequest, "请传入沙箱实例Id", nil)
	}
	id, err := strconv.Atoi(idStr)
	if err != nil || id < 0 {
		return getResponse(http.StatusBadRequest, "无效的沙箱实例Id", nil)
	}
	k, v := getPermissionBySandboxRuntimeId(ctx, int64(id))
	if !k {
		return getResponse(http.StatusBadRequest, v, nil)
	}
	if v != constant.RoleAdmin && v != constant.RoleMember && v != constant.RoleSuperAdmin {
		return getResponse(http.StatusForbidden, "用户权限不足", nil)
	}

	// 读取请求体获取环境变量
	body, err := io.ReadAll(r.Body())
	if err != nil {
		return getResponse(http.StatusBadRequest, "读取请求体失败", nil)
	}
	var req struct {
		EnvVars map[string]string `json:"envVars"`
	}
	if len(body) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			return getResponse(http.StatusBadRequest, "请求体格式错误", nil)
		}
	}

	// HTTP handler 触发的恢复，触发类型为 manual
	resp, err := h.svc.RestoreSandboxRuntime(ctx, int64(id), v, constant.TriggerTypeManual, req.EnvVars)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrSandboxRuntimeConflict):
			return getResponse(http.StatusConflict, err.Error(), resp)
		case errors.Is(err, service.ErrSandboxRuntimeNoPVC):
			return getResponse(http.StatusBadRequest, err.Error(), resp)
		default:
			return getResponse(http.StatusInternalServerError, err.Error(), resp)
		}
	}
	return getResponse(http.StatusOK, "SUCCESS", resp)
}

// ReleaseSandboxPVC 释放PVC  DELETE /rest/v1/sandbox/instances/:sandboxId/pvc
func (h *SandboxHandler) ReleaseSandboxPVC(ctx context.Context, r ghttp.Request) ghttp.Response {
	idStr := r.Param(":sandboxId")
	if idStr == "" {
		return getResponse(http.StatusBadRequest, "请传入沙箱实例Id", nil)
	}
	id, err := strconv.Atoi(idStr)
	if err != nil {
		return getResponse(http.StatusBadRequest, "无效的沙箱实例Id", nil)
	}
	k, v := getPermissionBySandboxRuntimeId(ctx, int64(id))
	if !k {
		return getResponse(http.StatusBadRequest, v, nil)
	}
	if v != constant.RoleAdmin && v != constant.RoleMember && v != constant.RoleSuperAdmin {
		return getResponse(http.StatusForbidden, "用户权限不足", nil)
	}

	// 通过 ID 查出 sandbox_uuid
	sandboxRuntimeDao := dao.Use(resource.OrmMain).SandboxRuntime
	runtime, queryErr := sandboxRuntimeDao.Where(sandboxRuntimeDao.ID.Eq(int64(id))).First()
	if queryErr != nil || runtime == nil {
		return getResponse(http.StatusBadRequest, "沙箱实例不存在", nil)
	}

	if err := h.svc.ReleaseSandboxPVC(ctx, runtime.SandboxUUID); err != nil {
		return getResponse(http.StatusInternalServerError, err.Error(), nil)
	}
	return getResponse(http.StatusOK, "SUCCESS", nil)
}

// 6. 续期实例  PUT /rest/v1/sandbox/instances/:sandboxId/renew
func (h *SandboxHandler) RenewSandboxRuntime(ctx context.Context, r ghttp.Request) ghttp.Response {
	idStr := r.Param(":sandboxId")
	if idStr == "" {
		return getResponse(http.StatusBadRequest, "请传入沙箱实例Id", nil)
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		return getResponse(http.StatusBadRequest, "无效的沙箱实例Id", nil)
	}

	// 从请求体中读取 timeout 参数
	body, err := io.ReadAll(r.Body())
	if err != nil {
		return getResponse(http.StatusBadRequest, "读取请求体失败", nil)
	}

	var req struct {
		Timeout int32 `json:"timeout"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return getResponse(http.StatusBadRequest, "请求体格式错误", nil)
	}

	if req.Timeout <= 0 {
		return getResponse(http.StatusBadRequest, "请传入有效的超时时间", nil)
	}

	// 权限检查
	k, v := getPermissionBySandboxRuntimeId(ctx, id)
	if !k {
		return getResponse(http.StatusBadRequest, v, nil)
	}
	if v != constant.RoleAdmin && v != constant.RoleMember && v != constant.RoleSuperAdmin {
		return getResponse(http.StatusForbidden, "用户权限不足", nil)
	}

	// 调用服务层续期方法
	err = h.svc.RenewSandboxRuntime(ctx, id, req.Timeout, v)
	if err != nil {
		return getResponse(http.StatusInternalServerError, err.Error(), nil)
	}

	return getResponse(http.StatusOK, "续期成功", nil)
}

func getPermissionBySandboxRuntimeId(ctx context.Context, sandboxRuntimeID int64) (bool, string) {
	SandboxRuntimeDao := dao.Use(resource.OrmMain).SandboxRuntime
	sdr, err := SandboxRuntimeDao.Where(SandboxRuntimeDao.ID.Eq(sandboxRuntimeID)).First()
	if err != nil || sdr == nil {
		return false, "无效的沙箱实例Id"
	}
	return getPermissionByAppID(ctx, sdr.AppID)
}

func getPermissionByScheduledTaskId(ctx context.Context, taskID int64) (bool, string) {
	scheduledTaskDao := dao.Use(resource.OrmMain).ScheduledTask
	task, err := scheduledTaskDao.Where(scheduledTaskDao.ID.Eq(taskID)).First()
	if err != nil || task == nil {
		return false, "无效的定时任务Id"
	}
	return getPermissionBySandboxRuntimeId(ctx, task.SandboxID)
}

// MigrateSandboxPVC 管理侧触发 PVC 迁移
// POST /rest/v1/sandbox/instances/:sandboxId/migrate
func (h *SandboxHandler) MigrateSandboxPVC(ctx context.Context, r ghttp.Request) ghttp.Response {
	idStr := r.Param(":sandboxId")
	if idStr == "" {
		return getResponse(http.StatusBadRequest, "请传入沙箱实例Id", nil)
	}
	id, err := strconv.Atoi(idStr)
	if err != nil || id < 0 {
		return getResponse(http.StatusBadRequest, "无效的沙箱实例Id", nil)
	}

	k, v := getPermissionBySandboxRuntimeId(ctx, int64(id))
	if !k {
		return getResponse(http.StatusBadRequest, v, nil)
	}
	if v != constant.RoleAdmin && v != constant.RoleSuperAdmin {
		return getResponse(http.StatusForbidden, "仅管理员可执行迁移操作", nil)
	}

	// 读取请求体
	body, err := io.ReadAll(r.Body())
	if err != nil {
		return getResponse(http.StatusBadRequest, "读取请求体失败", nil)
	}
	var req struct {
		TargetNode string `json:"targetNode"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return getResponse(http.StatusBadRequest, "请求体格式错误", nil)
	}
	if req.TargetNode == "" {
		return getResponse(http.StatusBadRequest, "请传入目标节点", nil)
	}

	// 通过 ID 查出 sandbox_uuid
	sandboxRuntimeDao := dao.Use(resource.OrmMain).SandboxRuntime
	runtime, queryErr := sandboxRuntimeDao.Where(sandboxRuntimeDao.ID.Eq(int64(id))).First()
	if queryErr != nil || runtime == nil {
		return getResponse(http.StatusBadRequest, "沙箱实例不存在", nil)
	}

	migration, err := service.MigrateSandboxPVC(ctx, runtime.SandboxUUID, req.TargetNode)
	if err != nil {
		resource.LoggerService.Error(ctx, "migrate sandbox pvc failed",
			logit.Int64("sandboxRuntimeId", int64(id)),
			logit.String("error", err.Error()))
		return getResponse(http.StatusInternalServerError, err.Error(), nil)
	}

	return getResponse(http.StatusOK, "SUCCESS", map[string]interface{}{
		"id":           migration.ID,
		"sandbox_uuid": migration.SandboxUUID,
		"target_node":  migration.TargetNode,
		"status":       migration.Status,
	})
}

// GetMigrationStatus 管理侧查询迁移状态
// GET /rest/v1/sandbox/instances/:sandboxId/migrate/status
func (h *SandboxHandler) GetMigrationStatus(ctx context.Context, r ghttp.Request) ghttp.Response {
	idStr := r.Param(":sandboxId")
	if idStr == "" {
		return getResponse(http.StatusBadRequest, "请传入沙箱实例Id", nil)
	}
	id, err := strconv.Atoi(idStr)
	if err != nil || id < 0 {
		return getResponse(http.StatusBadRequest, "无效的沙箱实例Id", nil)
	}

	k, v := getPermissionBySandboxRuntimeId(ctx, int64(id))
	if !k {
		return getResponse(http.StatusBadRequest, v, nil)
	}
	if v != constant.RoleAdmin && v != constant.RoleMember && v != constant.RoleSuperAdmin {
		return getResponse(http.StatusForbidden, "用户权限不足", nil)
	}

	// 通过 ID 查出 sandbox_uuid
	sandboxRuntimeDao := dao.Use(resource.OrmMain).SandboxRuntime
	runtime, queryErr := sandboxRuntimeDao.Where(sandboxRuntimeDao.ID.Eq(int64(id))).First()
	if queryErr != nil || runtime == nil {
		return getResponse(http.StatusBadRequest, "沙箱实例不存在", nil)
	}

	migration, err := service.GetMigrationStatus(ctx, runtime.SandboxUUID)
	if err != nil {
		return getResponse(http.StatusNotFound, "未找到迁移记录", nil)
	}

	return getResponse(http.StatusOK, "SUCCESS", migration)
}

// BatchMigrateToCFS 批量将 local-path PVC 迁移为 CFS
// POST /rest/v1/sandbox/instances/migrate/batch-to-cfs
func (h *SandboxHandler) BatchMigrateToCFS(ctx context.Context, r ghttp.Request) ghttp.Response {
	body, err := io.ReadAll(r.Body())
	if err != nil {
		return getResponse(http.StatusBadRequest, "读取请求体失败", nil)
	}

	var req service.BatchMigrateLocalPathToCFSRequest
	if len(body) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			return getResponse(http.StatusBadRequest, "请求体格式错误", nil)
		}
	}

	result, err := service.BatchMigrateLocalPathToCFS(ctx, &req)
	if err != nil {
		resource.LoggerService.Error(ctx, "batch migrate local-path to CFS failed",
			logit.String("error", err.Error()))
		return getResponse(http.StatusInternalServerError, err.Error(), nil)
	}

	return getResponse(http.StatusOK, "SUCCESS", result)
}

// GetSandboxOwner 根据沙箱IP查询创建人
// POST /sandboxes/owner
func (h *SandboxHandler) GetSandboxOwner(ctx context.Context, r ghttp.Request) ghttp.Response {
	// 校验 ACCESS-TOKEN
	accessToken, hasToken := r.Header("ACCESS-TOKEN")
	if !hasToken || accessToken == "" {
		return getResponse(http.StatusUnauthorized, "缺少ACCESS-TOKEN请求头", nil)
	}

	// 从配置文件读取全局 access token 并校验
	authApi := devopsauth.GetAuthApi()
	if authApi.Config.GlobalAccessToken == "" {
		resource.LoggerService.Error(ctx, "GlobalAccessToken not configured in devops_auth.toml")
		return getResponse(http.StatusInternalServerError, "服务器配置错误", nil)
	}

	if accessToken != authApi.Config.GlobalAccessToken {
		return getResponse(http.StatusForbidden, "ACCESS-TOKEN校验失败", nil)
	}

	// 读取请求体
	body, err := io.ReadAll(r.Body())
	if err != nil {
		return getResponse(http.StatusBadRequest, "读取请求体失败", nil)
	}

	var req struct {
		SandboxIP string `json:"sandboxIP"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return getResponse(http.StatusBadRequest, "请求体格式错误", nil)
	}

	if req.SandboxIP == "" {
		return getResponse(http.StatusBadRequest, "请传入沙箱IP", nil)
	}

	// 优先从缓存查询
	username, sandboxUUID, ugateToken, platform, appID, found := common.GetSandboxOwnerFromCache(ctx, resource.RedisClient, resource.LoggerService, req.SandboxIP)
	if found {
		// 缓存命中，直接返回
		result := map[string]interface{}{
			"sandboxUUID": sandboxUUID,
			"username":    username,
			"ugateToken":  ugateToken,
			"platform":    platform,
			"autoLogin":   common.IsAutoLogin(appID),
		}

		// 异步调用浏览器自动登录Cookie刷新
		authCenterApi := authcenter.GetAuthCenterApi()
		go func() {
			if _, err := authCenterApi.RefreshBrowserLoginCookie(sandboxUUID, username, appID); err != nil {
				resource.LoggerService.Error(context.Background(), "failed to refresh browser login cookie",
					logit.String("sandboxUUID", sandboxUUID),
					logit.String("username", username),
					logit.String("error", err.Error()))
			}
		}()

		return getResponse(http.StatusOK, "validation successful", result)
	}

	// 缓存未命中，查询数据库
	sandboxRuntimeDao := dao.Use(resource.OrmMain).SandboxRuntime
	runtime, queryErr := sandboxRuntimeDao.Where(
		sandboxRuntimeDao.PortIP.Eq(req.SandboxIP),
		sandboxRuntimeDao.Status.Eq(constant.StatusRunning),
	).First()
	if queryErr != nil || runtime == nil {
		resource.LoggerService.Error(ctx, "sandbox not found by ip",
			logit.String("sandboxIP", req.SandboxIP),
			logit.String("error", fmt.Sprintf("%v", queryErr)))
		return getResponse(http.StatusNotFound, "未找到该IP对应的沙箱实例", nil)
	}

	appDao := dao.Use(resource.OrmMain).App
	app, err := appDao.Where(appDao.ID.Eq(runtime.AppID)).First()
	if err != nil {
		resource.LoggerService.Error(ctx, "get app info failed", logit.String("error", err.Error()))
		return response.NewGResponse(http.StatusBadRequest,
			response.QueryDbErr)
	}
	// 获取 ugateToken（内部会自动使用缓存）
	authCenterApi := authcenter.GetAuthCenterApi()
	// runtime.EnvirVars 是 JSON 字符串，直接使用
	_, ugateTokenFromAuth := authCenterApi.GetSandboxUgateToken(runtime.SandboxUUID, runtime.CreateUser, runtime.AppID, runtime.EnvirVars)

	// 回写缓存（包含 ugateToken）
	_ = common.SetSandboxOwnerCache(ctx, resource.RedisClient, resource.LoggerService, req.SandboxIP, runtime.CreateUser,
		runtime.SandboxUUID, ugateTokenFromAuth, runtime.AppID, app.Platform)

	// 构造返回结果
	result := map[string]interface{}{
		"sandboxUUID": runtime.SandboxUUID,
		"username":    runtime.CreateUser,
		"ugateToken":  ugateTokenFromAuth,
		"platform":    app.Platform,
		"autoLogin":   common.IsAutoLogin(runtime.AppID),
	}

	// 异步调用浏览器自动登录Cookie刷新
	go func() {
		if _, err := authCenterApi.RefreshBrowserLoginCookie(runtime.SandboxUUID, runtime.CreateUser, runtime.AppID); err != nil {
			resource.LoggerService.Error(context.Background(), "failed to refresh browser login cookie",
				logit.String("sandboxUUID", runtime.SandboxUUID),
				logit.String("username", runtime.CreateUser),
				logit.String("error", err.Error()))
		}
	}()

	return getResponse(http.StatusOK, "", result)
}

// GetSandboxEnvirVars 根据沙箱UUID查询环境变量中的INFOFLOW相关配置
// GET /sandboxes/infoflow/envs?sandboxUuid=xxx
func (h *SandboxHandler) GetSandboxEnvirVars(ctx context.Context, r ghttp.Request) ghttp.Response {
	// 校验 ACCESS-TOKEN
	accessToken, hasToken := r.Header("ACCESS-TOKEN")
	if !hasToken || accessToken == "" {
		return getResponse(http.StatusUnauthorized, "ACCESS-TOKEN header is required", nil)
	}

	// 从配置文件读取全局 access token 并校验
	authApi := devopsauth.GetAuthApi()
	if authApi.Config.GlobalAccessToken == "" {
		resource.LoggerService.Error(ctx, "GlobalAccessToken not configured in devops_auth.toml")
		return getResponse(http.StatusInternalServerError, "Server configuration error", nil)
	}

	if accessToken != authApi.Config.GlobalAccessToken {
		return getResponse(http.StatusForbidden, "ACCESS-TOKEN validation failed", nil)
	}

	// 从 query 参数获取 sandboxUuid
	sandboxUUID, ok := r.Query("sandboxUuid")
	if !ok || sandboxUUID == "" {
		return getResponse(http.StatusBadRequest, "sandboxUuid parameter is required", nil)
	}

	// 查询数据库
	sandboxRuntimeDao := dao.Use(resource.OrmMain).SandboxRuntime
	runtime, queryErr := sandboxRuntimeDao.Where(
		sandboxRuntimeDao.SandboxUUID.Eq(sandboxUUID),
		sandboxRuntimeDao.Status.Eq(constant.StatusRunning),
	).First()
	if queryErr != nil || runtime == nil {
		resource.LoggerService.Error(ctx, "sandbox not found by uuid",
			logit.String("sandboxUUID", sandboxUUID),
			logit.String("error", fmt.Sprintf("%v", queryErr)))
		return getResponse(http.StatusNotFound, "Sandbox instance not found for the given UUID", nil)
	}

	// 解析 envir_vars JSON 字段
	var envirVarsMap map[string]string
	if runtime.EnvirVars != "" {
		if err := json.Unmarshal([]byte(runtime.EnvirVars), &envirVarsMap); err != nil {
			resource.LoggerService.Error(ctx, "failed to parse envir_vars",
				logit.String("sandboxUUID", sandboxUUID),
				logit.String("error", err.Error()))
			return getResponse(http.StatusInternalServerError, "Failed to parse environment variables", nil)
		}
	}

	// 只返回 INFOFLOW_APP_KEY 和 INFOFLOW_APP_SECRET
	result := map[string]interface{}{
		"INFOFLOW_APP_KEY":    envirVarsMap["INFOFLOW_APP_KEY"],
		"INFOFLOW_APP_SECRET": envirVarsMap["INFOFLOW_APP_SECRET"],
	}

	return getResponse(http.StatusOK, "SUCCESS", result)
}

// CheckSandboxPermission 检查用户对沙箱的访问权限
// POST /sandboxes/permisson/check
func (h *SandboxHandler) CheckSandboxPermission(ctx context.Context, r ghttp.Request) ghttp.Response {
	// 校验 ACCESS-TOKEN
	accessToken, hasToken := r.Header("ACCESS-TOKEN")
	if !hasToken || accessToken == "" {
		return getResponse(http.StatusUnauthorized, "Missing ACCESS-TOKEN header", nil)
	}

	// 从配置文件读取全局 access token 并校验
	authApi := devopsauth.GetAuthApi()
	if authApi.Config.GlobalAccessToken == "" {
		resource.LoggerService.Error(ctx, "GlobalAccessToken not configured in devops_auth.toml")
		return getResponse(http.StatusInternalServerError, "Server configuration error", nil)
	}

	if accessToken != authApi.Config.GlobalAccessToken {
		return getResponse(http.StatusForbidden, "ACCESS-TOKEN verification failed", nil)
	}

	// 读取请求体
	body, err := io.ReadAll(r.Body())
	if err != nil {
		return getResponse(http.StatusBadRequest, "Failed to read request body", nil)
	}

	var req struct {
		ZtAuthorization string `json:"ztAuthorization"`
		SandboxID       string `json:"sandboxId"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return getResponse(http.StatusBadRequest, "Invalid request body format", nil)
	}

	if req.ZtAuthorization == "" {
		return getResponse(http.StatusBadRequest, "ztAuthorization is required", nil)
	}

	if req.SandboxID == "" {
		return getResponse(http.StatusBadRequest, "sandboxId is required", nil)
	}

	// 检查JWT secret配置（复用前面获取的authApi）
	if authApi.Config.ZtAuthorizationSecret == "" {
		resource.LoggerService.Error(ctx, "ZtAuthorizationSecret not configured in devops_auth.toml")
		return getResponse(http.StatusInternalServerError, "Server configuration error", nil)
	}

	// 解析JWT token获取username
	username, err := common.ParseJWTToken(req.ZtAuthorization, authApi.Config.ZtAuthorizationSecret)
	if err != nil {
		resource.LoggerService.Error(ctx, "parse JWT token failed",
			logit.String("error", err.Error()))
		return getResponse(http.StatusUnauthorized, fmt.Sprintf("Failed to parse JWT token: %s", err.Error()), nil)
	}

	// 查询沙箱实例
	sandboxRuntimeDao := dao.Use(resource.OrmMain).SandboxRuntime
	runtime, queryErr := sandboxRuntimeDao.Where(sandboxRuntimeDao.SandboxUUID.Eq(req.SandboxID)).First()
	if queryErr != nil || runtime == nil {
		resource.LoggerService.Error(ctx, "sandbox not found by sandboxId",
			logit.String("sandboxId", req.SandboxID),
			logit.String("error", fmt.Sprintf("%v", queryErr)))
		return getResponse(http.StatusNotFound, "Sandbox instance not found", nil)
	}

	// 判断权限：沙箱创建人和JWT token解析的username是否一致
	hasPermission := (runtime.CreateUser == username)

	// 检查 Redis 授权缓存
	if !hasPermission && common.HasSandboxGrant(ctx, resource.RedisClient, req.SandboxID, username) {
		hasPermission = true
	}

	// 构造返回结果
	result := map[string]interface{}{
		"sandboxUUID":   runtime.SandboxUUID,
		"username":      runtime.CreateUser,
		"hasPermission": hasPermission,
	}

	return getResponse(http.StatusOK, "validation successful", result)
}

// CheckSandboxRedirect 检查沙箱是否需要重定向和是否为小流量
// GET /sandboxes/redirect/check?sandboxUuid=xxx
func (h *SandboxHandler) CheckSandboxRedirect(ctx context.Context, r ghttp.Request) ghttp.Response {
	// 校验 ACCESS-TOKEN
	accessToken, hasToken := r.Header("ACCESS-TOKEN")
	if !hasToken || accessToken == "" {
		return getResponse(http.StatusUnauthorized, "ACCESS-TOKEN header is required", nil)
	}

	// 从配置文件读取全局 access token 并校验
	authApi := devopsauth.GetAuthApi()
	if authApi.Config.GlobalAccessToken == "" {
		resource.LoggerService.Error(ctx, "GlobalAccessToken not configured in devops_auth.toml")
		return getResponse(http.StatusInternalServerError, "Server configuration error", nil)
	}

	if accessToken != authApi.Config.GlobalAccessToken {
		return getResponse(http.StatusForbidden, "ACCESS-TOKEN validation failed", nil)
	}

	// 从 query 参数获取 sandboxUuid
	sandboxUUID, ok := r.Query("sandboxUuid")
	if !ok || sandboxUUID == "" {
		return getResponse(http.StatusBadRequest, "sandboxUuid parameter is required", nil)
	}

	// 查询数据库获取 sandbox 信息
	sandboxRuntimeDao := dao.Use(resource.OrmMain).SandboxRuntime
	runtime, queryErr := sandboxRuntimeDao.Where(
		sandboxRuntimeDao.SandboxUUID.Eq(sandboxUUID),
	).First()
	if queryErr != nil || runtime == nil {
		resource.LoggerService.Error(ctx, "sandbox not found by uuid",
			logit.String("sandboxUUID", sandboxUUID),
			logit.String("error", fmt.Sprintf("%v", queryErr)))
		return getResponse(http.StatusNotFound, "Sandbox instance not found for the given UUID", nil)
	}

	// 根据 app_id 和 create_user 一次性检查是否需要重定向以及是否为小流量
	redirect, smallFlow := common.CheckRedirectAndSmallFlow(ctx, resource.RedisClient, resource.LoggerService, runtime.AppID, runtime.CreateUser)

	// 构造返回结果
	result := map[string]interface{}{
		"redirect":  redirect,
		"smallFlow": smallFlow,
	}

	return getResponse(http.StatusOK, "", result)
}

// VerifyXAccessToken 验证 X-Access-Token 和 sandboxUuid 是否匹配
// GET /sandboxes/xaccesstoken/verify?sandboxUuid=xxx
// Headers: ACCESS-TOKEN (全局Token), X-Access-Token (沙箱Token)
func (h *SandboxHandler) VerifyXAccessToken(ctx context.Context, r ghttp.Request) ghttp.Response {
	// 校验 ACCESS-TOKEN
	accessToken, hasToken := r.Header("ACCESS-TOKEN")
	if !hasToken || accessToken == "" {
		return getResponse(http.StatusUnauthorized, "ACCESS-TOKEN header is required", nil)
	}

	// 从配置文件读取全局 access token 并校验
	authApi := devopsauth.GetAuthApi()
	if authApi.Config.GlobalAccessToken == "" {
		resource.LoggerService.Error(ctx, "GlobalAccessToken not configured in devops_auth.toml")
		return getResponse(http.StatusInternalServerError, "Server configuration error", nil)
	}

	if accessToken != authApi.Config.GlobalAccessToken {
		return getResponse(http.StatusForbidden, "ACCESS-TOKEN validation failed", nil)
	}

	// 从 query 参数获取 sandboxUuid
	sandboxUUID, ok := r.Query("sandboxUuid")
	if !ok || sandboxUUID == "" {
		return getResponse(http.StatusBadRequest, "sandboxUuid parameter is required", nil)
	}

	// 从 header 获取 X-Access-Token
	xAccessToken, hasXAccessToken := r.Header("X-Access-Token")
	if !hasXAccessToken || xAccessToken == "" {
		resource.LoggerService.Warning(ctx, "X-Access-Token header missing",
			logit.String("sandboxUuid", sandboxUUID))
		return getResponse(http.StatusOK, "", map[string]interface{}{
			"verified": false,
		})
	}

	// 查询数据库验证 sandbox_uuid 和 token 是否匹配
	sandboxRuntimeDao := dao.Use(resource.OrmMain).SandboxRuntime
	runtime, queryErr := sandboxRuntimeDao.Where(
		sandboxRuntimeDao.SandboxUUID.Eq(sandboxUUID),
		sandboxRuntimeDao.Token.Eq(xAccessToken),
		sandboxRuntimeDao.Deleted.Is(false),
	).First()

	// 验证结果
	verified := false
	if queryErr == nil && runtime != nil {
		verified = true
		resource.LoggerService.Notice(ctx, "X-Access-Token verification successful",
			logit.String("sandboxUuid", sandboxUUID),
			logit.Int64("sandboxId", runtime.ID))
	} else {
		resource.LoggerService.Warning(ctx, "X-Access-Token verification failed",
			logit.String("sandboxUuid", sandboxUUID),
			logit.String("error", fmt.Sprintf("%v", queryErr)))
	}

	// 构造返回结果
	result := map[string]interface{}{
		"verified": verified,
	}

	return getResponse(http.StatusOK, "", result)
}

// SetNotificationMessage 设置沙箱启动通知话术
// POST /sandboxes/notification/message
// 请求体: {"preStartMessage": "启动前话术", "postStartMessage": "启动后话术", "users": ["user1", "user2", "all"]}
// 所有字段都是可选的，至少需要提供一个
func (h *SandboxHandler) SetNotificationMessage(ctx context.Context, r ghttp.Request) ghttp.Response {
	// 校验 ACCESS-TOKEN
	accessToken, hasToken := r.Header("ACCESS-TOKEN")
	if !hasToken || accessToken == "" {
		return getResponse(http.StatusUnauthorized, "ACCESS-TOKEN header is required", nil)
	}

	// 从配置文件读取全局 access token 并校验
	authApi := devopsauth.GetAuthApi()
	if authApi.Config.GlobalAccessToken == "" {
		resource.LoggerService.Error(ctx, "GlobalAccessToken not configured in devops_auth.toml")
		return getResponse(http.StatusInternalServerError, "Server configuration error", nil)
	}

	if accessToken != authApi.Config.GlobalAccessToken {
		return getResponse(http.StatusForbidden, "ACCESS-TOKEN validation failed", nil)
	}

	// 读取请求体
	body, err := io.ReadAll(r.Body())
	if err != nil {
		return getResponse(http.StatusBadRequest, "Failed to read request body", nil)
	}

	var reqBody struct {
		PreStartMessage  string   `json:"preStartMessage"`  // 启动前话术（可选）
		PostStartMessage string   `json:"postStartMessage"` // 启动后话术（可选）
		Users            []string `json:"users"`            // 小流量用户列表（可选）
	}

	if err := json.Unmarshal(body, &reqBody); err != nil {
		return getResponse(http.StatusBadRequest, "Invalid JSON format", nil)
	}

	// 调用common包的函数设置通知消息
	if err := common.SetNotificationMessages(ctx, resource.RedisClient, reqBody.PreStartMessage, reqBody.PostStartMessage, reqBody.Users); err != nil {
		resource.LoggerService.Error(ctx, "failed to set notification messages",
			logit.String("error", err.Error()))
		return getResponse(http.StatusBadRequest, err.Error(), nil)
	}

	resource.LoggerService.Notice(ctx, "notification messages configured successfully",
		logit.String("preStartMessage", reqBody.PreStartMessage),
		logit.String("postStartMessage", reqBody.PostStartMessage),
		logit.Int("usersCount", len(reqBody.Users)))

	// 返回成功响应
	config, _, _ := common.GetNotificationMessagesConfig(ctx, resource.RedisClient)
	result := map[string]interface{}{
		"preStartMessage":  config.PreStartMessage,
		"postStartMessage": config.PostStartMessage,
		"users":            config.Users,
		"timestamp":        config.Timestamp,
	}

	return getResponse(http.StatusOK, "Configuration saved successfully", result)
}

// GetNotificationMessage 获取沙箱启动通知话术配置
// GET /sandboxes/notification/message
func (h *SandboxHandler) GetNotificationMessage(ctx context.Context, r ghttp.Request) ghttp.Response {
	// 校验 ACCESS-TOKEN
	accessToken, hasToken := r.Header("ACCESS-TOKEN")
	if !hasToken || accessToken == "" {
		return getResponse(http.StatusUnauthorized, "ACCESS-TOKEN header is required", nil)
	}

	// 从配置文件读取全局 access token 并校验
	authApi := devopsauth.GetAuthApi()
	if authApi.Config.GlobalAccessToken == "" {
		resource.LoggerService.Error(ctx, "GlobalAccessToken not configured in devops_auth.toml")
		return getResponse(http.StatusInternalServerError, "Server configuration error", nil)
	}

	if accessToken != authApi.Config.GlobalAccessToken {
		return getResponse(http.StatusForbidden, "ACCESS-TOKEN validation failed", nil)
	}

	// 调用common包的函数获取通知消息配置
	config, isDefault, err := common.GetNotificationMessagesConfig(ctx, resource.RedisClient)
	if err != nil {
		resource.LoggerService.Error(ctx, "failed to get notification messages",
			logit.String("error", err.Error()))
	}

	// 返回配置
	result := map[string]interface{}{
		"preStartMessage":         config.PreStartMessage,
		"postStartMessage":        config.PostStartMessage,
		"users":                   config.Users,
		"timestamp":               config.Timestamp,
		"isDefault":               isDefault,
		"defaultPreStartMessage":  common.DefaultPreStartMessage,
		"defaultPostStartMessage": common.DefaultPostStartMessage,
	}
	return getResponse(http.StatusOK, "SUCCESS", result)
}

// GetOrphanedPods 查找集群中存活的孤立 pod
// GET /rest/v1/sandbox/orphaned-pods
func (h *SandboxHandler) GetOrphanedPods(ctx context.Context, r ghttp.Request) ghttp.Response {
	result, err := h.svc.GetOrphanedPods(ctx)
	if err != nil {
		resource.LoggerService.Error(ctx, "get orphaned pods failed",
			logit.String("error", err.Error()))
		return getResponse(http.StatusInternalServerError, err.Error(), nil)
	}
	return getResponse(http.StatusOK, "SUCCESS", result)
}

// GetFuseOwner 根据沙箱UUID查询comateAuthToken和用户名
// POST /sandboxes/fuseOwner
// 请求体: {"sandboxUUID": "xxxx"}
// 响应: {"code": 200, "msg": "success", "data": {"comateAuthToken": "xxxx","username":"foo"}}
func (h *SandboxHandler) GetFuseOwner(ctx context.Context, r ghttp.Request) ghttp.Response {
	// 校验 ACCESS-TOKEN
	accessToken, hasToken := r.Header("ACCESS-TOKEN")
	if !hasToken || accessToken == "" {
		return getResponse(http.StatusUnauthorized, "缺少ACCESS-TOKEN请求头", nil)
	}
	if accessToken != envvar.GetFuseOwnerToken() {
		return getResponse(http.StatusForbidden, "ACCESS-TOKEN校验失败", nil)
	}

	// 读取请求体
	body, err := io.ReadAll(r.Body())
	if err != nil {
		return getResponse(http.StatusBadRequest, "读取请求体失败", nil)
	}

	var req struct {
		SandboxUUID string `json:"sandboxUUID"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return getResponse(http.StatusBadRequest, "请求体格式错误", nil)
	}

	if req.SandboxUUID == "" {
		return getResponse(http.StatusBadRequest, "请传入sandboxUUID", nil)
	}

	// 先查缓存
	if cached, ok := common.GetFuseOwnerFromCache(ctx, resource.RedisClient, resource.LoggerService, req.SandboxUUID); ok {
		result := map[string]interface{}{
			"comateAuthToken": cached.ComateAuthToken,
			"username":        cached.Username,
		}
		return getResponse(http.StatusOK, "success", result)
	}

	// 缓存未命中，查询数据库
	sandboxRuntimeDao := dao.Use(resource.OrmMain).SandboxRuntime
	runtime, queryErr := sandboxRuntimeDao.Where(
		sandboxRuntimeDao.SandboxUUID.Eq(req.SandboxUUID),
	).First()
	if queryErr != nil || runtime == nil {
		resource.LoggerService.Error(ctx, "sandbox not found by uuid",
			logit.String("sandboxUUID", req.SandboxUUID),
			logit.String("error", fmt.Sprintf("%v", queryErr)))
		return getResponse(http.StatusNotFound, "未找到该sandboxUUID对应的沙箱实例", nil)
	}

	// 解析 envir_vars JSON 字段，提取 COMATE_AUTH_TOKEN
	var comateAuthToken string
	if runtime.EnvirVars != "" {
		var envirVarsMap map[string]string
		if err := json.Unmarshal([]byte(runtime.EnvirVars), &envirVarsMap); err != nil {
			resource.LoggerService.Error(ctx, "failed to parse envir_vars",
				logit.String("sandboxUUID", req.SandboxUUID),
				logit.String("error", err.Error()))
			// 解析失败时，comateAuthToken 为空字符串
		} else {
			comateAuthToken = envirVarsMap["COMATE_AUTH_TOKEN"]
		}
	}

	// 写入缓存
	fuseOwnerInfo := &common.FuseOwnerInfo{
		ComateAuthToken: comateAuthToken,
		Username:        runtime.CreateUser,
	}
	_ = common.SetFuseOwnerCache(ctx, resource.RedisClient, resource.LoggerService, req.SandboxUUID, fuseOwnerInfo)

	// 构造返回结果
	result := map[string]interface{}{
		"comateAuthToken": comateAuthToken,
		"username":        runtime.CreateUser,
	}

	return getResponse(http.StatusOK, "success", result)
}
