package httpserver

import (
	"context"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/mcpcenter"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/service"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util/constant"
	"icode.baidu.com/baidu/gdp/ghttp"
	"net/http"
	"strconv"
)

type AppHandler struct {
	svc service.AppService
}

func NewAppHandler(svc service.AppService) *AppHandler {
	return &AppHandler{svc: svc}
}

// 平台和空间绑定关系查询 GET /rest/v1/sandbox/band/enquiry/:workspaceId
func (h *AppHandler) GetAppId(ctx context.Context, r ghttp.Request) ghttp.Response {
	idStr := r.Param(":workspaceId") // 取出字符串
	if idStr == "" {
		return getResponse(http.StatusBadRequest, "请传入空间Id", nil)
	}
	id, err := strconv.Atoi(idStr)
	if err != nil {
		return getResponse(http.StatusBadRequest, "无效的空间Id", nil)
	}
	// 6017是公共环境，直接放行
	if id != 6017 {
		k, v := mcpcenter.GetMcpCenterApi().GetWorkspaceRole(id, ctx.Value(constant.CtxLoginUserKey).(string))
		if !k {
			return getResponse(http.StatusBadRequest, v, nil)
		}
		if v != constant.RoleAdmin && v != constant.RoleMember && v != constant.RoleSuperAdmin {
			return getResponse(http.StatusForbidden, "用户权限不足", nil)
		}
	}
	resp, err := h.svc.GetAppId(ctx, id)
	if err != nil {
		return getResponse(http.StatusInternalServerError, err.Error(), resp)
	}
	return getResponse(http.StatusOK, "SUCCESS", resp)
}

// 查询appKeys GET /rest/v1/sandbox/{workspaceId}/appKey: workspaceId为路径参数
func (h *AppHandler) GetAppKey(ctx context.Context, r ghttp.Request) ghttp.Response {
	idStr := r.Param(":workspaceId") // 取出字符串
	if idStr == "" {
		return getResponse(http.StatusBadRequest, "请传入空间Id", nil)
	}
	id, err := strconv.Atoi(idStr)
	if err != nil {
		return getResponse(http.StatusBadRequest, "无效的空间Id", nil)
	}
	k, v := mcpcenter.GetMcpCenterApi().GetWorkspaceRole(id, ctx.Value(constant.CtxLoginUserKey).(string))
	if !k {
		return getResponse(http.StatusBadRequest, v, nil)
	}
	if v == constant.RoleMember {
		return getResponse(http.StatusForbidden, "用户权限不足", nil)
	}
	resp, err := h.svc.GetApiKey(id)
	if err != nil {
		return getResponse(http.StatusInternalServerError, err.Error(), resp)
	}
	return getResponse(http.StatusOK, "SUCCESS", resp)
}

// UpdateAccountRequest 更新account请求
type UpdateAccountRequest struct {
	CloudAccountIds []string `json:"cloudAccountIds" binding:"required"`
}

// UpdateAccount 更新app表的account字段 PUT /rest/v1/sandbox/account/:workspaceId
func (h *AppHandler) UpdateAccount(ctx context.Context, r ghttp.Request) ghttp.Response {
	idStr := r.Param(":workspaceId")
	if idStr == "" {
		return getResponse(http.StatusBadRequest, "请传入空间Id", nil)
	}

	id, err := strconv.Atoi(idStr)
	if err != nil {
		return getResponse(http.StatusBadRequest, "无效的空间Id", nil)
	}

	// 权限校验
	k, v := mcpcenter.GetMcpCenterApi().GetWorkspaceRole(id, ctx.Value(constant.CtxLoginUserKey).(string))
	if !k {
		return getResponse(http.StatusBadRequest, v, nil)
	}
	if v != constant.RoleAdmin && v != constant.RoleSuperAdmin {
		return getResponse(http.StatusForbidden, "用户权限不足，仅管理员可操作", nil)
	}

	// 解析请求体
	request := &UpdateAccountRequest{}
	if err := util.Bind(r, request); err != nil {
		return getResponse(http.StatusBadRequest, "请求参数解析失败", nil)
	}

	if len(request.CloudAccountIds) == 0 {
		return getResponse(http.StatusBadRequest, "cloudAccountIds不能为空", nil)
	}

	// 调用service更新account
	err = h.svc.UpdateAccount(ctx, id, request.CloudAccountIds)
	if err != nil {
		return getResponse(http.StatusInternalServerError, err.Error(), nil)
	}

	return getResponse(http.StatusOK, "SUCCESS", map[string]interface{}{
		"workspaceId": id,
		"account":     request.CloudAccountIds[len(request.CloudAccountIds)-1],
	})
}
