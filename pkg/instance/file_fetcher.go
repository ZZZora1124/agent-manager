package instance

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/dao"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/resource"
	"icode.baidu.com/baidu/gdp/logit"
)

// FileFetcher 提供从沙箱实例拉取文件内容的功能
type FileFetcher struct {
	db dao.Query
}

// NewFileFetcher 创建一个新的 FileFetcher 实例
func NewFileFetcher(db dao.Query) *FileFetcher {
	return &FileFetcher{
		db: db,
	}
}

// FetchFileParams 拉取文件的参数
type FetchFileParams struct {
	SandboxUUID string // 沙箱唯一标识
	FilePath    string // 文件路径
}

// FetchFile 从指定沙箱拉取文件内容
// 参数:
//   - ctx: 上下文
//   - params: 拉取参数，包含 SandboxUUID、FilePath
//
// 返回:
//   - string: 文件文本内容
//   - error: 拉取过程中的错误，成功则返回 nil
func (f *FileFetcher) FetchFile(ctx context.Context, params FetchFileParams) (string, error) {
	logger := resource.LoggerService

	// 参数校验
	if params.SandboxUUID == "" {
		return "", fmt.Errorf("sandboxUUID cannot be empty")
	}
	if params.FilePath == "" {
		return "", fmt.Errorf("filePath cannot be empty")
	}

	// 从数据库查询沙箱运行时信息
	sandboxRuntime, err := f.db.SandboxRuntime.WithContext(ctx).
		Where(f.db.SandboxRuntime.SandboxUUID.Eq(params.SandboxUUID)).
		Where(f.db.SandboxRuntime.Deleted.Is(false)).
		First()
	if err != nil {
		logger.Error(ctx, "query sandbox runtime failed",
			logit.String("sandboxUUID", params.SandboxUUID),
			logit.String("error", err.Error()))
		return "", fmt.Errorf("query sandbox runtime failed: %w", err)
	}

	// 检查沙箱状态
	if sandboxRuntime.Status != "running" {
		logger.Warning(ctx, "sandbox status is not running",
			logit.String("sandboxUUID", params.SandboxUUID),
			logit.String("status", sandboxRuntime.Status))
		return "", fmt.Errorf("sandbox status is invalid, current status: %s", sandboxRuntime.Status)
	}

	// 构造请求 URL
	requestURL := fmt.Sprintf(
		"https://%d-%s.agent-sandbox.baidu-int.com/files?path=%s",
		sandboxRuntime.CommandPort,
		params.SandboxUUID,
		url.QueryEscape(params.FilePath),
	)

	logger.Notice(ctx, "preparing to fetch file",
		logit.String("sandboxUUID", params.SandboxUUID),
		logit.String("filePath", params.FilePath),
		logit.String("url", requestURL))

	// 创建 HTTP 请求
	req, err := http.NewRequestWithContext(ctx, "GET", requestURL, nil)
	if err != nil {
		logger.Error(ctx, "create http request failed", logit.String("error", err.Error()))
		return "", fmt.Errorf("create http request failed: %w", err)
	}

	// 设置请求头
	req.Header.Set("X-Access-Token", sandboxRuntime.Token)

	// 发送请求
	client := &http.Client{
		Timeout: 30 * time.Second, // 设置 30 秒超时
	}

	logger.Notice(ctx, "sending fetch file request", logit.String("url", requestURL))
	resp, err := client.Do(req)
	if err != nil {
		logger.Error(ctx, "send http request failed", logit.String("error", err.Error()))
		return "", fmt.Errorf("send http request failed: %w", err)
	}
	defer resp.Body.Close()

	// 检查响应状态码
	if resp.StatusCode != http.StatusOK {
		logger.Error(ctx, "fetch file failed",
			logit.Int("statusCode", resp.StatusCode),
			logit.String("sandboxUUID", params.SandboxUUID),
			logit.String("filePath", params.FilePath))
		return "", fmt.Errorf("fetch file failed, http status code: %d", resp.StatusCode)
	}

	// 读取响应体
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Error(ctx, "read response body failed", logit.String("error", err.Error()))
		return "", fmt.Errorf("read response body failed: %w", err)
	}

	content := string(bodyBytes)

	logger.Notice(ctx, "file fetched successfully",
		logit.String("sandboxUUID", params.SandboxUUID),
		logit.String("filePath", params.FilePath),
		logit.Int("contentLength", len(content)))

	return content, nil
}
