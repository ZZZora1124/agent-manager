package instance

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/common"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/dao"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/authcenter"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/devopsauth"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/k8s"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/resource"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/model"
	"icode.baidu.com/baidu/gdp/logit"
)

// OpenClawConfigGenerator 提供生成 openclaw.json 配置的功能
type OpenClawConfigGenerator struct {
	db dao.Query
}

// NewOpenClawConfigGenerator 创建一个新的 OpenClawConfigGenerator 实例
func NewOpenClawConfigGenerator(db dao.Query) *OpenClawConfigGenerator {
	return &OpenClawConfigGenerator{
		db: db,
	}
}

// GenerateConfigParams 生成配置的参数
type GenerateConfigParams struct {
	OneApiKey          string // OneAPI 密钥
	OpenClawToken      string // OpenClaw 访问令牌
	SandboxUUID        string // 沙箱唯一标识（也用作 Pod 名称）
	Username           string // 用户名
	IsSmallTrafficUser bool   // 是否为小流量用户
	IsSuperUser        bool   // 是否为超级用户
}

// GenerateOpenClawConfig 生成用户的 openclaw.json 配置内容
// 参数:
//   - ctx: 上下文
//   - params: 生成参数，包含所有模板替换所需的变量
//
// 返回:
//   - string: 生成的 JSON 配置内容
//   - error: 生成过程中的错误，成功则返回 nil
//
// 使用示例:
//
//	generator := NewOpenClawConfigGenerator()
//	config, err := generator.GenerateOpenClawConfig(ctx, GenerateConfigParams{
//	    OneApiKey:      "your-api-key",
//	    OpenClawToken:  "your-token",
//	    SandboxUUID:    "pod-123456",
//	    Username:       "yugeyang",
//	})
func (g *OpenClawConfigGenerator) GenerateOpenClawConfig(ctx context.Context, params GenerateConfigParams) (string, error) {
	logger := resource.LoggerService

	// 参数校验
	if err := g.validateParams(params); err != nil {
		logger.Error(ctx, "invalid parameters for generating openclaw config",
			logit.String("error", err.Error()))
		return "", err
	}

	logger.Notice(ctx, "generating openclaw config",
		logit.String("sandboxUUID", params.SandboxUUID),
		logit.String("username", params.Username))

	// 构建模板数据
	templateData := &k8s.ClawdBotTemplateData{
		OneApiKey:          params.OneApiKey,
		OpenClawToken:      params.OpenClawToken,
		SandboxUUID:        params.SandboxUUID,
		Username:           params.Username,
		IsSmallTrafficUser: params.IsSmallTrafficUser,
		IsSuperUser:        params.IsSuperUser,
	}

	// 渲染模板生成 JSON 配置
	jsonConfig, err := k8s.RenderClawdBotJson(templateData)
	if err != nil {
		logger.Error(ctx, "failed to render openclaw config template",
			logit.String("sandboxUUID", params.SandboxUUID),
			logit.String("error", err.Error()))
		return "", fmt.Errorf("failed to render openclaw config template: %w", err)
	}

	logger.Notice(ctx, "openclaw config generated successfully",
		logit.String("sandboxUUID", params.SandboxUUID),
		logit.Int("configLength", len(jsonConfig)))

	return jsonConfig, nil
}

// validateParams 校验生成配置所需的参数
func (g *OpenClawConfigGenerator) validateParams(params GenerateConfigParams) error {
	if params.OneApiKey == "" {
		return fmt.Errorf("oneApiKey cannot be empty")
	}
	if params.OpenClawToken == "" {
		return fmt.Errorf("openClawToken cannot be empty")
	}
	if params.SandboxUUID == "" {
		return fmt.Errorf("sandboxUUID cannot be empty")
	}
	if params.Username == "" {
		return fmt.Errorf("username cannot be empty")
	}
	return nil
}

