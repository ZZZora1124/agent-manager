package instance

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/dao"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/resource"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/model"
	"icode.baidu.com/baidu/gdp/logit"
	"net/http"
	"strings"
	"time"
)

// GatewayChecker 提供 openclaw-gateway 进程检测功能
type GatewayChecker struct {
	db dao.Query
}

// NewGatewayChecker 创建一个新的 GatewayChecker 实例
func NewGatewayChecker(db dao.Query) *GatewayChecker {
	return &GatewayChecker{
		db: db,
	}
}

// CheckGatewayParams 检测 gateway 的参数
type CheckGatewayParams struct {
	SandboxUUID string // 沙箱唯一标识
}

// CheckGatewayResult 检测结果
type CheckGatewayResult struct {
	IsRunning             bool   // gateway 是否正在运行
	CheckMethod           string // 检测方式: "process" 或 "port" 或 "both"
	OpenclawProcessExists bool   // openclaw 进程是否存在（仅当使用进程检测时）
	OpenclawPortListening bool   // openclaw 端口是否监听（仅当使用端口检测时）
	InfoflowPortListening bool   // infoflow 端口(5000)是否监听
	Details               string // 详细信息
}

// executeCommand 执行沙箱内的命令（内部方法）
func (g *GatewayChecker) executeCommand(ctx context.Context, sandboxRuntime *model.SandboxRuntime, command string) (*CheckGatewayResponse, error) {
	logger := resource.LoggerService

	// 构造请求体
	requestBody := CheckGatewayRequest{
		Process: Process{
			Cmd:  "bash",
			Args: []string{"-l", "-c", command},
		},
	}

	requestBodyBytes, err := json.Marshal(requestBody)
	if err != nil {
		logger.Error(ctx, "marshal request body failed", logit.String("error", err.Error()))
		return nil, fmt.Errorf("marshal request body failed: %w", err)
	}

	// 构造请求 URL
	url := fmt.Sprintf(
		"http://%d-%s.agent-sandbox.baidu-int.com/process.Process/StartSync",
		sandboxRuntime.CommandPort,
		sandboxRuntime.SandboxUUID,
	)

	// 创建 HTTP 请求
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(requestBodyBytes))
	if err != nil {
		logger.Error(ctx, "create http request failed", logit.String("error", err.Error()))
		return nil, fmt.Errorf("create http request failed: %w", err)
	}

	// 设置请求头
	req.Header.Set("X-Access-Token", sandboxRuntime.Token)
	req.Header.Set("Content-Type", "application/json")

	// 发送请求
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	logger.Notice(ctx, "sending command to sandbox", logit.String("url", url), logit.String("cmd", command))
	resp, err := client.Do(req)
	if err != nil {
		logger.Error(ctx, "send http request failed", logit.String("error", err.Error()))
		return nil, fmt.Errorf("send http request failed: %w", err)
	}
	defer resp.Body.Close()

	// 检查响应状态码
	if resp.StatusCode != http.StatusOK {
		logger.Error(ctx, "command execution failed", logit.Int("statusCode", resp.StatusCode))
		return nil, fmt.Errorf("command execution failed, http status code: %d", resp.StatusCode)
	}

	// 解析响应体
	var result CheckGatewayResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		logger.Error(ctx, "decode response body failed", logit.String("error", err.Error()))
		return nil, fmt.Errorf("decode response body failed: %w", err)
	}

	return &result, nil
}

