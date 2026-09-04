package httpserver

import (
	"context"
	"encoding/json"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/dao"
	"io"
	"net/http"

	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/common"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/resource"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/service"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util/constant"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util/response"
	"icode.baidu.com/baidu/gdp/ghttp"
)

type ImageSmallHandler struct {
	imageSvc service.ImageService
}

func NewImageSmallHandler(imageSvc service.ImageService) *ImageSmallHandler {
	return &ImageSmallHandler{
		imageSvc: imageSvc,
	}
}

type SetImageSmallRequest struct {
	Template    string   `json:"template"`
	Users       []string `json:"users"`
	Departments []string `json:"departments"`
	Sandboxes   []string `json:"sandboxes"`
	Image       string   `json:"image"`
	// 每个用户/部门/沙箱指定各自的镜像，优先级高于 Users+Image 等旧字段
	UserImages       map[string]string `json:"userImages,omitempty"`
	DepartmentImages map[string]string `json:"departmentImages,omitempty"`
	SandboxImages    map[string]string `json:"sandboxImages,omitempty"`
	Mode             string            `json:"mode"` // "merge" 或 "replace"，默认 "merge"
}

type UpdateImageByTplIDRequest struct {
	Template string `json:"template"`
	Image    string `json:"image"`
}

// SetImageSmall 设置按模版ID的小流量镜像配置 POST /api/image/small
func (h *ImageSmallHandler) SetImageSmall(ctx context.Context, r ghttp.Request) ghttp.Response {

	var req SetImageSmallRequest
	body, err := io.ReadAll(r.Body())
	if err != nil {
		return response.NewGResponse(http.StatusBadRequest, map[string]interface{}{
			"code": http.StatusBadRequest,
			"msg":  "read body error: " + err.Error(),
		})
	}

	if err := json.Unmarshal(body, &req); err != nil {
		return response.NewGResponse(http.StatusBadRequest, map[string]interface{}{
			"code": http.StatusBadRequest,
			"msg":  "invalid json",
		})
	}

	if len(req.Template) <= 0 {
		return response.NewGResponse(http.StatusBadRequest, map[string]interface{}{
			"code": http.StatusBadRequest,
			"msg":  "tplID is required",
		})
	}

	hasOldFields := len(req.Users) > 0 || len(req.Departments) > 0 || len(req.Sandboxes) > 0
	hasNewFields := len(req.UserImages) > 0 || len(req.DepartmentImages) > 0 || len(req.SandboxImages) > 0
	if hasOldFields && req.Image == "" {
		return response.NewGResponse(http.StatusBadRequest, map[string]interface{}{
			"code": http.StatusBadRequest,
			"msg":  "image is required when using users/departments/sandboxes fields",
		})
	}
	if !hasOldFields && !hasNewFields {
		return response.NewGResponse(http.StatusBadRequest, map[string]interface{}{
			"code": http.StatusBadRequest,
			"msg":  "no targets specified, provide users/departments/sandboxes or userImages/departmentImages/sandboxImages",
		})
	}

	appID := ctx.Value(constant.CtxAppIDKey).(int32)

	templateDao := dao.Use(resource.OrmMain).Template

	template, err := templateDao.Where(templateDao.AppID.Eq(appID), templateDao.Name.Eq(req.Template)).First()
	if template == nil || err != nil {
		return response.NewGResponse(http.StatusBadRequest, map[string]interface{}{
			"code": http.StatusBadRequest,
			"msg":  "template not found",
		})
	}

	mode := req.Mode
	if mode == "" {
		mode = "merge"
	}

	err = common.SetImageSmallByTplID(ctx, resource.RedisClient, resource.LoggerService, appID, req.Template, req.Users, req.Departments, req.Sandboxes,
		req.Image, req.UserImages, req.DepartmentImages, req.SandboxImages, mode)
	if err != nil {
		return response.NewGResponse(http.StatusInternalServerError, map[string]interface{}{
			"code": http.StatusInternalServerError,
			"msg":  "failed to set image small config: " + err.Error(),
		})
	}

	return response.NewGResponse(http.StatusOK, map[string]interface{}{
		"code": http.StatusOK,
		"msg":  "SUCCESS",
	})
}