// WriteOpenClawConfigToInstance 生成用户的 openclaw.json 配置并写入实例
// 这是一个上层方法，自动从数据库查询并组装所有必要的参数，生成配置后写入实例的 /home/gem/test/config 目录
//
// 参数:
//   - ctx: 上下文
//   - sandboxUUID: 沙箱唯一标识
//
// 返回:
//   - error: 执行过程中的错误，成功则返回 nil
func (g *OpenClawConfigGenerator) WriteOpenClawConfigToInstance(ctx context.Context, sandboxUUID string) error {
	logger := resource.LoggerService

	// 参数校验
	if sandboxUUID == "" {
		return fmt.Errorf("sandboxUUID cannot be empty")
	}

	logger.Notice(ctx, "fetching openclaw config for sandbox",
		logit.String("sandboxUUID", sandboxUUID))

	// 1. 从数据库查询沙箱运行时信息
	sandboxRuntime, err := g.db.SandboxRuntime.WithContext(ctx).
		Where(g.db.SandboxRuntime.SandboxUUID.Eq(sandboxUUID)).
		Where(g.db.SandboxRuntime.Deleted.Is(false)).
		First()
	if err != nil {
		logger.Error(ctx, "query sandbox runtime failed",
			logit.String("sandboxUUID", sandboxUUID),
			logit.String("error", err.Error()))
		return fmt.Errorf("query sandbox runtime failed: %w", err)
	}

	// 获取用户名（创建人）
	username := sandboxRuntime.CreateUser
	if username == "" {
		return fmt.Errorf("sandbox create_user is empty")
	}

	// 检查沙箱状态
	if sandboxRuntime.Status != "running" {
		logger.Warning(ctx, "sandbox status is not running",
			logit.String("sandboxUUID", sandboxUUID),
			logit.String("status", sandboxRuntime.Status))
		return fmt.Errorf("sandbox status is invalid, current status: %s", sandboxRuntime.Status)
	}

	// 获取 AppID 和 ImageID
	appID := sandboxRuntime.AppID
	imageID := sandboxRuntime.ImageID
	logger.Notice(ctx, "sandbox info retrieved",
		logit.String("sandboxUUID", sandboxUUID),
		logit.String("username", username),
		logit.Int32("appID", appID),
		logit.Int32("imageID", imageID))

	// 2. 获取 OneApiKey
	oneApiKey, err := g.getOneApiKey(ctx, appID, username, sandboxRuntime.EnvirVars)
	if err != nil {
		logger.Warning(ctx, "failed to get oneApiKey, will use empty string",
			logit.String("sandboxUUID", sandboxUUID),
			logit.String("error", err.Error()))
		return fmt.Errorf("获取模型凭证失败")
	}

	// 3. OpenClawToken 固定为 "123456"
	openClawToken := "123456"

	image, err := g.db.Image.WithContext(ctx).
		Where(g.db.Image.ID.Eq(imageID)).
		First()
	if err != nil {
		logger.Warning(ctx, "query image failed, using default",
			logit.String("sandboxUUID", sandboxUUID),
			logit.Int32("imageID", imageID),
			logit.String("error", err.Error()))
	} else {
		logger.Notice(ctx, "image info retrieved",
			logit.String("sandboxUUID", sandboxUUID),
			logit.String("imageURL", image.ImageURL))
	}

	// 5. 生成配置
	isSmallTrafficUser, isSuperUser := common.IsSmallTrafficUser(username) // 从Redis白名单判断是否为小流量用户

	config, err := g.GenerateOpenClawConfig(ctx, GenerateConfigParams{
		OneApiKey:          oneApiKey,
		OpenClawToken:      openClawToken,
		SandboxUUID:        sandboxUUID,
		Username:           username,
		IsSmallTrafficUser: isSmallTrafficUser,
		IsSuperUser:        isSuperUser,
	})
	if err != nil {
		return fmt.Errorf("failed to generate openclaw config: %w", err)
	}

	logger.Notice(ctx, "openclaw config generated, preparing to write to instance",
		logit.String("sandboxUUID", sandboxUUID),
		logit.Int("configLength", len(config)))

	// 6. 将配置写入实例的 /home/gem/test/config 目录
	if err := g.writeConfigToInstance(ctx, sandboxRuntime, config); err != nil {
		return fmt.Errorf("failed to write config to instance: %w", err)
	}

	logger.Notice(ctx, "openclaw config written to instance successfully",
		logit.String("sandboxUUID", sandboxUUID))

	return nil
}

// getOneApiKey 获取 OneAPI 密钥
// 参考逻辑来自 handler.go 中的处理流程
func (g *OpenClawConfigGenerator) getOneApiKey(ctx context.Context, appID int32, username string, envirVarsJSON string) (string, error) {
	logger := resource.LoggerService

	// 1. 尝试从环境变量中获取 COMATE_AUTH_TOKEN
	var envVars map[string]string
	comateAuthToken := ""

	if envirVarsJSON != "" {
		if err := json.Unmarshal([]byte(envirVarsJSON), &envVars); err != nil {
			logger.Warning(ctx, "failed to parse envir_vars",
				logit.String("error", err.Error()))
		} else {
			comateAuthToken = envVars["COMATE_AUTH_TOKEN"]
		}
	}

	// 2. 如果 COMATE_AUTH_TOKEN 为空，尝试从 authCenter 获取个人 token
	if comateAuthToken == "" {
		logger.Notice(ctx, "COMATE_AUTH_TOKEN env is empty",
			logit.Int32("appID", appID),
			logit.String("username", username))

		// 检查 appID 是否在白名单中
		// 使用 defer-recover 防止 Redis 未初始化导致的 panic
		//inWhiteList := false
		func() {
			defer func() {
				if r := recover(); r != nil {
					logger.Warning(ctx, "AppInTokenWhiteList panic recovered",
						logit.String("panic", fmt.Sprintf("%v", r)))
				}
			}()
			//inWhiteList = common.AppInTokenWhiteList(appID)
		}()

		if username != "" {
			logger.Notice(ctx, "try get personal token from authCenter",
				logit.Int32("appID", appID),
				logit.String("username", username))

			comateAuthToken = authcenter.GetAuthCenterApi().GetPersonalTokenByUsername(username)
		}
	}

	// 3. 如果 comateAuthToken 不为空，调用 devopsauth 获取 modelToken
	if comateAuthToken != "" {
		if common.IsPersonalTokenUser(username) {
			// 使用新的 token 接口
			if personalToken, pErr := devopsauth.GetAuthApi().GetOpenclawGWToken(username); pErr == nil {
				return personalToken, nil
			}
		} else {
			modelToken, err := devopsauth.GetAuthApi().GetModelInfo(comateAuthToken)
			if err != nil || modelToken == nil {
				logger.Notice(ctx, "get model token failed",
					logit.String("error", err.Error()))
				return "", fmt.Errorf("get model token failed: %w", err)
			}

			logger.Notice(ctx, "successfully retrieved model token",
				logit.String("username", username))

			return modelToken.ModelToken, nil
		}
	}

	// 4. 无法获取 token
	logger.Warning(ctx, "unable to retrieve oneApiKey",
		logit.Int32("appID", appID),
		logit.String("username", username))

	return "", fmt.Errorf("unable to retrieve oneApiKey: comateAuthToken is empty")
}