// getSandboxRuntime 获取沙箱运行时信息（内部方法）
func (g *GatewayChecker) getSandboxRuntime(ctx context.Context, sandboxUUID string) (*model.SandboxRuntime, error) {
	logger := resource.LoggerService

	sandboxRuntime, err := g.db.SandboxRuntime.WithContext(ctx).
		Where(g.db.SandboxRuntime.SandboxUUID.Eq(sandboxUUID)).
		Where(g.db.SandboxRuntime.Deleted.Is(false)).
		First()
	if err != nil {
		logger.Error(ctx, "query sandbox runtime failed",
			logit.String("sandboxUUID", sandboxUUID),
			logit.String("error", err.Error()))
		return nil, fmt.Errorf("query sandbox runtime failed: %w", err)
	}

	// 检查沙箱状态
	if sandboxRuntime.Status != "running" {
		logger.Warning(ctx, "sandbox status is not running",
			logit.String("sandboxUUID", sandboxUUID),
			logit.String("status", sandboxRuntime.Status))
		return nil, fmt.Errorf("sandbox status is invalid, current status: %s", sandboxRuntime.Status)
	}

	return sandboxRuntime, nil
}

// CheckGatewayByProcess 通过进程检测 openclaw-gateway 是否启动
// 使用命令: ps -aux | grep openclaw-gateway | grep -v grep
func (g *GatewayChecker) CheckGatewayByProcess(ctx context.Context, params CheckGatewayParams) (*CheckGatewayResult, error) {
	logger := resource.LoggerService

	// 参数校验
	if params.SandboxUUID == "" {
		return nil, fmt.Errorf("sandboxUUID cannot be empty")
	}

	// 获取沙箱运行时信息
	sandboxRuntime, err := g.getSandboxRuntime(ctx, params.SandboxUUID)
	if err != nil {
		return nil, err
	}

	// 执行进程检测命令
	checkCmd := "ps -aux | grep openclaw-gateway | grep -v grep"
	logger.Notice(ctx, "checking gateway process",
		logit.String("sandboxUUID", params.SandboxUUID),
		logit.String("cmd", checkCmd))

	result, err := g.executeCommand(ctx, sandboxRuntime, checkCmd)
	if err != nil {
		return nil, err
	}

	// exitCode 为 0 且有输出表示找到进程
	isRunning := result.ExitCode == 0 && len(strings.TrimSpace(result.Stdout)) > 0

	logger.Notice(ctx, "gateway process check completed",
		logit.String("sandboxUUID", params.SandboxUUID),
		logit.Bool("processExists", isRunning),
		logit.Int("exitCode", result.ExitCode))

	return &CheckGatewayResult{
		IsRunning:             isRunning,
		CheckMethod:           "process",
		OpenclawProcessExists: isRunning,
		Details:               result.Stdout,
	}, nil
}

// CheckGatewayByPort 通过端口检测 openclaw-gateway 是否启动
// 使用命令: netstat -anlp | grep 8081 | grep -v TIME_WAIT
func (g *GatewayChecker) CheckGatewayByPort(ctx context.Context, params CheckGatewayParams) (*CheckGatewayResult, error) {
	logger := resource.LoggerService

	// 参数校验
	if params.SandboxUUID == "" {
		return nil, fmt.Errorf("sandboxUUID cannot be empty")
	}

	// 获取沙箱运行时信息
	sandboxRuntime, err := g.getSandboxRuntime(ctx, params.SandboxUUID)
	if err != nil {
		return nil, err
	}

	// 执行端口检测命令
	checkCmd := "netstat -anlp | grep 8081 | grep -v TIME_WAIT"
	logger.Notice(ctx, "checking gateway port",
		logit.String("sandboxUUID", params.SandboxUUID),
		logit.String("cmd", checkCmd))

	result, err := g.executeCommand(ctx, sandboxRuntime, checkCmd)
	if err != nil {
		return nil, err
	}

	// exitCode 为 0 且有输出表示端口正在监听
	isRunning := result.ExitCode == 0 && len(strings.TrimSpace(result.Stdout)) > 0

	logger.Notice(ctx, "gateway port check completed",
		logit.String("sandboxUUID", params.SandboxUUID),
		logit.Bool("portListening", isRunning),
		logit.Int("exitCode", result.ExitCode))

	return &CheckGatewayResult{
		IsRunning:             isRunning,
		CheckMethod:           "port",
		OpenclawPortListening: isRunning,
		Details:               result.Stdout,
	}, nil
}

