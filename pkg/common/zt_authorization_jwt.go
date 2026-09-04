package common

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// JWTUserClaims JWT token中的用户信息
type JWTUserClaims struct {
	Username string `json:"username"` // 邮箱前缀
	Name     string `json:"name"`     // 中文名
	Email    string `json:"email"`    // 邮箱
	jwt.RegisteredClaims
}

// ParseJWTToken 解析JWT token获取用户名
// tokenStr: JWT token字符串
// secret: JWT签名密钥
// 返回值: username, error
func ParseJWTToken(tokenStr string, secret string) (string, error) {
	if tokenStr == "" {
		return "", errors.New("JWT token为空")
	}

	if secret == "" {
		return "", errors.New("JWT secret为空")
	}

	token, err := jwt.ParseWithClaims(tokenStr, &JWTUserClaims{}, func(token *jwt.Token) (interface{}, error) {
		return []byte(secret), nil
	}, jwt.WithExpirationRequired(), jwt.WithIssuedAt())

	if err != nil {
		return "", fmt.Errorf("JWT token解析失败: %w", err)
	}

	// 提取用户信息
	if claims, ok := token.Claims.(*JWTUserClaims); ok && claims.Username != "" {
		return claims.Username, nil
	}

	return "", errors.New("JWT token中未找到有效的用户名")
}

// GenerateJWTToken 生成JWT token
// username: 用户名（邮箱前缀）
// name: 中文名
// email: 邮箱
// secret: JWT签名密钥
// expirationHours: token有效期（小时），默认24小时
// 返回值: token字符串, error
func GenerateJWTToken(username, name, email, secret string, expirationHours int) (string, error) {
	if username == "" {
		return "", errors.New("用户名不能为空")
	}

	if secret == "" {
		return "", errors.New("JWT secret为空")
	}

	// 默认24小时过期
	if expirationHours <= 0 {
		expirationHours = 24
	}

	now := time.Now()

	// 构造Claims
	claims := JWTUserClaims{
		Username: username,
		Name:     name,
		Email:    email,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),                                                 // 签发时间
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour * time.Duration(expirationHours))), // 过期时间
		},
	}

	// 创建token
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)

	// 签名并获取完整的token字符串
	tokenString, err := token.SignedString([]byte(secret))
	if err != nil {
		return "", fmt.Errorf("生成JWT token失败: %w", err)
	}

	return tokenString, nil
}