// writeConfigToInstance 将配置内容写入实例的 /home/gem/test/config 目录
// 参考 skill_installer.go 的实现方式，通过 HTTP API 调用实例的进程接口
func (g *OpenClawConfigGenerator) writeConfigToInstance(ctx context.Context, sandboxRuntime *model.SandboxRuntime, configContent string) error {
	logger := resource.LoggerService

	// Base64 编码配置内容，避免特殊字符问题
	encodedContent := base64.StdEncoding.EncodeToString([]byte(configContent))

	// 构造写入文件并重启 openclaw-gateway 的命令
	// 步骤：
	// 1. 删除旧配置文件 rm -f /home/gem/.openclaw/openclaw.json*
	// 2. 创建目录 mkdir -p /home/gem/.openclaw
	// 3. 将 base64 编码的内容写入文件 echo <encoded> | base64 -d > /home/gem/.openclaw/openclaw.json
	// 4. kill openclaw-gateway 进程 pkill -f openclaw-gateway
	// 5. 重新启动 openclaw-gateway 进程 openclaw gateway --allow-unconfigured > /tmp/openclaw.log 2>&1 &
	writeCmd := fmt.Sprintf(
		"rm -f /home/gem/.openclaw/openclaw.json* && "+
			"mkdir -p /home/gem/.openclaw && "+
			"echo '%s' | base64 -d > /home/gem/.openclaw/openclaw.json && "+
			"pkill -f openclaw-gateway || true && "+
			"sleep 1 && "+
			"openclaw gateway --allow-unconfigured > /tmp/openclaw.log 2>&1 &",
		encodedContent,
	)

	logger.Notice(ctx, "preparing to write config file and restart openclaw-gateway",
		logit.String("sandboxUUID", sandboxRuntime.SandboxUUID),
		logit.String("targetPath", "/home/gem/.openclaw/openclaw.json"))

	// 构造请求体
	requestBody := map[string]interface{}{
		"process": map[string]interface{}{
			"cmd":  "bash",
			"args": []string{"-l", "-c", writeCmd},
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
		sandboxRuntime.SandboxUUID,
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

	logger.Notice(ctx, "sending write file request", logit.String("url", url))

	resp, err := client.Do(req)
	if err != nil {
		logger.Error(ctx, "send http request failed", logit.String("error", err.Error()))
		return fmt.Errorf("send http request failed: %w", err)
	}
	defer resp.Body.Close()

	// 检查响应状态码
	if resp.StatusCode != http.StatusOK {
		logger.Error(ctx, "write config file failed", logit.Int("statusCode", resp.StatusCode))
		return fmt.Errorf("write config file failed, http status code: %d", resp.StatusCode)
	}

	// 解析响应体
	var result struct {
		ExitCode int    `json:"exitCode"`
		Stdout   string `json:"stdout"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		logger.Error(ctx, "decode response body failed", logit.String("error", err.Error()))
		return fmt.Errorf("decode response body failed: %w", err)
	}

	// 检查 exitCode，只有为 0 才表示成功
	if result.ExitCode != 0 {
		logger.Error(ctx, "write config file failed",
			logit.String("sandboxUUID", sandboxRuntime.SandboxUUID),
			logit.Int("exitCode", result.ExitCode),
			logit.String("stdout", result.Stdout))
		return fmt.Errorf("write config file failed, exitCode: %d, details: %s", result.ExitCode, result.Stdout)
	}

	logger.Notice(ctx, "config file written and openclaw-gateway restarted successfully",
		logit.String("sandboxUUID", sandboxRuntime.SandboxUUID),
		logit.String("configPath", "/home/gem/.openclaw/openclaw.json"))

	return nil
}
