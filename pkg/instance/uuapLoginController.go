package instance

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/dao"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/resource"
	"icode.baidu.com/baidu/gdp/logit"
)

// UUAPLoginController 提供 UUAP 登录登出控制功能
type UUAPLoginController struct {
	db dao.Query
}

// NewUUAPLoginController 创建一个新的 UUAPLoginController 实例
func NewUUAPLoginController(db dao.Query) *UUAPLoginController {
	return &UUAPLoginController{
		db: db,
	}
}

// LogoutParams 登出参数
type LogoutParams struct {
	SandboxUUID string // 沙箱唯一标识
}

// LoginParams 登入参数
type LoginParams struct {
	SandboxUUID string // 沙箱唯一标识
	Username    string // 用户名
	Token       string // Ugate Token
}

// ExecuteCommandResponse 执行命令的响应结果
type ExecuteCommandResponse struct {
	ExitCode int    `json:"exitCode"` // 命令退出码，0 表示成功
	Stdout   string `json:"stdout"`   // 标准输出
	Stderr   string `json:"stderr"`   // 标准错误输出
}

// Login 在沙箱内部执行登入操作
// 功能：
//
//	写入 ugate token 文件到 ~/.config/uuap/.eac_ugate_token_{username}
//
// 参数:
//   - ctx: 上下文
//   - params: 登入参数，包含 SandboxUUID、Username、Token
//
// 返回:
//   - error: 登入过程中的错误，成功则返回 nil
func (c *UUAPLoginController) Login(ctx context.Context, params LoginParams) error {
	logger := resource.LoggerService

	// 参数校验
	if params.SandboxUUID == "" {
		return fmt.Errorf("sandboxUUID cannot be empty")
	}
	if params.Username == "" {
		return fmt.Errorf("username cannot be empty")
	}
	if params.Token == "" {
		return fmt.Errorf("token cannot be empty")
	}

	// 从数据库查询沙箱运行时信息
	sandboxRuntime, err := c.db.SandboxRuntime.WithContext(ctx).
		Where(c.db.SandboxRuntime.SandboxUUID.Eq(params.SandboxUUID)).
		Where(c.db.SandboxRuntime.Deleted.Is(false)).
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

	// 构造写入 token 文件的命令
	// 1. 创建 ~/.config/uuap/ 目录
	// 2. 写入 ugate token 文件
	writeTokenFilePath := fmt.Sprintf("~/.config/uuap/.eac_ugate_token_%s", params.Username)
	writeCmd := fmt.Sprintf(`
mkdir -p ~/.config/uuap
cat > %s << 'EOF'
%s
EOF
echo "Token file written: %s"
`, writeTokenFilePath, params.Token, writeTokenFilePath)

	logger.Notice(ctx, "preparing to write ugate token file to sandbox",
		logit.String("sandboxUUID", params.SandboxUUID),
		logit.String("username", params.Username),
		logit.String("tokenFilePath", writeTokenFilePath))

	// 构造请求体
	requestBody := InstallSkillRequest{
		Process: Process{
			Cmd:  "bash",
			Args: []string{"-l", "-c", writeCmd},
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
		Timeout: 30 * time.Second,
	}

	logger.Notice(ctx, "sending write token file request to sandbox", logit.String("url", url))
	resp, err := client.Do(req)
	if err != nil {
		logger.Error(ctx, "send http request failed", logit.String("error", err.Error()))
		return fmt.Errorf("send http request failed: %w", err)
	}
	defer resp.Body.Close()

	// 检查响应状态码
	if resp.StatusCode != http.StatusOK {
		logger.Error(ctx, "write token file failed", logit.Int("statusCode", resp.StatusCode))
		return fmt.Errorf("write token file failed, http status code: %d", resp.StatusCode)
	}

	// 解析响应体
	var result ExecuteCommandResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		logger.Error(ctx, "decode response body failed", logit.String("error", err.Error()))
		return fmt.Errorf("decode response body failed: %w", err)
	}

	// 记录执行结果
	logger.Notice(ctx, "write token file execution result",
		logit.String("sandboxUUID", params.SandboxUUID),
		logit.Int("exitCode", result.ExitCode),
		logit.String("stdout", result.Stdout),
		logit.String("stderr", result.Stderr))

	// 检查 exitCode，只有为 0 才表示成功
	if result.ExitCode != 0 {
		logger.Error(ctx, "write token file execution failed",
			logit.String("sandboxUUID", params.SandboxUUID),
			logit.Int("exitCode", result.ExitCode),
			logit.String("stdout", result.Stdout),
			logit.String("stderr", result.Stderr))
		return fmt.Errorf("write token file execution failed, exitCode: %d, stderr: %s", result.ExitCode, result.Stderr)
	}

	logger.Notice(ctx, "UUAP login completed successfully",
		logit.String("sandboxUUID", params.SandboxUUID),
		logit.String("username", params.Username))

	return nil
}

