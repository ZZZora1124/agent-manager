package httpserver

import (
	"context"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util/response"
	"net/http"

	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/resource"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/service"
	"icode.baidu.com/baidu/gdp/ghttp"
	"icode.baidu.com/baidu/gdp/logit"
)

const clusterToken = "c5b34f80-33f1-4436-a6ec-b7002345bb76"

type ClusterHandler struct {
	svc service.ClusterService
}

func NewClusterHandler(svc service.ClusterService) *ClusterHandler {
	return &ClusterHandler{
		svc: svc,
	}
}

// ClusterResponse 集群操作响应格式
type ClusterResponse struct {
	Code    int    `json:"code"`    // 响应码
	Message string `json:"message"` // 响应消息
	Data    any    `json:"data"`    // 响应数据
}

func checkToken(req ghttp.Request) bool {
	token, has := req.Header("token")
	if !has {
		return false
	}
	return token == clusterToken
}

// ExpandCluster 集群扩容
// @Summary 集群扩容
// @Description 创建指定数量的新节点
// @Tags cluster
// @Accept json
// @Produce json
// @Param request body service.ExpandClusterRequest true "扩容请求"
// @Success 200 {object} ClusterResponse
// @Router /api/cluster/expand [post]
func (h *ClusterHandler) ExpandCluster(ctx context.Context, req ghttp.Request) ghttp.Response {
	logger := resource.LoggerService

	if !checkToken(req) {
		return ghttp.NewJSONResponse(http.StatusUnauthorized, ClusterResponse{
			Code:    http.StatusUnauthorized,
			Message: "token error",
			Data:    nil,
		})
	}

	request := &service.ExpandClusterRequest{}

	if err := util.Bind(req, request); err != nil {
		return response.NewGResponse(http.StatusBadRequest,
			response.ErrParams)
	}

	result, err := h.svc.ExpandCluster(ctx, request)
	if err != nil {
		logger.Error(ctx, "expand cluster failed", logit.String("error", err.Error()))
		return ghttp.NewJSONResponse(http.StatusInternalServerError, ClusterResponse{
			Code:    http.StatusInternalServerError,
			Message: "扩容失败: " + err.Error(),
			Data:    nil,
		})
	}

	logger.Notice(ctx, "expand cluster success", logit.String("cluster_id", request.ClusterID))

	return ghttp.NewJSONResponse(http.StatusOK, ClusterResponse{
		Code:    http.StatusOK,
		Message: "success",
		Data:    result,
	})
}

// SyncClusterStatus 同步集群节点状态
// @Summary 同步集群节点状态
// @Description 同步节点状态并执行网络配置
// @Tags cluster
// @Accept json
// @Produce json
// @Param request body service.SyncClusterStatusRequest true "同步状态请求"
// @Success 200 {object} ClusterResponse
// @Router /api/cluster/sync_status [post]
func (h *ClusterHandler) SyncClusterStatus(ctx context.Context, req ghttp.Request) ghttp.Response {
	logger := resource.LoggerService

	if !checkToken(req) {
		return ghttp.NewJSONResponse(http.StatusUnauthorized, ClusterResponse{
			Code:    http.StatusUnauthorized,
			Message: "token error",
			Data:    nil,
		})
	}

	request := &service.SyncClusterStatusRequest{}

	if err := util.Bind(req, request); err != nil {
		return response.NewGResponse(http.StatusBadRequest,
			response.ErrParams)
	}

	result, err := h.svc.SyncClusterStatus(ctx, request)
	if err != nil {
		logger.Error(ctx, "sync cluster status failed", logit.String("error", err.Error()))
		return ghttp.NewJSONResponse(http.StatusInternalServerError, ClusterResponse{
			Code:    http.StatusInternalServerError,
			Message: "同步状态失败: " + err.Error(),
			Data:    nil,
		})
	}

	logger.Notice(ctx, "sync cluster status success", logit.String("cluster_id", request.ClusterID))

	return ghttp.NewJSONResponse(http.StatusOK, ClusterResponse{
		Code:    http.StatusOK,
		Message: "success",
		Data:    result,
	})
}
