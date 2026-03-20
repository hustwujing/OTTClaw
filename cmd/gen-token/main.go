// Author:    维杰（邬晶）
// Email:     wujing03@bilibili.com
// Date:      2026
// Copyright: Copyright (c) 2026 维杰（邬晶）

// cmd/gen-token/main.go — 开发 / 运维工具：签发邀请码或直接生成 JWT
//
// 子命令：
//
//	invite <user-id> [-n <设备数>] [-ttl <有效期>]
//	                          在数据库中创建邀请码并打印
//	                          -n  最多允许几台设备登录（默认不限）
//	                          -ttl 邀请码有效期，如 168h、30d（默认永不过期）
//	token  [user-id] [ttl]   直接签发 JWT（跳过邀请码，仅供本地调试）
//
// 示例：
//
//	gen-token invite alice
//	gen-token invite alice -n 3
//	gen-token invite alice -n 3 -ttl 30d
//	gen-token token alice 24h
package main

import (
	"crypto/rand"
	"flag"
	"fmt"
	"math/big"
	"os"
	"strings"
	"time"

	"OTTClaw/internal/auth"
	"OTTClaw/internal/storage"
)

// 邀请码字符集：去掉易混淆的 0/O/1/I
const charset = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

func genCode() string {
	groups := 4
	groupLen := 4
	parts := make([]string, groups)
	for i := range parts {
		b := make([]byte, groupLen)
		for j := range b {
			n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
			b[j] = charset[n.Int64()]
		}
		parts[i] = string(b)
	}
	return strings.Join(parts, "-")
}

func parseTTL(s string) (time.Duration, error) {
	// 支持 "30d" 简写
	if strings.HasSuffix(s, "d") {
		var days int
		if _, err := fmt.Sscanf(s, "%dd", &days); err == nil {
			return time.Duration(days) * 24 * time.Hour, nil
		}
	}
	return time.ParseDuration(s)
}

func cmdInvite(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "用法: invite <user-id> [-n 设备数] [-ttl 有效期]")
		os.Exit(1)
	}
	userID := args[0]

	fs := flag.NewFlagSet("invite", flag.ExitOnError)
	maxUses := fs.Int("n", 0, "最多允许登录的设备数（默认不限）")
	ttlStr := fs.String("ttl", "", "邀请码有效期，如 168h、30d（默认永不过期）")
	fs.Parse(args[1:]) // user-id 已取出，剩余为 flags

	var expiresAt *time.Time
	if *ttlStr != "" {
		d, err := parseTTL(*ttlStr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "无效有效期 %q: %v\n", *ttlStr, err)
			os.Exit(1)
		}
		t := time.Now().Add(d)
		expiresAt = &t
	}

	if err := storage.InitDB(); err != nil {
		fmt.Fprintf(os.Stderr, "初始化数据库失败: %v\n", err)
		os.Exit(1)
	}

	code := genCode()
	if err := storage.CreateInviteCode(code, userID, *maxUses, expiresAt); err != nil {
		fmt.Fprintf(os.Stderr, "写入数据库失败: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("账号名  : %s\n", userID)
	fmt.Printf("邀请码  : %s\n", code)
	if *maxUses > 0 {
		fmt.Printf("设备限制: %d 台\n", *maxUses)
	} else {
		fmt.Printf("设备限制: 不限\n")
	}
	if expiresAt != nil {
		fmt.Printf("有效期至: %s\n", expiresAt.Format("2006-01-02 15:04:05"))
	} else {
		fmt.Printf("有效期至: 永不过期\n")
	}
}

func cmdToken(args []string) {
	userID := fmt.Sprintf("user-%06x", mustRandInt(0xffffff))
	ttl := 24 * time.Hour

	if len(args) >= 1 {
		userID = args[0]
	}
	if len(args) >= 2 {
		d, err := parseTTL(args[1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "无效有效期 %q: %v\n", args[1], err)
			os.Exit(1)
		}
		ttl = d
	}

	token, err := auth.GenerateToken(userID, ttl)
	if err != nil {
		fmt.Fprintf(os.Stderr, "生成 Token 失败: %v\n", err)
		os.Exit(1)
	}
	exp := time.Now().Add(ttl)
	fmt.Printf("账号名  : %s\n", userID)
	fmt.Printf("有效期至: %s（%s 后）\n", exp.Format("2006-01-02 15:04:05"), ttl)
	fmt.Printf("Token   : %s\n", token)
}

func mustRandInt(max int64) int64 {
	n, _ := rand.Int(rand.Reader, big.NewInt(max))
	return n.Int64()
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("用法:")
		fmt.Println("  gen-token invite <user-id> [-n 设备数] [-ttl 有效期]")
		fmt.Println("  gen-token token  [user-id] [ttl]")
		fmt.Println()
		fmt.Println("示例:")
		fmt.Println("  gen-token invite alice                   # 不限设备，永不过期")
		fmt.Println("  gen-token invite alice -n 3              # 限 3 台设备")
		fmt.Println("  gen-token invite alice -n 3 -ttl 30d     # 限 3 台，30 天有效")
		fmt.Println("  gen-token token  alice 24h               # 直接签发 JWT（调试）")
		os.Exit(1)
	}

	sub, rest := os.Args[1], os.Args[2:]
	switch sub {
	case "invite":
		cmdInvite(rest)
	case "token":
		cmdToken(rest)
	default:
		fmt.Fprintf(os.Stderr, "未知子命令 %q，可选：invite | token\n", sub)
		os.Exit(1)
	}

}
