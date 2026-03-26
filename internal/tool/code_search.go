// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/tool/code_search.go — code_search 工具处理器
//
// 提供 8 种操作供 LLM 分析代码库和文档：
//
//   - tree       : 递归列出目录树，自动过滤 node_modules/.git/vendor 等噪音目录。
//                  适合：快速了解项目结构、定位目录层级。
//
//   - grep       : 正则搜索文件内容，返回匹配行 + 可选上下文行，自动跳过二进制文件。
//                  适合：查找函数/变量/字符串在哪里被定义或引用。
//
//   - glob       : 按 glob 模式匹配文件路径，支持 ** 跨目录通配（如 **/*.go）。
//                  适合：按命名规则批量定位文件，比 tree 更精准、比 grep 更轻量。
//
//   - git        : 只读 git 操作——log（提交历史）/ blame（逐行归属）/
//                  diff（变更内容）/ show（单次提交详情）/ status / branch / tag。
//                  适合：理解代码演变历史、定位某行代码的修改原因。
//
//   - outline    : 提取源文件的符号大纲（Go/Python/TS/JS/Rust/Java 的函数、类型、
//                  接口；Markdown 的标题层级），支持单文件或整个目录。
//                  适合：在不读全文的前提下掌握文件结构，辅助 chunk_read 定位目标块。
//
//   - chunk_read : 将文件按固定行数分块（默认 80 行/块）逐块返回，附行号和分块元信息。
//                  适合：阅读超大文件，避免一次性读取超出上下文窗口。
//
//   - ast_grep   : 通过 AST 模式做结构化代码搜索（需安装 ast-grep）。
//                  用 $VAR / $$$VARS 占位，匹配语法结构而非字面文本。
//                  适合：跨格式查找同类代码模式，如所有 if err != nil 处理、特定函数签名。
//
//   - comby      : 通过分隔符平衡模板做结构感知搜索（需安装 comby，语言无关）。
//                  用 :[VAR] 占位，自动平衡括号/引号/大括号，捕获任意嵌套内容。
//                  适合：跨语言搜索结构相似但文本不同的代码片段，grep 正则难以覆盖的场景。
package tool

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
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
	codeTreeMaxEntries   = 500
	codeGrepMaxSize      = 1 << 20 // 1 MB
	codeGlobMaxResults   = 300
	codeGitMaxOutput     = 12000
	codeChunkDefaultSize = 80
	codeOutlineMaxFiles  = 50
)

type codeSearchArgs struct {
	Action       string `json:"action"`
	Path         string `json:"path"`
	MaxDepth     int    `json:"max_depth"`
	Include      string `json:"include"`
	Pattern      string `json:"pattern"`
	MaxResults   int    `json:"max_results"`
	ContextLines int    `json:"context_lines"`
	// git
	GitAction string `json:"git_action"` // log | blame | diff | show | status | branch | tag
	Revision  string `json:"revision"`   // commit/range（diff/show/blame/log 可选）
	N         int    `json:"n"`          // log 条数上限（默认 20）
	// chunk_read
	Chunk     int `json:"chunk"`      // 第几块（1-based，默认 1）
	ChunkSize int `json:"chunk_size"` // 每块行数（默认 80）
	// ast_grep
	Lang string `json:"lang"` // 语言标识（go/python/js/ts/rust/java/cpp/...）
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
	case "glob":
		return codeGlob(args)
	case "git":
		return codeGit(args)
	case "outline":
		return codeOutline(args)
	case "chunk_read":
		return codeChunkRead(args)
	case "ast_grep":
		return codeAstGrep(args)
	case "comby":
		return codeComby(args)
	default:
		return "", fmt.Errorf("code_search: unknown action %q (valid: tree/grep/glob/git/outline/chunk_read/ast_grep/comby)", args.Action)
	}
}

// ───────────────────────────── tree ─────────────────────────────

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

