package httpserver

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"icode.baidu.com/baidu/gdp/ghttp"
	"icode.baidu.com/baidu/gdp/logit"

	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/resource"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/service"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util/constant"
)

type ScheduledTaskHandler struct {
	svc service.ScheduledTaskService
}

func NewScheduledTaskHandler(svc service.ScheduledTaskService) *ScheduledTaskHandler {
	return &ScheduledTaskHandler{svc: svc}
}

// CreateScheduledTasks 创建或更新沙箱的定时任务
// POST /rest/v1/sandbox/instances/:sandboxId/scheduled-tasks
func (h *ScheduledTaskHandler) CreateScheduledTasks(ctx context.Context, r ghttp.Request) ghttp.Response {
	logger := resource.LoggerService

	sandboxIdStr := r.Param(":sandboxId")
	if sandboxIdStr == "" {
		logger.Warning(ctx, "sandboxId is required")
		return getResponse(http.StatusBadRequest, "sandboxId is required", nil)
	}

	sandboxId, err := strconv.ParseInt(sandboxIdStr, 10, 64)
	if err != nil {
		logger.Warning(ctx, "invalid sandboxId", logit.String("sandboxId", sandboxIdStr))
		return getResponse(http.StatusBadRequest, "invalid sandboxId", nil)
	}

	// 权限校验
	k, v := getPermissionBySandboxRuntimeId(ctx, sandboxId)
	if !k {
		return getResponse(http.StatusBadRequest, v, nil)
	}
	if v != constant.RoleAdmin && v != constant.RoleMember && v != constant.RoleSuperAdmin {
		return getResponse(http.StatusForbidden, "用户权限不足", nil)
	}

	var req service.CreateScheduledTasksRequest
	body, err := io.ReadAll(r.Body())
	if err != nil {
		logger.Warning(ctx, "read body error", logit.String("error", err.Error()))
		return getResponse(http.StatusBadRequest, "read body error", nil)
	}

	if err := json.Unmarshal(body, &req); err != nil {
		logger.Warning(ctx, "invalid json", logit.String("error", err.Error()))
		return getResponse(http.StatusBadRequest, "invalid json", nil)
	}

	// 设置 sandboxId（从路径参数获取）
	req.SandboxID = sandboxId

	err = h.svc.CreateScheduledTasks(ctx, req)
	if err != nil {
		logger.Error(ctx, "failed to create scheduled tasks",
			logit.Int64("sandboxId", sandboxId),
			logit.String("error", err.Error()))
		return getResponse(http.StatusInternalServerError, err.Error(), nil)
	}

	return getResponse(http.StatusOK, "scheduled tasks created successfully", nil)
}

// GetScheduledTasks 获取沙箱的定时任务列表
// GET /rest/v1/sandbox/instances/:sandboxId/scheduled-tasks
func (h *ScheduledTaskHandler) GetScheduledTasks(ctx context.Context, r ghttp.Request) ghttp.Response {
	logger := resource.LoggerService

	sandboxIdStr := r.Param(":sandboxId")
	if sandboxIdStr == "" {
		logger.Warning(ctx, "sandboxId is required")
		return getResponse(http.StatusBadRequest, "sandboxId is required", nil)
	}

	sandboxId, err := strconv.ParseInt(sandboxIdStr, 10, 64)
	if err != nil {
		logger.Warning(ctx, "invalid sandboxId", logit.String("sandboxId", sandboxIdStr))
		return getResponse(http.StatusBadRequest, "invalid sandboxId", nil)
	}

	// 权限校验
	k, v := getPermissionBySandboxRuntimeId(ctx, sandboxId)
	if !k {
		return getResponse(http.StatusBadRequest, v, nil)
	}
	if v != constant.RoleAdmin && v != constant.RoleMember && v != constant.RoleSuperAdmin {
		return getResponse(http.StatusForbidden, "用户权限不足", nil)
	}

	tasks, err := h.svc.GetScheduledTasks(ctx, sandboxId)
	if err != nil {
		logger.Error(ctx, "failed to get scheduled tasks",
			logit.Int64("sandboxId", sandboxId),
			logit.String("error", err.Error()))
		return getResponse(http.StatusInternalServerError, err.Error(), nil)
	}

	return getResponse(http.StatusOK, "get scheduled tasks successfully", tasks)
}

// DeleteScheduledTask 删除单个定时任务
// DELETE /rest/v1/sandbox/scheduled-tasks/:taskID
func (h *ScheduledTaskHandler) DeleteScheduledTask(ctx context.Context, r ghttp.Request) ghttp.Response {
	logger := resource.LoggerService

	taskIDStr := r.Param(":taskID")
	if taskIDStr == "" {
		logger.Warning(ctx, "taskID is required")
		return getResponse(http.StatusBadRequest, "taskID is required", nil)
	}

	taskID, err := strconv.ParseInt(taskIDStr, 10, 64)
	if err != nil {
		logger.Warning(ctx, "invalid taskID", logit.String("taskID", taskIDStr))
		return getResponse(http.StatusBadRequest, "invalid taskID", nil)
	}

	// 权限校验 - 需要先查询任务获取 sandboxId
	k, v := getPermissionByScheduledTaskId(ctx, taskID)
	if !k {
		return getResponse(http.StatusBadRequest, v, nil)
	}
	if v != constant.RoleAdmin && v != constant.RoleMember && v != constant.RoleSuperAdmin {
		return getResponse(http.StatusForbidden, "用户权限不足", nil)
	}

	err = h.svc.DeleteScheduledTask(ctx, taskID)
	if err != nil {
		logger.Error(ctx, "failed to delete scheduled task",
			logit.Int64("taskID", taskID),
			logit.String("error", err.Error()))
		return getResponse(http.StatusInternalServerError, err.Error(), nil)
	}

	return getResponse(http.StatusOK, "scheduled task deleted successfully", nil)
}

// DeleteScheduledTasksBySandbox 删除沙箱的所有定时任务
// DELETE /rest/v1/sandbox/instances/:sandboxId/scheduled-tasks
func (h *ScheduledTaskHandler) DeleteScheduledTasksBySandbox(ctx context.Context, r ghttp.Request) ghttp.Response {
	logger := resource.LoggerService

	sandboxIdStr := r.Param(":sandboxId")
	if sandboxIdStr == "" {
		logger.Warning(ctx, "sandboxId is required")
		return getResponse(http.StatusBadRequest, "sandboxId is required", nil)
	}

	sandboxId, err := strconv.ParseInt(sandboxIdStr, 10, 64)
	if err != nil {
		logger.Warning(ctx, "invalid sandboxId", logit.String("sandboxId", sandboxIdStr))
		return getResponse(http.StatusBadRequest, "invalid sandboxId", nil)
	}

	// 权限校验
	k, v := getPermissionBySandboxRuntimeId(ctx, sandboxId)
	if !k {
		return getResponse(http.StatusBadRequest, v, nil)
	}
	if v != constant.RoleAdmin && v != constant.RoleMember && v != constant.RoleSuperAdmin {
		return getResponse(http.StatusForbidden, "用户权限不足", nil)
	}

	err = h.svc.DeleteScheduledTasksBySandbox(ctx, sandboxId)
	if err != nil {
		logger.Error(ctx, "failed to delete scheduled tasks",
			logit.Int64("sandboxId", sandboxId),
			logit.String("error", err.Error()))
		return getResponse(http.StatusInternalServerError, err.Error(), nil)
	}

	return getResponse(http.StatusOK, "scheduled tasks deleted successfully", nil)
}
