package instance

import (
	"context"
	"fmt"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/dao"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/authcenter"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/resource"
	"icode.baidu.com/baidu/gdp/logit"
)

// TokenValidator 提供 token 校验和用户权限验证功能
type TokenValidator struct {
	db      dao.Query
	authAPI *authcenter.Api
}

// NewTokenValidator 创建一个新的 TokenValidator 实例
func NewTokenValidator(db dao.Query) *TokenValidator {
	return &TokenValidator{
		db:      db,
		authAPI: authcenter.GetAuthCenterApi(),
	}
}

// ValidateSandboxAccess 校验用户是否有权限访问指定的沙箱
// 参数:
//   - ctx: 上下文
//   - sandboxUUID: 沙箱唯一标识
//   - accessToken: 用户访问令牌
//
// 返回:
//   - string: 验证通过时返回用户名
//   - error: 验证失败时返回错误信息
func (v *TokenValidator) ValidateSandboxAccess(ctx context.Context, sandboxUUID string, accessToken string) (string, error) {
	logger := resource.LoggerService

	// 参数校验
	if sandboxUUID == "" {
		return "", fmt.Errorf("sandboxUUID cannot be empty")
	}
	if accessToken == "" {
		return "", fmt.Errorf("accessToken cannot be empty")
	}

	// 1. 调用 authcenter API 校验 token，获取用户信息
	logger.Notice(ctx, "validating access token",
		logit.String("sandboxUUID", sandboxUUID))

	tokenData, err := v.authAPI.ValidateToken(accessToken)
	if err != nil {
		logger.Error(ctx, "validate access token failed",
			logit.String("sandboxUUID", sandboxUUID),
			logit.String("error", err.Error()))
		return "", fmt.Errorf("failed to validate access token: %w", err)
	}

	// 获取 token 对应的用户名
	tokenUsername := tokenData.Username
	if tokenUsername == "" {
		logger.Error(ctx, "username from token is empty",
			logit.String("sandboxUUID", sandboxUUID))
		return "", fmt.Errorf("username from token is empty")
	}

	logger.Notice(ctx, "token validated successfully",
		logit.String("sandboxUUID", sandboxUUID),
		logit.String("username", tokenUsername))

	// 2. 从数据库查询沙箱的创建用户
	sandboxRuntime, err := v.db.SandboxRuntime.WithContext(ctx).
		Where(v.db.SandboxRuntime.SandboxUUID.Eq(sandboxUUID)).
		Where(v.db.SandboxRuntime.Deleted.Is(false)).
		First()
	if err != nil {
		logger.Error(ctx, "query sandbox runtime failed",
			logit.String("sandboxUUID", sandboxUUID),
			logit.String("error", err.Error()))
		return "", fmt.Errorf("sandbox not found or has been deleted")
	}

	// 获取沙箱的创建用户
	createUser := sandboxRuntime.CreateUser
	logger.Notice(ctx, "sandbox creator found",
		logit.String("sandboxUUID", sandboxUUID),
		logit.String("createUser", createUser))

	// 3. 比较两个用户名是否一致
	if tokenUsername != createUser {
		logger.Warning(ctx, "username mismatch",
			logit.String("sandboxUUID", sandboxUUID),
			logit.String("tokenUsername", tokenUsername),
			logit.String("createUser", createUser))
		return "", fmt.Errorf("username mismatch: token user '%s' is not the sandbox creator '%s'", tokenUsername, createUser)
	}

	logger.Notice(ctx, "sandbox access validation successful",
		logit.String("sandboxUUID", sandboxUUID),
		logit.String("username", tokenUsername))

	// 4. 验证通过，返回用户名
	return tokenUsername, nil
}