// ───────────────────────────── grep ─────────────────────────────

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
		lines   []string
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

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		if bytes.IndexByte(data, 0) >= 0 {
			return nil // 跳过二进制文件
		}

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
			start := i - contextLines
			if start < 0 {
				start = 0
			}
			end := i + contextLines + 1
			if end > len(allLines) {
				end = len(allLines)
			}
			rel, _ := filepath.Rel(root, path)
			var formatted []string
			for j := start; j < end; j++ {
				marker := " "
				if j == i {
					marker = ">"
				}
				formatted = append(formatted, fmt.Sprintf("%s %s:%d: %s", marker, rel, j+1, allLines[j]))
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

// ───────────────────────────── glob ─────────────────────────────

// codeGlob 按 glob 模式匹配文件路径，支持 ** 跨目录通配。
// pattern 示例：**/*.go  /  src/**/*.ts  /  **/test_*.py
func codeGlob(args codeSearchArgs) (string, error) {
	if args.Pattern == "" {
		return "", fmt.Errorf("code_search glob: pattern is required")
	}
	maxResults := args.MaxResults
	if maxResults <= 0 {
		maxResults = codeGlobMaxResults
	}

	root := filepath.Clean(args.Path)
	pattern := filepath.ToSlash(args.Pattern)

	var matches []string
	truncated := false

	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if len(matches) >= maxResults {
			truncated = true
			return filepath.SkipAll
		}
		rel, _ := filepath.Rel(root, path)
		if globMatch(pattern, filepath.ToSlash(rel)) {
			matches = append(matches, filepath.ToSlash(rel))
		}
		return nil
	})

	if len(matches) == 0 {
		return fmt.Sprintf("(no files matching %q under %s)", args.Pattern, root), nil
	}
	var sb strings.Builder
	for _, m := range matches {
		sb.WriteString(m + "\n")
	}
	if truncated {
		sb.WriteString(fmt.Sprintf("\n[截断：已返回 %d 条匹配，超出 max_results]\n", maxResults))
	}
	return sb.String(), nil
}

// globMatch 将以 / 分隔的 glob pattern 与文件相对路径进行匹配，支持 **。
func globMatch(pattern, path string) bool {
	return globMatchParts(strings.Split(pattern, "/"), strings.Split(path, "/"))
}

// globMatchParts 递归匹配 pattern 段与路径段。
// ** 匹配 0 到多个路径段；其余段用 filepath.Match 做单级通配。
func globMatchParts(pat, path []string) bool {
	for {
		if len(pat) == 0 {
			return len(path) == 0
		}
		if pat[0] == "**" {
			pat = pat[1:]
			if len(pat) == 0 {
				return true // ** 在末尾：匹配剩余所有路径
			}
			// ** 可消耗 0~N 个路径段
			for i := 0; i <= len(path); i++ {
				if globMatchParts(pat, path[i:]) {
					return true
				}
			}
			return false
		}
		if len(path) == 0 {
			return false
		}
		ok, _ := filepath.Match(pat[0], path[0])
		if !ok {
			return false
		}
		pat = pat[1:]
		path = path[1:]
	}
}

// ───────────────────────────── git ─────────────────────────────

var allowedGitSubcmds = map[string]bool{
	"log": true, "blame": true, "diff": true,
	"show": true, "status": true, "branch": true, "tag": true,
}

