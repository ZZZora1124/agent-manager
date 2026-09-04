package httpserver

import (
	"context"
	"encoding/json"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/dao"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/mcpcenter"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/resource"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/service"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util/constant"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util/response"
	"icode.baidu.com/baidu/gdp/ghttp"
	"io"
	"net/http"
	"strconv"
)

type TemplateHandler struct {
	svc service.TemplateService
}

func NewTemplateHandler(svc service.TemplateService) *TemplateHandler {
	return &TemplateHandler{svc: svc}
}

// 1. 新增模板  POST /rest/v1/sandbox/templates
func (h *TemplateHandler) CreateTemplate(ctx context.Context, r ghttp.Request) ghttp.Response {
	var req service.CreateTemplateRequest

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
	// AppID == 2071(workspaceId = 6017) 时,只允许管理员创建模板,成员没有权限
	if req.AppID == 2071 && v == constant.RoleMember {
		return getResponse(http.StatusForbidden, "该应用只有管理员才能创建模板", nil)
	}
	resp, err := h.svc.CreateTemplate(ctx, req)
	if err != nil {
		return getResponse(http.StatusInternalServerError, err.Error(), resp)
	}
	return getResponse(http.StatusOK, "SUCCESS", resp)
}

// 2. 删除模板  DELETE /rest/v1/sandbox/templates/:templateId
func (h *TemplateHandler) DeleteTemplate(ctx context.Context, r ghttp.Request) ghttp.Response {
	idStr := r.Param(":templateId") // 取出字符串
	if idStr == "" {
		return getResponse(http.StatusBadRequest, "请传入沙箱应用Id", nil)
	}
	id, err := strconv.Atoi(idStr)
	if err != nil {
		return getResponse(http.StatusBadRequest, "无效的沙箱应用Id", nil)
	}
	k, v := getPermissionByTemplate(ctx, int32(id))
	if !k {
		return getResponse(http.StatusBadRequest, v, nil)
	}
	if v == constant.RoleMember {
		return getResponse(http.StatusForbidden, "用户权限不足", nil)
	}
	_, err = h.svc.DeleteTemplate(ctx, id)
	if err != nil {
		return getResponse(http.StatusInternalServerError, err.Error(), nil)
	}
	return getResponse(http.StatusOK, "SUCCESS", nil)
}

// 3. 单条查询  GET /rest/v1/sandbox/templates/:templateId
func (h *TemplateHandler) SelectTemplate(ctx context.Context, r ghttp.Request) ghttp.Response {
	idStr := r.Param(":templateId") // 取出字符串
	if idStr == "" {
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
	resp, err := h.svc.SelectTemplate(ctx, id)
	if err != nil {
		return getResponse(http.StatusInternalServerError, err.Error(), nil)
	}
	return getResponse(http.StatusOK, "SUCCESS", resp)
}

// 4. 根据 appID 批量查  GET /rest/v1/sandbox/templates
func (h *TemplateHandler) SelectTemplateAll(ctx context.Context, r ghttp.Request) ghttp.Response {
	idStr, ok := r.Query("appId") // 取出字符串
	if !ok {
		return getResponse(http.StatusBadRequest, "请传入appId", nil)
	}
	id, err := strconv.Atoi(idStr)
	if err != nil || id < 0 {
		return getResponse(http.StatusBadRequest, "无效的appId", nil)
	}
	k, v := getPermissionByAppID(ctx, int32(id))
	if !k {
		return getResponse(http.StatusBadRequest, v, nil)
	}
	if v != constant.RoleAdmin && v != constant.RoleMember && v != constant.RoleSuperAdmin {
		return getResponse(http.StatusForbidden, "用户权限不足", nil)
	}
	resp, err := h.svc.SelecTemplateAll(ctx, id)
	if err != nil {
		return getResponse(http.StatusInternalServerError, err.Error(), nil)
	}
	return getResponse(http.StatusOK, "SUCCESS", resp)
}

// 5. 公共模板  GET /rest/v1/sandbox/common/template
func (h *TemplateHandler) GetCommonTemplate(ctx context.Context, r ghttp.Request) ghttp.Response {
	resp, err := h.svc.GetCommonTemplate(ctx)
	if err != nil {
		return getResponse(http.StatusInternalServerError, err.Error(), nil)
	}
	return getResponse(http.StatusOK, "SUCCESS", resp)
}

// 6. 编辑模板  PUT /rest/v1/sandbox/templates
func (h *TemplateHandler) UpdateTemplate(ctx context.Context, r ghttp.Request) ghttp.Response {
	var req service.CreateTemplateRequest

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
	resp, err := h.svc.UpdateTemplate(ctx, req, v)
	if err != nil {
		return getResponse(http.StatusInternalServerError, err.Error(), resp)
	}
	return getResponse(http.StatusOK, "SUCCESS", resp)
}

func getResponse(code int, msg string, data interface{}) ghttp.Response {
	return response.NewGResponse(code, map[string]interface{}{
		"code": code,
		"msg":  msg,
		"data": data,
	})
}

func getPermissionByAppID(ctx context.Context, appID int32) (bool, string) {
	appDao := dao.Use(resource.OrmMain).App
	app, err := appDao.Where(appDao.ID.Eq(appID)).First()
	if err != nil || app == nil {
		return false, "无效的appId"
	}
	u := ctx.Value(constant.CtxLoginUserKey).(string)
	k, v := mcpcenter.GetMcpCenterApi().GetWorkspaceRole(int(app.WorkspaceID), u)
	if app.WorkspaceID == 6017 {
		if v != constant.RoleMember && v != constant.RoleSuperAdmin && v != constant.RoleAdmin {
			return true, constant.RoleMember
		}
	}
	return k, v
}

func getPermissionByTemplate(ctx context.Context, templateID int32) (bool, string) {
	templateDao := dao.Use(resource.OrmMain).Template
	tpl, err := templateDao.Where(templateDao.ID.Eq(templateID)).First()
	if err != nil || tpl == nil {
		return false, "无效的沙箱应用Id"
	}
	return getPermissionByAppID(ctx, tpl.AppID)
}