// GetImageSmall 查询按模版ID的小流量镜像配置 GET /api/image/small
func (h *ImageSmallHandler) GetImageSmall(ctx context.Context, r ghttp.Request) ghttp.Response {

	templateID, ok := r.Query("template")
	if !ok || templateID == "" {
		return response.NewGResponse(http.StatusBadRequest, map[string]interface{}{
			"code": http.StatusBadRequest,
			"msg":  "tplID is required",
		})
	}

	appID := ctx.Value(constant.CtxAppIDKey).(int32)

	data, err := common.GetImageSmallByTplID(ctx, resource.RedisClient, resource.LoggerService, appID, templateID)
	if err != nil {
		return response.NewGResponse(http.StatusInternalServerError, map[string]interface{}{
			"code": http.StatusInternalServerError,
			"msg":  "failed to get image small config: " + err.Error(),
		})
	}

	return response.NewGResponse(http.StatusOK, map[string]interface{}{
		"code": http.StatusOK,
		"msg":  "SUCCESS",
		"data": data,
	})
}

// UpdateImageByTplID 更新模版镜像地址 PUT /api/image/small
func (h *ImageSmallHandler) UpdateImageByTplID(ctx context.Context, r ghttp.Request) ghttp.Response {

	var req UpdateImageByTplIDRequest
	body, err := io.ReadAll(r.Body())
	if err != nil {
		return response.NewGResponse(http.StatusBadRequest, map[string]interface{}{
			"code": http.StatusBadRequest,
			"msg":  "read body error: " + err.Error(),
		})
	}

	if err := json.Unmarshal(body, &req); err != nil {
		return response.NewGResponse(http.StatusBadRequest, map[string]interface{}{
			"code": http.StatusBadRequest,
			"msg":  "invalid json",
		})
	}

	if len(req.Template) <= 0 {
		return response.NewGResponse(http.StatusBadRequest, map[string]interface{}{
			"code": http.StatusBadRequest,
			"msg":  "tplID is required",
		})
	}

	if req.Image == "" {
		return response.NewGResponse(http.StatusBadRequest, map[string]interface{}{
			"code": http.StatusBadRequest,
			"msg":  "image is required",
		})
	}

	if !isValidDockerImage(req.Image) {
		return response.NewGResponse(http.StatusBadRequest, map[string]interface{}{
			"code": http.StatusBadRequest,
			"msg":  "invalid image format, expected docker image address like: registry/namespace/image:tag",
		})
	}

	appID := ctx.Value(constant.CtxAppIDKey).(int32)
	templateDao := dao.Use(resource.OrmMain).Template

	template, err := templateDao.Where(templateDao.AppID.Eq(appID), templateDao.Name.Eq(req.Template)).First()
	if template == nil || err != nil {
		return response.NewGResponse(http.StatusBadRequest, map[string]interface{}{
			"code": http.StatusBadRequest,
			"msg":  "template not found",
		})
	}

	if appID != template.AppID {
		return response.NewGResponse(http.StatusUnauthorized, map[string]interface{}{
			"code": http.StatusUnauthorized,
			"msg":  "Unauthorized: Invalid token",
		})
	}

	err = h.imageSvc.UpdateImageByTplID(appID, req.Template, req.Image)
	if err != nil {
		return response.NewGResponse(http.StatusInternalServerError, map[string]interface{}{
			"code": http.StatusInternalServerError,
			"msg":  "failed to update image: " + err.Error(),
		})
	}

	return response.NewGResponse(http.StatusOK, map[string]interface{}{
		"code": http.StatusOK,
		"msg":  "SUCCESS",
		"data": map[string]interface{}{
			"template": req.Template,
			"image":    req.Image,
		},
	})
}

// parsePositiveInt32 解析正整数到 int32
func parsePositiveInt32(s string, result *int32) (bool, error) {
	var n int
	for _, c := range s {
		if c < '0' || c > '9' {
			return false, nil
		}
		n = n*10 + int(c-'0')
		if n > 2147483647 {
			return false, nil
		}
	}
	if n == 0 {
		return false, nil
	}
	*result = int32(n)
	return true, nil
}