// codeGit 执行只读 git 命令。
// path    = 仓库根目录（所有 git_action 均适用）
// pattern = blame 时为文件路径；log 时为 --grep 过滤词；diff 时为文件路径过滤
// revision = diff/show/blame 的提交引用；log 的分支/范围
// n       = log 最多显示条数（默认 20）
func codeGit(args codeSearchArgs) (string, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return "", fmt.Errorf("git is not installed or not in PATH; install git first (e.g. `brew install git` / `apt install git`)")
	}
	if !allowedGitSubcmds[args.GitAction] {
		return "", fmt.Errorf("code_search git: git_action %q not allowed (valid: log/blame/diff/show/status/branch/tag)", args.GitAction)
	}

	root := filepath.Clean(args.Path)
	gitArgs := []string{"-C", root}

	switch args.GitAction {
	case "log":
		n := args.N
		if n <= 0 {
			n = 20
		}
		gitArgs = append(gitArgs, "log", "--oneline", fmt.Sprintf("-n%d", n))
		if args.Revision != "" {
			gitArgs = append(gitArgs, args.Revision)
		}
		if args.Pattern != "" {
			gitArgs = append(gitArgs, "--grep="+args.Pattern)
		}

	case "blame":
		if args.Pattern == "" {
			return "", fmt.Errorf("code_search git blame: file path required in 'pattern' field")
		}
		gitArgs = append(gitArgs, "blame")
		if args.Revision != "" {
			gitArgs = append(gitArgs, args.Revision)
		}
		gitArgs = append(gitArgs, "--", args.Pattern)

	case "diff":
		gitArgs = append(gitArgs, "diff")
		if args.Revision != "" {
			gitArgs = append(gitArgs, args.Revision)
		}
		if args.Pattern != "" {
			gitArgs = append(gitArgs, "--", args.Pattern)
		}

	case "show":
		rev := args.Revision
		if rev == "" {
			rev = "HEAD"
		}
		gitArgs = append(gitArgs, "show", "--stat", rev)
		if args.Pattern != "" {
			gitArgs = append(gitArgs, "--", args.Pattern)
		}

	case "status":
		gitArgs = append(gitArgs, "status", "--short")

	case "branch":
		gitArgs = append(gitArgs, "branch", "-v")

	case "tag":
		gitArgs = append(gitArgs, "tag", "-l")
	}

	cmd := exec.Command("git", gitArgs...) //nolint:gosec — subcommand validated above
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", args.GitAction, msg)
	}

	result := out.String()
	if len(result) > codeGitMaxOutput {
		result = result[:codeGitMaxOutput] + fmt.Sprintf("\n[截断：输出超过 %d 字符]\n", codeGitMaxOutput)
	}
	return result, nil
}

// ───────────────────────────── outline ─────────────────────────────

// symbolPattern 描述一条语言符号提取规则。
type symbolPattern struct {
	re   *regexp.Regexp
	kind string // func / method / struct / class / interface / type / heading 等
}

// langPatterns 按文件扩展名映射符号提取规则列表（同一行取首个匹配）。
var langPatterns map[string][]symbolPattern

