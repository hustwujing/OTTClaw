// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/tool/exec_safety.go — exec 工具的静态安全分析
//
// 两个入口：
//   analyzeShellAST(code)  — 用 mvdan.cc/sh/v3 解析 shell 脚本 AST
//   analyzePythonAST(code) — 启动 python3 子进程做 AST 分析，失败时降级为正则
//
// 返回值均为 []string：命中的危险原因，空列表表示可以直接执行。
package tool

import (
	"context"
	"encoding/json"
	osexec "os/exec"
	"regexp"
	"strings"
	"time"

	"mvdan.cc/sh/v3/syntax"
)

// ── Shell AST 分析 ─────────────────────────────────────────────────────────────

// analyzeShellAST 用 mvdan.cc/sh/v3 解析 bash 脚本，检测危险操作。
func analyzeShellAST(code string) []string {
	f, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(code), "")
	if err != nil {
		// 语法错误无法分析，保守处理：要求用户确认
		return []string{"shell 脚本语法解析失败（建议人工审查）: " + err.Error()}
	}

	var risks []string
	seen := make(map[string]bool)
	add := func(r string) {
		if !seen[r] {
			seen[r] = true
			risks = append(risks, r)
		}
	}

	syntax.Walk(f, func(node syntax.Node) bool {
		switch n := node.(type) {
		case *syntax.CallExpr:
			name := shellCallName(n)
			switch name {
			case "sudo":
				add("sudo 提权执行")
			case "rm":
				if shellHasFlag(n.Args, 'r') {
					add("rm -r 递归删除")
				}
			case "dd":
				if shellHasArgPrefix(n.Args, "of=/dev/") {
					add("dd 直接写入设备")
				}
			case "mkfs", "fdisk", "parted", "shred", "wipefs":
				add(name + " 磁盘/设备操作")
			case "eval":
				if shellHasDynamic(n.Args) {
					add("eval 动态执行代码")
				}
			}

		case *syntax.Stmt:
			for _, redir := range n.Redirs {
				if redir.Op == syntax.RdrOut || redir.Op == syntax.AppOut {
					target := shellWordLit(redir.Word)
					// /dev/null、/dev/stdin、/dev/stdout、/dev/stderr 是标准伪文件，无害，跳过。
					safeDevPaths := []string{"/dev/null", "/dev/stdin", "/dev/stdout", "/dev/stderr"}
					isSafeDev := false
					for _, safe := range safeDevPaths {
						if target == safe {
							isSafeDev = true
							break
						}
					}
					if !isSafeDev {
						for _, p := range []string{"/etc/", "/dev/", "/boot/", "/sys/"} {
							if strings.HasPrefix(target, p) {
								add("重定向写入敏感路径: " + target)
								break
							}
						}
					}
					if strings.Contains(target, ".ssh/") || strings.Contains(target, ".aws/") {
						add("重定向写入敏感路径: " + target)
					}
				}
			}

		case *syntax.BinaryCmd:
			// 检测 `curl url | bash` / `wget url | sh` 远程代码执行模式
			if n.Op == syntax.Pipe || n.Op == syntax.PipeAll {
				rightName := ""
				if call, ok := n.Y.Cmd.(*syntax.CallExpr); ok {
					rightName = shellCallName(call)
				}
				if rightName == "bash" || rightName == "sh" {
					if call, ok := n.X.Cmd.(*syntax.CallExpr); ok {
						leftName := shellCallName(call)
						if leftName == "curl" || leftName == "wget" {
							add(leftName + " | " + rightName + " 远程代码执行")
						}
					}
				}
			}
		}
		return true
	})

	// 正则补充：AST Walk 只检测直接的 X|Y 管道结构，
	// 链式管道（a | curl | bash）需要正则兜底。
	if regexp.MustCompile(`\|\s*(bash|sh)\b`).MatchString(code) {
		add("管道传输到 bash/sh（疑似远程代码执行）")
	}

	return risks
}

func shellCallName(n *syntax.CallExpr) string {
	if len(n.Args) == 0 {
		return ""
	}
	return shellWordLit(n.Args[0])
}

func shellWordLit(w *syntax.Word) string {
	if w == nil || len(w.Parts) == 0 {
		return ""
	}
	if l, ok := w.Parts[0].(*syntax.Lit); ok {
		return l.Value
	}
	return ""
}

// shellHasFlag 检查 args（跳过第一个命令名）中是否存在含目标字母的短参数（如 -r、-rf）。
func shellHasFlag(args []*syntax.Word, flag rune) bool {
	for _, w := range args[1:] {
		v := shellWordLit(w)
		if strings.HasPrefix(v, "-") && !strings.HasPrefix(v, "--") && strings.ContainsRune(v, flag) {
			return true
		}
	}
	return false
}

