package httpserver

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/dao"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/instance"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/resource"
	"icode.baidu.com/baidu/gdp/ghttp"
	"icode.baidu.com/baidu/gdp/logit"
)

// InstanceHandler 实例操作处理器
// 负责处理所有与沙箱实例内部操作相关的 HTTP 请求
type InstanceHandler struct {
	skillInstaller    *instance.SkillInstaller
	gatewayChecker    *instance.GatewayChecker
	openClawConfigGen *instance.OpenClawConfigGenerator
}

// NewInstanceHandler 创建 InstanceHandler 实例
func NewInstanceHandler() *InstanceHandler {
	db := dao.Use(resource.OrmMain)
	return &InstanceHandler{
		skillInstaller:    instance.NewSkillInstaller(*db),
		gatewayChecker:    instance.NewGatewayChecker(*db),
		openClawConfigGen: instance.NewOpenClawConfigGenerator(*db),
	}
}

// StandardResponse 标准响应格式
type StandardResponse struct {
	Code    int    `json:"code"`    // 响应码
	Message string `json:"message"` // 响应消息
	Data    any    `json:"data"`    // 响应数据
}

// ========== Skill 安装相关 ==========

// InstallSkillRequest 安装 skill 的 HTTP 请求参数
type InstallSkillRequest struct {
	SandboxUUID string `json:"sandboxUUID"` // 沙箱唯一标识
	DownloadURL string `json:"downloadURL"` // skill 下载链接
	SkillName   string `json:"skillName"`   // skill 标识名称
}

// InstallSkill 安装 skill 到指定的沙箱容器
// POST /rest/v1/sandbox/instances/:sandboxId/skills/install
func (h *InstanceHandler) InstallSkill(ctx context.Context, r ghttp.Request) ghttp.Response {
	logger := resource.LoggerService

	// 读取请求体
	body, err := io.ReadAll(r.Body())
	if err != nil {
		logger.Error(ctx, "read request body failed", logit.String("error", err.Error()))
		return ghttp.NewJSONResponse(http.StatusBadRequest, StandardResponse{
			Code:    http.StatusBadRequest,
			Message: "读取请求体失败",
			Data:    nil,
		})
	}

	// 解析请求参数
	var req InstallSkillRequest
	if err := json.Unmarshal(body, &req); err != nil {
		logger.Error(ctx, "parse request json failed", logit.String("error", err.Error()))
		return ghttp.NewJSONResponse(http.StatusBadRequest, StandardResponse{
			Code:    http.StatusBadRequest,
			Message: "请求参数格式错误",
			Data:    nil,
		})
	}

	// 记录请求日志
	logger.Notice(ctx, "received skill install request",
		logit.String("sandboxUUID", req.SandboxUUID),
		logit.String("skillName", req.SkillName),
		logit.String("downloadURL", req.DownloadURL))

	// 调用 instance 层安装 skill
	params := instance.InstallSkillParams{
		SandboxUUID: req.SandboxUUID,
		DownloadURL: req.DownloadURL,
		SkillName:   req.SkillName,
	}

	err = h.skillInstaller.InstallSkill(ctx, params)
	if err != nil {
		logger.Error(ctx, "install skill failed",
			logit.String("sandboxUUID", req.SandboxUUID),
			logit.String("skillName", req.SkillName),
			logit.String("error", err.Error()))
		return ghttp.NewJSONResponse(http.StatusInternalServerError, StandardResponse{
			Code:    http.StatusInternalServerError,
			Message: "安装失败: " + err.Error(),
			Data:    nil,
		})
	}

	// 安装成功
	logger.Notice(ctx, "skill install request completed successfully",
		logit.String("sandboxUUID", req.SandboxUUID),
		logit.String("skillName", req.SkillName))

	return ghttp.NewJSONResponse(http.StatusOK, StandardResponse{
		Code:    http.StatusOK,
		Message: "Skill 安装成功",
		Data: map[string]string{
			"sandboxUUID": req.SandboxUUID,
			"skillName":   req.SkillName,
		},
	})
}

// ========== Gateway 健康检测相关 ==========

// CheckGatewayRequest 检测 gateway 的 HTTP 请求参数
type CheckGatewayRequest struct {
	SandboxUUID string `json:"sandboxUUID"` // 沙箱唯一标识
}

// CheckGatewayResponseData 检测 gateway 的响应数据
type CheckGatewayResponseData struct {
	IsRunning             bool   `json:"isRunning"`             // gateway 是否正在运行
	CheckMethod           string `json:"checkMethod"`           // 检测方式: "process" 或 "port" 或 "both"
	OpenclawProcessExists bool   `json:"openclawProcessExists"` // openclaw 进程是否存在
	OpenclawPortListening bool   `json:"openclawPortListening"` // openclaw 端口是否监听
	InfoflowPortListening bool   `json:"infoflowPortListening"` // infoflow 端口是否监听
	Details               string `json:"details"`               // 详细信息
}