func init() {
	goPatterns := []symbolPattern{
		{regexp.MustCompile(`^func\s+\(([^)]+)\)\s+(\w+)\s*[(\[]`), "method"},
		{regexp.MustCompile(`^func\s+(\w+)\s*[(\[]`), "func"},
		{regexp.MustCompile(`^type\s+(\w+)\s+interface`), "interface"},
		{regexp.MustCompile(`^type\s+(\w+)\s+struct`), "struct"},
		{regexp.MustCompile(`^type\s+(\w+)\s+`), "type"},
	}
	pyPatterns := []symbolPattern{
		{regexp.MustCompile(`^\s*(?:async\s+)?def\s+(\w+)\s*\(`), "func"},
		{regexp.MustCompile(`^class\s+(\w+)`), "class"},
	}
	tsPatterns := []symbolPattern{
		{regexp.MustCompile(`^(?:export\s+)?(?:default\s+)?(?:async\s+)?function\s+(\w+)\s*[(<]`), "func"},
		{regexp.MustCompile(`^(?:export\s+)?(?:abstract\s+)?class\s+(\w+)`), "class"},
		{regexp.MustCompile(`^(?:export\s+)?interface\s+(\w+)`), "interface"},
		{regexp.MustCompile(`^(?:export\s+)?type\s+(\w+)\s*[=<]`), "type"},
	}
	jsPatterns := []symbolPattern{
		{regexp.MustCompile(`^(?:export\s+)?(?:default\s+)?(?:async\s+)?function\s+(\w+)\s*\(`), "func"},
		{regexp.MustCompile(`^(?:export\s+)?class\s+(\w+)`), "class"},
	}
	rsPatterns := []symbolPattern{
		{regexp.MustCompile(`^\s*(?:pub(?:\([^)]*\))?\s+)?(?:async\s+)?fn\s+(\w+)\s*[(<]`), "func"},
		{regexp.MustCompile(`^\s*(?:pub(?:\([^)]*\))?\s+)?struct\s+(\w+)`), "struct"},
		{regexp.MustCompile(`^\s*(?:pub(?:\([^)]*\))?\s+)?enum\s+(\w+)`), "enum"},
		{regexp.MustCompile(`^\s*(?:pub(?:\([^)]*\))?\s+)?trait\s+(\w+)`), "trait"},
		{regexp.MustCompile(`^\s*impl(?:\s+\w+\s+for)?\s+(\w+)`), "impl"},
	}
	javaPatterns := []symbolPattern{
		{regexp.MustCompile(`^(?:public|private|protected)?\s*(?:abstract\s+|final\s+)*class\s+(\w+)`), "class"},
		{regexp.MustCompile(`^(?:public|private|protected)?\s*interface\s+(\w+)`), "interface"},
		{regexp.MustCompile(`^\s*(?:public|private|protected|static|abstract|final|\s)+\w[\w<>, \[\]]+\s+(\w+)\s*\(`), "method"},
	}
	mdPatterns := []symbolPattern{
		{regexp.MustCompile(`^(#{1,6})\s+(.+)`), "heading"},
	}

	langPatterns = map[string][]symbolPattern{
		".go":       goPatterns,
		".py":       pyPatterns,
		".ts":       tsPatterns,
		".tsx":      tsPatterns,
		".js":       jsPatterns,
		".jsx":      jsPatterns,
		".rs":       rsPatterns,
		".java":     javaPatterns,
		".md":       mdPatterns,
		".markdown": mdPatterns,
	}
}

// codeOutline 提取代码或文档文件的结构大纲。
// path 为文件时返回该文件的符号列表；为目录时递归处理其中的已知语言文件。
func codeOutline(args codeSearchArgs) (string, error) {
	path := filepath.Clean(args.Path)
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("code_search outline: %w", err)
	}
	if info.IsDir() {
		return codeOutlineDir(args, path)
	}
	return codeOutlineFile(path)
}

func codeOutlineFile(path string) (string, error) {
	ext := strings.ToLower(filepath.Ext(path))
	patterns := langPatterns[ext]
	if patterns == nil {
		return fmt.Sprintf("(outline: unsupported file type %q)", ext), nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("code_search outline: %w", err)
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return "", fmt.Errorf("code_search outline: binary file")
	}

	var sb strings.Builder
	sb.WriteString(path + "\n")

	scanner := bufio.NewScanner(bytes.NewReader(data))
	lineNum := 0
	found := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()

		for _, sp := range patterns {
			m := sp.re.FindStringSubmatch(line)
			if m == nil {
				continue
			}

			if sp.kind == "heading" {
				// m[1] = "###", m[2] = heading text
				level := len(m[1])
				indent := strings.Repeat("  ", level-1)
				sb.WriteString(fmt.Sprintf("  %4d  %s%s %s\n", lineNum, indent, m[1], strings.TrimSpace(m[2])))
			} else if sp.kind == "method" && ext == ".go" {
				// m[1] = receiver, m[2] = method name
				recv := strings.TrimSpace(m[1])
				// 只取类型部分（去掉变量名）："r *Agent" → "*Agent"
				parts := strings.Fields(recv)
				recvType := parts[len(parts)-1]
				sb.WriteString(fmt.Sprintf("  %4d  [%-12s] (%s) %s\n", lineNum, sp.kind, recvType, m[2]))
			} else {
				name := ""
				if len(m) > 1 {
					name = m[1]
				}
				sb.WriteString(fmt.Sprintf("  %4d  [%-12s] %s\n", lineNum, sp.kind, name))
			}
			found++
			break
		}
	}

	if found == 0 {
		sb.WriteString("  (no symbols found)\n")
	}
	return sb.String(), nil
}