// CheckInfoflowByPort 通过端口检测 infoflow 进程是否启动
// 使用命令: netstat -anlp | grep 5000 | grep -v TIME_WAIT
func (g *GatewayChecker) CheckInfoflowByPort(ctx context.Context, params CheckGatewayParams) (*CheckGatewayResult, error) {
	logger := resource.LoggerService

	// 参数校验
	if params.SandboxUUID == "" {
		return nil, fmt.Errorf("sandboxUUID cannot be empty")
	}

	// 获取沙箱运行时信息
	sandboxRuntime, err := g.getSandboxRuntime(ctx, params.SandboxUUID)
	if err != nil {
		return nil, err
	}

	// 执行端口检测命令
	checkCmd := "netstat -anlp | grep 5000 | grep -v TIME_WAIT"
	logger.Notice(ctx, "checking infoflow port",
		logit.String("sandboxUUID", params.SandboxUUID),
		logit.String("cmd", checkCmd))

	result, err := g.executeCommand(ctx, sandboxRuntime, checkCmd)
	if err != nil {
		return nil, err
	}

	// exitCode 为 0 且有输出表示端口正在监听
	isRunning := result.ExitCode == 0 && len(strings.TrimSpace(result.Stdout)) > 0

	logger.Notice(ctx, "infoflow port check completed",
		logit.String("sandboxUUID", params.SandboxUUID),
		logit.Bool("portListening", isRunning),
		logit.Int("exitCode", result.ExitCode))

	return &CheckGatewayResult{
		IsRunning:             isRunning,
		CheckMethod:           "port",
		InfoflowPortListening: isRunning,
		Details:               result.Stdout,
	}, nil
}

// CheckGateway 综合检测 openclaw-gateway 是否启动
// 同时检测进程和端口，两者都满足才认为服务正常运行
func (g *GatewayChecker) CheckGateway(ctx context.Context, params CheckGatewayParams) (*CheckGatewayResult, error) {
	logger := resource.LoggerService

	logger.Notice(ctx, "starting comprehensive gateway check",
		logit.String("sandboxUUID", params.SandboxUUID))

	// 再检测端口
	portResult, err := g.CheckGatewayByPort(ctx, params)
	if err != nil {
		logger.Error(ctx, "check gateway by port failed", logit.String("error", err.Error()))
		return nil, fmt.Errorf("check gateway by port failed: %w", err)
	}

	// 检测 infoflow 端口
	infoflowResult, err := g.CheckInfoflowByPort(ctx, params)
	if err != nil {
		logger.Error(ctx, "check infoflow by port failed", logit.String("error", err.Error()))
		return nil, fmt.Errorf("check infoflow by port failed: %w", err)
	}

	// 综合判断：进程存在 且 端口监听
	isRunning := portResult.OpenclawPortListening

	details := fmt.Sprintf("Port: %s\nInfoflow Port: %s",
		portResult.Details,
		infoflowResult.Details)

	logger.Notice(ctx, "comprehensive gateway check completed",
		logit.String("sandboxUUID", params.SandboxUUID),
		logit.Bool("isRunning", isRunning),
		logit.Bool("processExists", isRunning),
		logit.Bool("portListening", portResult.OpenclawPortListening),
		logit.Bool("infoflowListening", infoflowResult.InfoflowPortListening))

	return &CheckGatewayResult{
		IsRunning:             isRunning,
		CheckMethod:           "both",
		OpenclawProcessExists: isRunning,
		OpenclawPortListening: portResult.OpenclawPortListening,
		InfoflowPortListening: infoflowResult.InfoflowPortListening,
		Details:               details,
	}, nil
}

// CheckGatewayRequest 检测 gateway 的请求参数（内部使用）
type CheckGatewayRequest struct {
	Process Process `json:"process"`
}

// CheckGatewayResponse 检测 gateway 的响应结果（内部使用）
type CheckGatewayResponse struct {
	ExitCode int    `json:"exitCode"` // 命令退出码，0 表示成功
	Stdout   string `json:"stdout"`   // 标准输出
}