// 功能：
//  1. 清理 ~/.config/uuap/ 目录下的所有文件
//  2. 如果 ~/.tools/logout.py 存在，则执行该脚本进行额外清理
//
// 参数:
//   - ctx: 上下文
//   - params: 登出参数，包含 SandboxUUID
//
// 返回:
//   - error: 登出过程中的错误，成功则返回 nil
func (c *UUAPLoginController) Logout(ctx context.Context, params LogoutParams) error {
	logger := resource.LoggerService

	// 参数校验
	if params.SandboxUUID == "" {
		return fmt.Errorf("sandboxUUID cannot be empty")
	}

	// 从数据库查询沙箱运行时信息
	sandboxRuntime, err := c.db.SandboxRuntime.WithContext(ctx).
		Where(c.db.SandboxRuntime.SandboxUUID.Eq(params.SandboxUUID)).
		Where(c.db.SandboxRuntime.Deleted.Is(false)).
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

	// 构造清理命令
	// 1. 清理 ~/.config/uuap/ 目录
	// 2. 执行 aigate-cli cookie -c REMOVE 命令清理 cookie
	logoutCmd := `
# 清理 ~/.config/uuap/ 目录
if [ -d ~/.config/uuap/ ]; then
    echo "Cleaning ~/.config/uuap/ directory..."
    rm -rf ~/.config/uuap/.eac_ugate_token_*
    echo "~/.config/uuap/ cleaned successfully"
else
    echo "~/.config/uuap/ directory does not exist, skipping"
fi

# 执行 aigate-cli cookie -c REMOVE 命令清理 cookie
echo "Executing aigate-cli cookie -c REMOVE..."
aigate-cli cookie -c REMOVE
COOKIE_REMOVE_EXIT_CODE=$?
if [ $COOKIE_REMOVE_EXIT_CODE -eq 0 ]; then
    echo "aigate-cli cookie -c REMOVE executed successfully"
else
    echo "aigate-cli cookie -c REMOVE execution failed with exit code: $COOKIE_REMOVE_EXIT_CODE"
fi

echo "UUAP logout completed"
`

	logger.Notice(ctx, "preparing to execute UUAP logout in sandbox",
		logit.String("sandboxUUID", params.SandboxUUID))

	// 构造请求体
	requestBody := InstallSkillRequest{
		Process: Process{
			Cmd:  "bash",
			Args: []string{"-l", "-c", logoutCmd},
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
		Timeout: 30 * time.Second, // 设置 30 秒超时
	}

	logger.Notice(ctx, "sending logout request to sandbox", logit.String("url", url))
	resp, err := client.Do(req)
	if err != nil {
		logger.Error(ctx, "send http request failed", logit.String("error", err.Error()))
		return fmt.Errorf("send http request failed: %w", err)
	}
	defer resp.Body.Close()

	// 检查响应状态码
	if resp.StatusCode != http.StatusOK {
		logger.Error(ctx, "execute logout command failed", logit.Int("statusCode", resp.StatusCode))
		return fmt.Errorf("execute logout command failed, http status code: %d", resp.StatusCode)
	}

	// 解析响应体
	var result ExecuteCommandResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		logger.Error(ctx, "decode response body failed", logit.String("error", err.Error()))
		return fmt.Errorf("decode response body failed: %w", err)
	}

	// 记录执行结果（即使 exitCode 不为 0，也记录输出）
	logger.Notice(ctx, "logout command execution result",
		logit.String("sandboxUUID", params.SandboxUUID),
		logit.Int("exitCode", result.ExitCode),
		logit.String("stdout", result.Stdout),
		logit.String("stderr", result.Stderr))

	// 检查 exitCode，只有为 0 才表示成功
	if result.ExitCode != 0 {
		logger.Error(ctx, "UUAP logout execution failed",
			logit.String("sandboxUUID", params.SandboxUUID),
			logit.Int("exitCode", result.ExitCode),
			logit.String("stdout", result.Stdout),
			logit.String("stderr", result.Stderr))
		return fmt.Errorf("UUAP logout execution failed, exitCode: %d, stderr: %s", result.ExitCode, result.Stderr)
	}

	logger.Notice(ctx, "UUAP logout completed successfully",
		logit.String("sandboxUUID", params.SandboxUUID))

	return nil
}
