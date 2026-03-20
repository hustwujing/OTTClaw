// Author:    维杰（邬晶）
// Email:     wujing03@bilibili.com
// Date:      2026
// Copyright: Copyright (c) 2026 维杰（邬晶）

// internal/tool/code_search.go — code_search 工具处理器
//
// 提供两个高效操作供 LLM 分析代码库：
//   - tree  : 递归列出目录树（自动过滤噪音目录）
//   - grep  : 递归搜索文件内容（关键词/正则，带上下文行）
package tool

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// skipDirs 自动跳过的噪音目录
var skipDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"__pycache__":  true,
	".venv":        true,
	"vendor":       true,
	".idea":        true,
	".vscode":      true,
	".DS_Store":    true,
	"bin":          true,
	"dist":         true,
	"build":        true,
	".next":        true,
}

const (
	codeTreeMaxEntries = 500
	codeGrepMaxSize    = 1 << 20 // 1 MB
)

type codeSearchArgs struct {
	Action       string `json:"action"`
	Path         string `json:"path"`
	MaxDepth     int    `json:"max_depth"`
	Include      string `json:"include"`
	Pattern      string `json:"pattern"`
	MaxResults   int    `json:"max_results"`
	ContextLines int    `json:"context_lines"`
}

func handleCodeSearch(_ context.Context, argsJSON string) (string, error) {
	var args codeSearchArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("code_search: invalid args: %w", err)
	}
	if args.Path == "" {
		return "", fmt.Errorf("code_search: path is required")
	}

	switch args.Action {
	case "tree":
		return codeTree(args)
	case "grep":
		return codeGrep(args)
	default:
		return "", fmt.Errorf("code_search: unknown action %q, expected \"tree\" or \"grep\"", args.Action)
	}
}

// codeTree 递归列出目录树，返回缩进文本格式
func codeTree(args codeSearchArgs) (string, error) {
	maxDepth := args.MaxDepth
	if maxDepth <= 0 {
		maxDepth = 5
	}

	root := filepath.Clean(args.Path)
	info, err := os.Stat(root)
	if err != nil {
		return "", fmt.Errorf("code_search tree: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("code_search tree: %q is not a directory", root)
	}

	var sb strings.Builder
	sb.WriteString(filepath.Base(root) + "/\n")

	count := 0
	truncated := false

	var walk func(dir string, depth int, prefix string) error
	walk = func(dir string, depth int, prefix string) error {
		if depth > maxDepth {
			return nil
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil // 跳过无权限目录
		}
		for _, entry := range entries {
			if skipDirs[entry.Name()] {
				continue
			}
			if count >= codeTreeMaxEntries {
				truncated = true
				return nil
			}
			// 文件名 glob 过滤（仅对文件生效）
			if args.Include != "" && !entry.IsDir() {
				matched, _ := filepath.Match(args.Include, entry.Name())
				if !matched {
					continue
				}
			}
			count++
			if entry.IsDir() {
				sb.WriteString(prefix + "  " + entry.Name() + "/\n")
				_ = walk(filepath.Join(dir, entry.Name()), depth+1, prefix+"  ")
			} else {
				sb.WriteString(prefix + "  " + entry.Name() + "\n")
			}
		}
		return nil
	}

	_ = walk(root, 1, "")

	if truncated {
		sb.WriteString(fmt.Sprintf("\n[截断：已列出 %d 个条目，超出上限 %d]\n", count, codeTreeMaxEntries))
	}

	return sb.String(), nil
}

// codeGrep 递归搜索文件内容，返回匹配行及上下文
func codeGrep(args codeSearchArgs) (string, error) {
	if args.Pattern == "" {
		return "", fmt.Errorf("code_search grep: pattern is required")
	}
	maxResults := args.MaxResults
	if maxResults <= 0 {
		maxResults = 50
	}
	contextLines := args.ContextLines
	if contextLines < 0 {
		contextLines = 0
	}

	re, err := regexp.Compile(args.Pattern)
	if err != nil {
		return "", fmt.Errorf("code_search grep: invalid pattern: %w", err)
	}

	root := filepath.Clean(args.Path)

	type result struct {
		file    string
		lineNum int
		lines   []string // 上下文 + 匹配行 + 下文，格式化后
	}

	var results []result
	truncated := false

	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		// 文件名 glob 过滤
		if args.Include != "" {
			matched, _ := filepath.Match(args.Include, d.Name())
			if !matched {
				return nil
			}
		}
		// 文件大小限制
		fi, err := d.Info()
		if err != nil || fi.Size() > codeGrepMaxSize {
			return nil
		}

		// 读取文件，检测二进制
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		if bytes.IndexByte(data, 0) >= 0 {
			return nil // 跳过二进制文件
		}

		// 逐行扫描
		scanner := bufio.NewScanner(bytes.NewReader(data))
		var allLines []string
		for scanner.Scan() {
			allLines = append(allLines, scanner.Text())
		}

		for i, line := range allLines {
			if len(results) >= maxResults {
				truncated = true
				return filepath.SkipAll
			}
			if !re.MatchString(line) {
				continue
			}
			// 计算上下文范围
			start := i - contextLines
			if start < 0 {
				start = 0
			}
			end := i + contextLines + 1
			if end > len(allLines) {
				end = len(allLines)
			}
			// 构建格式化片段
			rel, _ := filepath.Rel(root, path)
			var formatted []string
			for j := start; j < end; j++ {
				lineNo := j + 1
				marker := " "
				if j == i {
					marker = ">"
				}
				formatted = append(formatted, fmt.Sprintf("%s %s:%d: %s", marker, rel, lineNo, allLines[j]))
			}
			results = append(results, result{file: rel, lineNum: i + 1, lines: formatted})
		}
		return nil
	})
	if err != nil && err != filepath.SkipAll {
		return "", fmt.Errorf("code_search grep: walk error: %w", err)
	}

	if len(results) == 0 {
		return fmt.Sprintf("(no matches for %q in %s)", args.Pattern, root), nil
	}

	var sb strings.Builder
	for idx, r := range results {
		if idx > 0 {
			sb.WriteString("---\n")
		}
		for _, l := range r.lines {
			sb.WriteString(l + "\n")
		}
	}
	if truncated {
		sb.WriteString(fmt.Sprintf("\n[截断：已返回 %d 个匹配，超出 max_results 限制]\n", maxResults))
	}

	return sb.String(), nil
}