func codeOutlineDir(args codeSearchArgs, root string) (string, error) {
	var sb strings.Builder
	count := 0

	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if count >= codeOutlineMaxFiles {
			return filepath.SkipAll
		}
		// 文件过滤：优先 include glob，否则只处理已知语言文件
		ext := strings.ToLower(filepath.Ext(d.Name()))
		if args.Include != "" {
			ok, _ := filepath.Match(args.Include, d.Name())
			if !ok {
				return nil
			}
		} else if langPatterns[ext] == nil {
			return nil
		}

		result, err := codeOutlineFile(path)
		if err != nil {
			return nil
		}
		sb.WriteString(result + "\n")
		count++
		return nil
	})

	if sb.Len() == 0 {
		return "(no supported source files found)", nil
	}
	return sb.String(), nil
}

// ───────────────────────────── chunk_read ─────────────────────────────

// codeChunkRead 将文件按行分块后返回指定块，附行号。
// 适合大文件的分页阅读（避免一次性读取超出上下文）。
func codeChunkRead(args codeSearchArgs) (string, error) {
	chunkSize := args.ChunkSize
	if chunkSize <= 0 {
		chunkSize = codeChunkDefaultSize
	}
	chunk := args.Chunk
	if chunk <= 0 {
		chunk = 1
	}

	data, err := os.ReadFile(filepath.Clean(args.Path))
	if err != nil {
		return "", fmt.Errorf("code_search chunk_read: %w", err)
	}

	lines := strings.Split(string(data), "\n")
	// strings.Split 在末尾换行符后会产生一个空行，去掉
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	totalLines := len(lines)
	totalChunks := (totalLines + chunkSize - 1) / chunkSize
	if totalChunks == 0 {
		totalChunks = 1
	}

	if chunk > totalChunks {
		return "", fmt.Errorf("code_search chunk_read: chunk %d out of range (file has %d chunks of ~%d lines each)",
			chunk, totalChunks, chunkSize)
	}

	startLine := (chunk - 1) * chunkSize // 0-indexed
	endLine := startLine + chunkSize
	if endLine > totalLines {
		endLine = totalLines
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("[chunk %d/%d | lines %d-%d of %d | %s]\n",
		chunk, totalChunks, startLine+1, endLine, totalLines, filepath.Base(args.Path)))

	for i := startLine; i < endLine; i++ {
		sb.WriteString(fmt.Sprintf("%4d  %s\n", i+1, lines[i]))
	}

	if chunk < totalChunks {
		sb.WriteString(fmt.Sprintf("\n[%d chunks remaining — call chunk_read with chunk=%d to continue reading]\n",
			totalChunks-chunk, chunk+1))
	} else {
		sb.WriteString("\n[end of file]\n")
	}
	return sb.String(), nil
}

// ───────────────────────────── ast_grep ─────────────────────────────

// astGrepMatch 对应 ast-grep --json 输出的单条匹配。
type astGrepMatch struct {
	Text  string `json:"text"`
	File  string `json:"file"`
	Lines string `json:"lines"` // 含上下文的源码行
	Range struct {
		Start struct {
			Line   int `json:"line"`   // 0-indexed
			Column int `json:"column"` // 0-indexed
		} `json:"start"`
	} `json:"range"`
	MetaVariables struct {
		Single map[string]struct {
			Text string `json:"text"`
		} `json:"single"`
	} `json:"metaVariables"`
}