// CheckGateway 综合检测 openclaw-gateway 是否正常运行
// POST /rest/v1/sandbox/instances/:sandboxId/gateway/check
func (h *InstanceHandler) CheckGateway(ctx context.Context, r ghttp.Request) ghttp.Response {
	logger := resource.LoggerService

	// 从 URL 路径参数获取 sandboxId
	sandboxID := r.Param(":sandboxId")
	if sandboxID == "" {
		// 尝试从请求体获取
		body, err := io.ReadAll(r.Body())
		if err != nil {
			logger.Error(ctx, "read request body failed", logit.String("error", err.Error()))
			return ghttp.NewJSONResponse(http.StatusBadRequest, StandardResponse{
				Code:    http.StatusBadRequest,
				Message: "读取请求体失败",
				Data:    nil,
			})
		}

		var req CheckGatewayRequest
		if err := json.Unmarshal(body, &req); err != nil {
			logger.Error(ctx, "parse request json failed", logit.String("error", err.Error()))
			return ghttp.NewJSONResponse(http.StatusBadRequest, StandardResponse{
				Code:    http.StatusBadRequest,
				Message: "请求参数格式错误",
				Data:    nil,
			})
		}
		sandboxID = req.SandboxUUID
	}

	if sandboxID == "" {
		return ghttp.NewJSONResponse(http.StatusBadRequest, StandardResponse{
			Code:    http.StatusBadRequest,
			Message: "sandboxUUID 不能为空",
			Data:    nil,
		})
	}

	// 记录请求日志
	logger.Notice(ctx, "received gateway check request",
		logit.String("sandboxUUID", sandboxID))

	// 调用 instance 层检测 gateway
	params := instance.CheckGatewayParams{
		SandboxUUID: sandboxID,
	}

	result, err := h.gatewayChecker.CheckGateway(ctx, params)
	if err != nil {
		logger.Error(ctx, "check gateway failed",
			logit.String("sandboxUUID", sandboxID),
			logit.String("error", err.Error()))
		return ghttp.NewJSONResponse(http.StatusInternalServerError, StandardResponse{
			Code:    http.StatusInternalServerError,
			Message: "检测失败: " + err.Error(),
			Data:    nil,
		})
	}

	// 检测完成
	logger.Notice(ctx, "gateway check request completed successfully",
		logit.String("sandboxUUID", sandboxID),
		logit.Bool("isRunning", result.IsRunning),
		logit.Bool("openclawProcessExists", result.OpenclawProcessExists),
		logit.Bool("openclawPortListening", result.OpenclawPortListening),
		logit.Bool("infoflowPortListening", result.InfoflowPortListening))

	responseData := CheckGatewayResponseData{
		IsRunning:             result.IsRunning,
		CheckMethod:           result.CheckMethod,
		OpenclawProcessExists: result.OpenclawProcessExists,
		OpenclawPortListening: result.OpenclawPortListening,
		InfoflowPortListening: result.InfoflowPortListening,
		Details:               result.Details,
	}

	var message string
	if result.IsRunning {
		message = "openclaw-gateway 运行正常"
	} else {
		message = "openclaw-gateway 未正常运行"
	}

	return ghttp.NewJSONResponse(http.StatusOK, StandardResponse{
		Code:    http.StatusOK,
		Message: message,
		Data:    responseData,
	})
}

// ========== OpenClaw 配置写入相关 ==========

// WriteOpenClawConfigRequest 写入 openclaw.json 配置的 HTTP 请求参数
type WriteOpenClawConfigRequest struct {
	SandboxUUID string `json:"sandboxUUID"` // 沙箱唯一标识
}

// WriteOpenClawConfig 生成并写入用户的 openclaw.json 配置到实例
// POST /rest/v1/sandbox/instances/:sandboxId/openclaw-config/write
func (h *InstanceHandler) WriteOpenClawConfig(ctx context.Context, r ghttp.Request) ghttp.Response {
	logger := resource.LoggerService

	// 从 URL 路径参数获取 sandboxId
	sandboxID := r.Param(":sandboxId")
	if sandboxID == "" {
		// 尝试从请求体获取
		body, err := io.ReadAll(r.Body())
		if err != nil {
			logger.Error(ctx, "read request body failed", logit.String("error", err.Error()))
			return ghttp.NewJSONResponse(http.StatusBadRequest, StandardResponse{
				Code:    http.StatusBadRequest,
				Message: "读取请求体失败",
				Data:    nil,
			})
		}

		var req WriteOpenClawConfigRequest
		if err := json.Unmarshal(body, &req); err != nil {
			logger.Error(ctx, "parse request json failed", logit.String("error", err.Error()))
			return ghttp.NewJSONResponse(http.StatusBadRequest, StandardResponse{
				Code:    http.StatusBadRequest,
				Message: "请求参数格式错误",
				Data:    nil,
			})
		}
		sandboxID = req.SandboxUUID
	}

	if sandboxID == "" {
		return ghttp.NewJSONResponse(http.StatusBadRequest, StandardResponse{
			Code:    http.StatusBadRequest,
			Message: "sandboxUUID 不能为空",
			Data:    nil,
		})
	}

	// 记录请求日志
	logger.Notice(ctx, "received write openclaw config request",
		logit.String("sandboxUUID", sandboxID))

	// 调用 instance 层写入配置
	err := h.openClawConfigGen.WriteOpenClawConfigToInstance(ctx, sandboxID)
	if err != nil {
		logger.Error(ctx, "write openclaw config failed",
			logit.String("sandboxUUID", sandboxID),
			logit.String("error", err.Error()))
		return ghttp.NewJSONResponse(http.StatusInternalServerError, StandardResponse{
			Code:    http.StatusInternalServerError,
			Message: "写入配置失败: " + err.Error(),
			Data:    nil,
		})
	}

	// 写入成功
	logger.Notice(ctx, "write openclaw config request completed successfully",
		logit.String("sandboxUUID", sandboxID))

	return ghttp.NewJSONResponse(http.StatusOK, StandardResponse{
		Code:    http.StatusOK,
		Message: "OpenClaw 配置写入成功，gateway 已重启",
		Data: map[string]string{
			"sandboxUUID": sandboxID,
			"configPath":  "/home/gem/.openclaw/openclaw.json",
		},
	})
}
