package instance

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/dao"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/resource"
	"icode.baidu.com/baidu/gdp/logit"
	"net/http"
	"time"
)

// SkillInstaller 提供 skill 自动安装功能
type SkillInstaller struct {
	db dao.Query
}

// NewSkillInstaller 创建一个新的 SkillInstaller 实例
func NewSkillInstaller(db dao.Query) *SkillInstaller {
	return &SkillInstaller{
		db: db,
	}
}

// Process 进程结构体
type Process struct {
	Cmd  string   `json:"cmd"`
	Args []string `json:"args"`
}

// InstallSkillRequest 安装 skill 的请求参数
type InstallSkillRequest struct {
	Process Process `json:"process"`
}

// InstallSkillResponse 安装 skill 的响应结果
type InstallSkillResponse struct {
	ExitCode int    `json:"exitCode"` // 命令退出码，0 表示成功
	Stdout   string `json:"stdout"`   // 标准输出
}

// InstallSkillParams 安装 skill 的参数
type InstallSkillParams struct {
	SandboxUUID string // 沙箱唯一标识
	DownloadURL string // skill 下载链接
	SkillName   string // skill 标识名称（如：iapi-manager）
}

// InstallSkill 自动安装 skill 到指定的沙箱
// 参数:
//   - ctx: 上下文
//   - params: 安装参数，包含 SandboxUUID、DownloadURL、SkillName
//
// 返回:
//   - error: 安装过程中的错误，成功则返回 nil
func (s *SkillInstaller) InstallSkill(ctx context.Context, params InstallSkillParams) error {
	logger := resource.LoggerService

	// 参数校验
	if params.SandboxUUID == "" {
		return fmt.Errorf("sandboxUUID cannot be empty")
	}
	if params.DownloadURL == "" {
		return fmt.Errorf("downloadURL cannot be empty")
	}
	if params.SkillName == "" {
		return fmt.Errorf("skillName cannot be empty")
	}

	// 从数据库查询沙箱运行时信息
	sandboxRuntime, err := s.db.SandboxRuntime.WithContext(ctx).
		Where(s.db.SandboxRuntime.SandboxUUID.Eq(params.SandboxUUID)).
		Where(s.db.SandboxRuntime.Deleted.Is(false)).
		First()
	if err != nil {
		logger.Error(ctx, "query sandbox runtime failed",
			logit.String("sandboxUUID", params.SandboxUUID),
			logit.String("error", err.Error()))
		return fmt.Errorf("query sandbox runtime failed: %w", err)
	}

	// 检查沙箱状态
	if sandboxRuntime.Status != "running" {
		logger.Warning(ctx, "sandbox status is not running",
			logit.String("sandboxUUID", params.SandboxUUID),
			logit.String("status", sandboxRuntime.Status))
		return fmt.Errorf("sandbox status is invalid, current status: %s", sandboxRuntime.Status)
	}

	// 构造安装命令
	installCmd := fmt.Sprintf(
		"cd ~/.openclaw/skills && wget -O %s.zip %s && unzip -o %s.zip && rm %s.zip",
		params.SkillName,
		params.DownloadURL,
		params.SkillName,
		params.SkillName,
	)

	logger.Notice(ctx, "preparing to install skill",
		logit.String("sandboxUUID", params.SandboxUUID),
		logit.String("skillName", params.SkillName),
		logit.String("cmd", installCmd))

	// 构造请求体
	requestBody := InstallSkillRequest{
		Process: Process{
			Cmd:  "bash",
			Args: []string{"-l", "-c", installCmd},
		},
	}

	requestBodyBytes, err := json.Marshal(requestBody)
	if err != nil {
		logger.Error(ctx, "marshal request body failed", logit.String("error", err.Error()))
		return fmt.Errorf("marshal request body failed: %w", err)
	}

	// 构造请求 URL
	url := fmt.Sprintf(
		"http://%d-%s.agent-sandbox.baidu-int.com/process.Process/StartSync",
		sandboxRuntime.CommandPort,
		params.SandboxUUID,
	)

	// 创建 HTTP 请求
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(requestBodyBytes))
	if err != nil {
		logger.Error(ctx, "create http request failed", logit.String("error", err.Error()))
		return fmt.Errorf("create http request failed: %w", err)
	}

	// 设置请求头
	req.Header.Set("X-Access-Token", sandboxRuntime.Token)
	req.Header.Set("Content-Type", "application/json")

	// 发送请求
	client := &http.Client{
		Timeout: 60 * time.Second, // 设置 60 秒超时
	}

	logger.Notice(ctx, "sending install request", logit.String("url", url))
	resp, err := client.Do(req)
	if err != nil {
		logger.Error(ctx, "send http request failed", logit.String("error", err.Error()))
		return fmt.Errorf("send http request failed: %w", err)
	}
	defer resp.Body.Close()

	// 检查响应状态码
	if resp.StatusCode != http.StatusOK {
		logger.Error(ctx, "install skill failed", logit.Int("statusCode", resp.StatusCode))
		return fmt.Errorf("install skill failed, http status code: %d", resp.StatusCode)
	}

	// 解析响应体
	var result InstallSkillResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		logger.Error(ctx, "decode response body failed", logit.String("error", err.Error()))
		return fmt.Errorf("decode response body failed: %w", err)
	}

	// 检查 exitCode，只有为 0 才表示成功
	if result.ExitCode != 0 {
		logger.Error(ctx, "skill installation failed",
			logit.String("sandboxUUID", params.SandboxUUID),
			logit.String("skillName", params.SkillName),
			logit.Int("exitCode", result.ExitCode),
			logit.String("stdout", result.Stdout))
		return fmt.Errorf("skill installation failed, exitCode: %d, details: %s", result.ExitCode, result.Stdout)
	}

	logger.Notice(ctx, "skill installed successfully",
		logit.String("sandboxUUID", params.SandboxUUID),
		logit.String("skillName", params.SkillName),
		logit.Int("exitCode", result.ExitCode))

	return nil
}