// codeAstGrep 通过 ast-grep 做结构化代码搜索。
// pattern : 代码模板，用 $VAR 或 $$$VARS 做占位（如 "fmt.Println($$$)"）
// lang    : 语言标识（go/python/js/ts/rust/java/cpp/...）
// path    : 搜索目录或文件
func codeAstGrep(args codeSearchArgs) (string, error) {
	if _, err := exec.LookPath("ast-grep"); err != nil {
		return "", fmt.Errorf(
			"ast-grep not found in PATH\n" +
				"install: brew install ast-grep  (macOS)\n" +
				"         cargo install ast-grep  (cross-platform)\n" +
				"docs:    https://ast-grep.github.io")
	}
	if args.Pattern == "" {
		return "", fmt.Errorf("code_search ast_grep: pattern is required (e.g. \"fmt.Println($$$)\")")
	}
	if args.Lang == "" {
		return "", fmt.Errorf("code_search ast_grep: lang is required (go/python/js/ts/rust/java/cpp/...)")
	}
	maxResults := args.MaxResults
	if maxResults <= 0 {
		maxResults = 50
	}

	// ast-grep run：--json=compact 输出单行 JSON 数组，便于解析
	cmdArgs := []string{"run", "--pattern", args.Pattern, "--lang", args.Lang, "--json=compact"}
	if args.ContextLines > 0 {
		cmdArgs = append(cmdArgs,
			"-A", fmt.Sprintf("%d", args.ContextLines),
			"-B", fmt.Sprintf("%d", args.ContextLines))
	}
	cmdArgs = append(cmdArgs, filepath.Clean(args.Path))

	cmd := exec.Command("ast-grep", cmdArgs...) //nolint:gosec — pattern/lang from user, path cleaned
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	_ = cmd.Run() // exit 1 = no matches（与 grep 行为一致），不当作 error

	if stderr.Len() > 0 {
		return "", fmt.Errorf("ast-grep: %s", strings.TrimSpace(stderr.String()))
	}

	raw := bytes.TrimSpace(out.Bytes())
	if len(raw) == 0 || string(raw) == "[]" || string(raw) == "null" {
		return fmt.Sprintf("(no matches for pattern %q in %s)", args.Pattern, args.Path), nil
	}

	var results []astGrepMatch
	if err := json.Unmarshal(raw, &results); err != nil {
		// 解析失败时原样返回（截断）
		s := string(raw)
		if len(s) > 4000 {
			s = s[:4000] + "\n[truncated]"
		}
		return s, nil
	}
	if len(results) == 0 {
		return fmt.Sprintf("(no matches for pattern %q in %s)", args.Pattern, args.Path), nil
	}

	truncated := false
	if len(results) > maxResults {
		results = results[:maxResults]
		truncated = true
	}

	root := filepath.Clean(args.Path)
	var sb strings.Builder
	for i, r := range results {
		if i > 0 {
			sb.WriteString("---\n")
		}
		rel, _ := filepath.Rel(root, r.File)
		if rel == "" {
			rel = r.File
		}
		sb.WriteString(fmt.Sprintf("%s:%d\n", rel, r.Range.Start.Line+1))

		// 优先用 lines（含上下文），退而求其次用 text
		src := r.Lines
		if src == "" {
			src = r.Text
		}
		for _, l := range strings.Split(strings.TrimRight(src, "\n"), "\n") {
			sb.WriteString("  " + l + "\n")
		}

		// 显示捕获的元变量（$VAR 绑定值）
		if len(r.MetaVariables.Single) > 0 {
			sb.WriteString("  vars:")
			for k, v := range r.MetaVariables.Single {
				sb.WriteString(fmt.Sprintf(" $%s=%q", k, v.Text))
			}
			sb.WriteString("\n")
		}
	}
	if truncated {
		sb.WriteString(fmt.Sprintf("\n[截断：已返回 %d 个匹配，超出 max_results]\n", maxResults))
	}
	return sb.String(), nil
}

// ───────────────────────────── comby ─────────────────────────────