// shellHasArgPrefix 检查 args 中是否存在以 prefix 开头的参数（用于 dd of=/dev/...）。
func shellHasArgPrefix(args []*syntax.Word, prefix string) bool {
	for _, w := range args[1:] {
		if strings.HasPrefix(shellWordLit(w), prefix) {
			return true
		}
	}
	return false
}

// shellHasDynamic 检查 args 中是否含有动态展开部分（命令替换、参数展开等）。
func shellHasDynamic(args []*syntax.Word) bool {
	for _, w := range args[1:] {
		for _, p := range w.Parts {
			switch p.(type) {
			case *syntax.CmdSubst, *syntax.ParamExp, *syntax.ArithmExp, *syntax.ProcSubst:
				return true
			}
		}
	}
	return false
}

// ── Python AST 分析 ────────────────────────────────────────────────────────────

// pythonASTScript 是嵌入的 Python 分析脚本。
// 从 stdin 读取 Python 源码，输出 JSON {"risks":["..."]}。
// 使用 Python 自身的 ast 模块，可追踪 import 别名，比正则更准确。
const pythonASTScript = `
import ast, json, sys

DANGER_ATTR = {
    ('os','system'),('os','popen'),('os','remove'),('os','unlink'),('os','rmdir'),
    ('subprocess','run'),('subprocess','call'),('subprocess','Popen'),
    ('subprocess','check_output'),('subprocess','getoutput'),('subprocess','getstatusoutput'),
    ('shutil','rmtree'),('shutil','rmdir'),
}
DANGER_BUILTINS = {'eval','exec','__import__'}
SENSITIVE = ['/etc/passwd','/etc/shadow','.ssh/','.aws/credentials']

code = sys.stdin.read()
try:
    tree = ast.parse(code)
except SyntaxError as e:
    print(json.dumps({'risks':[],'parse_error':str(e)})); sys.exit(0)

mod_aliases = {}
func_aliases = {}
for node in ast.walk(tree):
    if isinstance(node, ast.Import):
        for a in node.names:
            mod_aliases[a.asname or a.name] = a.name
    elif isinstance(node, ast.ImportFrom):
        m = node.module or ''
        for a in node.names:
            func_aliases[a.asname or a.name] = (m, a.name)

risks, seen = [], set()
def add(r):
    if r not in seen: seen.add(r); risks.append(r)

for node in ast.walk(tree):
    if isinstance(node, ast.Call):
        f = node.func; ln = getattr(node, 'lineno', '?')
        if isinstance(f, ast.Attribute) and isinstance(f.value, ast.Name):
            obj, meth = f.value.id, f.attr
            real_mod = mod_aliases.get(obj, obj)
            if (real_mod, meth) in DANGER_ATTR:
                add(f'{obj}.{meth}() 危险调用 (line {ln})')
        elif isinstance(f, ast.Name):
            nm = f.id
            if nm in DANGER_BUILTINS:
                add(f'{nm}() 动态代码执行 (line {ln})')
            if nm in func_aliases:
                fm, fa = func_aliases[nm]
                if (fm, fa) in DANGER_ATTR:
                    add(f'{nm}() 危险调用 via import (line {ln})')
    elif isinstance(node, ast.Constant) and isinstance(node.value, str):
        for p in SENSITIVE:
            if p in node.value: add(f'包含敏感路径: {p}'); break

print(json.dumps({'risks': risks}))
`

// analyzePythonAST 启动 python3 子进程做 AST 分析。
// python3 不可用时降级为 analyzePythonRegex。
func analyzePythonAST(code string) []string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := osexec.CommandContext(ctx, "python3", "-c", pythonASTScript)
	cmd.Stdin = strings.NewReader(code)
	out, err := cmd.Output()
	if err != nil {
		return analyzePythonRegex(code)
	}

	var result struct {
		Risks []string `json:"risks"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return analyzePythonRegex(code)
	}
	return result.Risks
}

// analyzePythonRegex 是 python3 不可用时的正则降级实现。
var pythonDangerRe = []*regexp.Regexp{
	regexp.MustCompile(`\bos\.(system|popen|remove|unlink|rmdir)\s*\(`),
	regexp.MustCompile(`\bsubprocess\.(run|call|Popen|check_output|getoutput)\s*\(`),
	regexp.MustCompile(`\bshutil\.(rmtree|rmdir)\s*\(`),
	regexp.MustCompile(`\beval\s*\(`),
	regexp.MustCompile(`\bexec\s*\(`),
	regexp.MustCompile(`(?i)(/etc/passwd|/etc/shadow|\.ssh/|\.aws/credentials)`),
}

func analyzePythonRegex(code string) []string {
	var risks []string
	for _, re := range pythonDangerRe {
		if m := re.FindString(code); m != "" {
			risks = append(risks, "危险模式: "+m)
		}
	}
	return risks
}
