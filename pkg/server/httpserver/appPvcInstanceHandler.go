package httpserver

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strconv"

	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/service"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util/constant"
	"icode.baidu.com/baidu/gdp/ghttp"
)

type AppPvcInstanceHandler struct {
	svc service.AppPvcInstanceService
}

func NewAppPvcInstanceHandler(svc service.AppPvcInstanceService) *AppPvcInstanceHandler {
	return &AppPvcInstanceHandler{svc: svc}
}

type CreateAppPvcInstanceReq struct {
	Name string `json:"name"`
	Size string `json:"size"`
}

// CreateAppPvcInstance POST /pvc/instances
func (h *AppPvcInstanceHandler) CreateAppPvcInstance(ctx context.Context, r ghttp.Request) ghttp.Response {
	body, err := io.ReadAll(r.Body())
	if err != nil {
		return getResponse(http.StatusBadRequest, "请求体读取失败", nil)
	}

	req := &CreateAppPvcInstanceReq{}
	if err = json.Unmarshal(body, req); err != nil {
		return getResponse(http.StatusBadRequest, "请求参数解析失败", nil)
	}

	appID := ctx.Value(constant.CtxAppIDKey).(int32)

	if appID == 0 || req.Name == "" || req.Size == "" {
		return getResponse(http.StatusBadRequest, "appId, name, size 不能为空", nil)
	}

	if !isValidPvcName(req.Name) {
		return getResponse(http.StatusBadRequest, "name 格式不合法：仅支持字母和数字，不能以数字开头，最长64字符", nil)
	}

	sizeVal, err := strconv.Atoi(req.Size)
	if err != nil || sizeVal <= 0 || sizeVal >= 100 {
		return getResponse(http.StatusBadRequest, "size 必须为0到100之间的正整数", nil)
	}
	// 默认单位是 Gi
	req.Size = req.Size + "Gi"

	instance, err := h.svc.CreateAppPvcInstance(ctx, appID, req.Name, req.Size)
	if err != nil {
		return getResponse(http.StatusInternalServerError, err.Error(), nil)
	}

	return getResponse(http.StatusOK, "SUCCESS", instance)
}

type DeleteAppPvcInstanceReq struct {
	AppID int32 `json:"appId"`
}

// DeleteAppPvcInstance DELETE /pvc/instances/:instanceId
func (h *AppPvcInstanceHandler) DeleteAppPvcInstance(ctx context.Context, r ghttp.Request) ghttp.Response {

	appID := ctx.Value(constant.CtxAppIDKey).(int32)

	instanceIDStr := r.Param(":instanceId")
	if instanceIDStr == "" {
		return getResponse(http.StatusBadRequest, "instanceId 不能为空", nil)
	}
	instanceID, err := strconv.ParseInt(instanceIDStr, 10, 64)
	if err != nil {
		return getResponse(http.StatusBadRequest, "无效的 instanceId", nil)
	}

	if appID == 0 {
		return getResponse(http.StatusBadRequest, "appId 不能为空", nil)
	}

	if err = h.svc.DeleteAppPvcInstance(ctx, appID, instanceID); err != nil {
		return getResponse(http.StatusInternalServerError, err.Error(), nil)
	}

	return getResponse(http.StatusOK, "SUCCESS", nil)
}

// ListAppPvcInstances GET /pvc/instances
func (h *AppPvcInstanceHandler) ListAppPvcInstances(ctx context.Context, r ghttp.Request) ghttp.Response {
	appID := ctx.Value(constant.CtxAppIDKey).(int32)
	if appID == 0 {
		return getResponse(http.StatusBadRequest, "appId 不能为空", nil)
	}

	instances, err := h.svc.ListAppPvcInstances(ctx, appID)
	if err != nil {
		return getResponse(http.StatusInternalServerError, err.Error(), nil)
	}

	return getResponse(http.StatusOK, "SUCCESS", instances)
}

var pvcNameRegex = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9]{0,63}$`)

func isValidPvcName(name string) bool {
	return pvcNameRegex.MatchString(name)
}