// combyFileResult 对应 comby -json-lines 输出的单个文件结果。
type combyFileResult struct {
	URI     string `json:"uri"`
	Matches []struct {
		Matched     string `json:"matched"`
		Range       struct {
			Start struct {
				Line   int `json:"line"`   // 1-indexed
				Column int `json:"column"` // 1-indexed
			} `json:"start"`
		} `json:"range"`
		Environment []struct {
			Variable string `json:"variable"`
			Value    string `json:"value"`
		} `json:"environment"`
	} `json:"matches"`
}

// codeComby 通过 comby 做结构感知搜索（语言无关，基于分隔符平衡）。
// pattern : 匹配模板，用 :[VAR] 做占位（如 "fmt.Println(:[arg])"）
// include : 文件扩展名过滤（如 ".go"），可选
// path    : 搜索目录
func codeComby(args codeSearchArgs) (string, error) {
	if _, err := exec.LookPath("comby"); err != nil {
		return "", fmt.Errorf(
			"comby not found in PATH\n" +
				"install: bash <(curl -sL get.comby.dev)  (macOS/Linux)\n" +
				"         brew install comby              (macOS)\n" +
				"docs:    https://comby.dev")
	}
	if args.Pattern == "" {
		return "", fmt.Errorf("code_search comby: pattern is required (e.g. \"fmt.Println(:[arg])\")")
	}
	maxResults := args.MaxResults
	if maxResults <= 0 {
		maxResults = 50
	}

	// comby MATCH REWRITE [options]  —  rewrite 为空串表示只匹配不替换
	cmdArgs := []string{
		args.Pattern, "",
		"-match-only",
		"-json-lines",
		"-stdout",
		"-directory", filepath.Clean(args.Path),
	}
	if args.Include != "" {
		cmdArgs = append(cmdArgs, "-matcher", args.Include)
	}

	cmd := exec.Command("comby", cmdArgs...) //nolint:gosec — pattern from user, path cleaned
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	_ = cmd.Run() // comby 无匹配时也可能返回非零

	if out.Len() == 0 {
		if stderr.Len() > 0 {
			return "", fmt.Errorf("comby: %s", strings.TrimSpace(stderr.String()))
		}
		return fmt.Sprintf("(no matches for pattern %q in %s)", args.Pattern, args.Path), nil
	}

	// 解析 JSON lines，每行一个文件的匹配结果
	var sb strings.Builder
	total := 0
	truncated := false
	root := filepath.Clean(args.Path)

	scanner := bufio.NewScanner(&out)
	scanner.Buffer(make([]byte, 64*1024), 1*1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var fr combyFileResult
		if err := json.Unmarshal(line, &fr); err != nil {
			continue
		}
		rel, _ := filepath.Rel(root, fr.URI)
		if rel == "" {
			rel = fr.URI
		}
		for _, m := range fr.Matches {
			if total >= maxResults {
				truncated = true
				break
			}
			if total > 0 {
				sb.WriteString("---\n")
			}
			sb.WriteString(fmt.Sprintf("%s:%d\n", rel, m.Range.Start.Line))
			// 匹配内容（可能多行），每行缩进显示
			for _, l := range strings.Split(strings.TrimRight(m.Matched, "\n"), "\n") {
				sb.WriteString("  " + l + "\n")
			}
			// 显示捕获的 :[VAR] 绑定值
			if len(m.Environment) > 0 {
				sb.WriteString("  vars:")
				for _, e := range m.Environment {
					sb.WriteString(fmt.Sprintf(" :[%s]=%q", e.Variable, e.Value))
				}
				sb.WriteString("\n")
			}
			total++
		}
		if truncated {
			break
		}
	}

	if sb.Len() == 0 {
		return fmt.Sprintf("(no matches for pattern %q in %s)", args.Pattern, args.Path), nil
	}
	if truncated {
		sb.WriteString(fmt.Sprintf("\n[截断：已返回 %d 个匹配，超出 max_results]\n", maxResults))
	}
	return sb.String(), nil
}
