package httpserver

import (
	"context"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/service"
	"icode.baidu.com/baidu/gdp/ghttp"
	"net/http"
)

type ImageHandler struct {
	svc service.ImageService
}

func NewImageHandler(svc service.ImageService) *ImageHandler {
	return &ImageHandler{svc: svc}
}

// 查询公共镜像模版 GET /rest/v1/sandbox/common/image
func (h *ImageHandler) GetPublicImage(ctx context.Context, r ghttp.Request) ghttp.Response {
	resp, err := h.svc.GetPublicImage()
	if err != nil {
		return getResponse(http.StatusInternalServerError, err.Error(), resp)
	}
	return getResponse(http.StatusOK, "SUCCESS", resp)
}
