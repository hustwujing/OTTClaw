// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/auth/jwt.go — JWT 签发与解析，user_id 从 sub claim 读取，不可伪造
package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"OTTClaw/config"
)

// Claims 自定义 JWT payload，包含标准 RegisteredClaims
type Claims struct {
	jwt.RegisteredClaims
	// sub 字段即 user_id，通过 RegisteredClaims.Subject 承载
}

// ParseToken 解析并校验 JWT，返回 user_id（sub）
// 若签名无效、过期或格式错误，返回 error
func ParseToken(tokenStr string) (userID string, err error) {
	token, err := jwt.ParseWithClaims(
		tokenStr,
		&Claims{},
		func(t *jwt.Token) (interface{}, error) {
			// 确保签名算法是 HMAC 系列，防止 alg=none 攻击
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
			}
			return []byte(config.Cfg.JWTSecret), nil
		},
		jwt.WithExpirationRequired(), // 强制要求 exp 字段存在
	)
	if err != nil {
		return "", fmt.Errorf("parse jwt: %w", err)
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return "", errors.New("invalid token claims")
	}

	userID = claims.Subject
	if userID == "" {
		return "", errors.New("token missing sub (user_id)")
	}
	return userID, nil
}

// GenerateToken 为测试方便提供签发接口，生产环境应由认证服务签发
// expiry: token 有效期
func GenerateToken(userID string, expiry time.Duration) (string, error) {
	claims := &Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(expiry)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(config.Cfg.JWTSecret))
}

// ExtractTokenFromBearer 从 "Bearer <token>" 格式中提取 token 字符串
func ExtractTokenFromBearer(header string) (string, error) {
	const prefix = "Bearer "
	if len(header) <= len(prefix) || header[:len(prefix)] != prefix {
		return "", errors.New("authorization header must be 'Bearer <token>'")
	}
	return header[len(prefix):], nil
}
