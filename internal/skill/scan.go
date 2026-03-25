// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/skill/scan.go — 技能文件写入前安全扫描
// 拦截三类风险：
//  1. 不可见/零宽 Unicode 字符（所有文件）
//  2. Prompt injection 短语（SKILL.md 及 .md 文件）
//  3. 危险命令模式：反弹 Shell / 管道执行 / base64+exec（script/ 文件）
package skill

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// ScanSkillFile 在写入技能文件前做安全检查。
// subPath 为空或 "SKILL.md" 时视为 SKILL.md；以 "script/" 开头时视为脚本文件。
// 返回非 nil 表示内容可疑，调用方应拒绝写入并将错误原因返回给调用者。
func ScanSkillFile(content, subPath string) error {
	normPath := strings.ToLower(strings.TrimSpace(subPath))
	isSkillMD := normPath == "" || normPath == "skill.md"
	isMarkdown := isSkillMD || strings.HasSuffix(normPath, ".md")
	isScript := strings.HasPrefix(normPath, "script/")

	// ---- 1. 不可见 Unicode 字符（所有文件）----
	if err := scanInvisibleUnicode(content); err != nil {
		return err
	}

	// ---- 2. Prompt injection（SKILL.md 和其他 .md 文件）----
	if isMarkdown {
		if err := scanPromptInjection(content); err != nil {
			return err
		}
	}

	// ---- 3. 危险命令模式（script/ 文件）----
	if isScript {
		if err := scanDangerousCommands(content); err != nil {
			return err
		}
	}

	return nil
}

// scanInvisibleUnicode 检测不可见/零宽字符，这类字符可用于隐藏指令或编码外泄数据。
func scanInvisibleUnicode(content string) error {
	for i, r := range content {
		switch {
		case r == '\u200B', // ZERO WIDTH SPACE
			r == '\u200C', // ZERO WIDTH NON-JOINER
			r == '\u200D', // ZERO WIDTH JOINER
			r == '\uFEFF', // BOM / ZERO WIDTH NO-BREAK SPACE
			r == '\u00AD', // SOFT HYPHEN
			r == '\u2028', // LINE SEPARATOR
			r == '\u2029', // PARAGRAPH SEPARATOR
			r >= '\u202A' && r <= '\u202E',    // DIRECTIONAL OVERRIDES
			r >= '\uE000' && r <= '\uF8FF',    // PRIVATE USE AREA
			unicode.Is(unicode.Cf, r) && r != '\t' && r != '\n': // 其他格式控制字符
			return fmt.Errorf("content contains invisible unicode character U+%04X at byte offset %d", r, i)
		}
	}
	return nil
}

// scanPromptInjection 检测高置信度 prompt injection 短语。
// 只使用特异性强的短语以减少误报（技能描述可能包含各种普通英文词汇）。
func scanPromptInjection(content string) error {
	lower := strings.ToLower(content)
	phrases := []string{
		"ignore previous instructions",
		"ignore all previous",
		"disregard all instructions",
		"disregard previous instructions",
		"forget your instructions",
		"forget your system prompt",
		"new system prompt",
		"override your instructions",
		"override all previous",
	}
	for _, phrase := range phrases {
		if strings.Contains(lower, phrase) {
			return fmt.Errorf("content contains potential prompt injection: %q", phrase)
		}
	}
	return nil
}

// dangerousPatterns 用于检测脚本中的高危命令组合。
// 每条规则是一个 *regexp.Regexp，匹配则拒绝。
// 模式选择标准：特异性高（误报率极低），明确指示恶意意图。
var dangerousPatterns = []*regexp.Regexp{
	// 反弹 Shell：bash/sh -i 配合 /dev/tcp 或 0>&1 重定向
	regexp.MustCompile(`(?i)bash\s+-i\s+.*>&\s*/dev/tcp/`),
	regexp.MustCompile(`(?i)/dev/tcp/\d`),
	regexp.MustCompile(`(?i)0<&\d+-\s+exec\s+\d+<>/dev/tcp`),

	// nc / ncat 反弹 Shell
	regexp.MustCompile(`(?i)\bnc(?:at)?\b.*-e\s+/bin/(?:ba)?sh`),
	regexp.MustCompile(`(?i)\bnc(?:at)?\b.*--exec\s+/bin`),

	// Base64 解码后直接 pipe 到 shell 执行
	regexp.MustCompile(`(?i)base64\s+(?:-d|--decode)\s*\|+\s*(?:ba)?sh`),
	regexp.MustCompile(`(?i)echo\s+[A-Za-z0-9+/=]{20,}\s*\|\s*base64\s+(?:-d|--decode)\s*\|\s*(?:ba)?sh`),
	regexp.MustCompile(`(?i)openssl\s+enc.*-d.*\|\s*(?:ba)?sh`),

	// curl / wget pipe 到 shell（下载即执行）
	regexp.MustCompile(`(?i)\bcurl\b[^|]*\|\s*(?:sudo\s+)?(?:ba)?sh\b`),
	regexp.MustCompile(`(?i)\bwget\b[^|]*-O\s*-[^|]*\|\s*(?:sudo\s+)?(?:ba)?sh\b`),
	regexp.MustCompile(`(?i)\bwget\b[^|]*\|\s*(?:sudo\s+)?(?:ba)?sh\b`),

	// Python/Perl 执行 base64 payload
	regexp.MustCompile(`(?i)python[23]?\s+-c\s+["'].*base64.*exec\(`),
	regexp.MustCompile(`(?i)perl\s+-e\s+["'].*socket.*exec\(`),
}

// scanDangerousCommands 检测脚本文件中的高危命令模式。
func scanDangerousCommands(content string) error {
	for _, re := range dangerousPatterns {
		if loc := re.FindStringIndex(content); loc != nil {
			snippet := content[loc[0]:loc[1]]
			if len(snippet) > 80 {
				snippet = snippet[:80] + "..."
			}
			return fmt.Errorf("script contains dangerous command pattern: %q", snippet)
		}
	}
	return nil
}
