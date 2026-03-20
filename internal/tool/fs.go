// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/tool/fs.go — 文件系统操作工具
//
// 安全策略：
//   - 所有操作：路径必须在项目工作目录内（checkInProject），禁止访问宿主机任意路径
//   - fs_read / fs_list：额外阻断 .env、*.db、data/ 等敏感路径（checkSensitivePath）
//   - fs_write / fs_delete / fs_move / fs_mkdir：
//       · 仅限 uploads/ 或 output/（共享）
//       · 或 skills/users/{userID}/（当前用户专属目录，不可跨用户操作）
//       · skills/system/ 永远只读，任何用户都不能写入或删除
package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"OTTClaw/config"
	"OTTClaw/internal/storage"
)

// ── 安全路径检查 ──────────────────────────────────────────────────────────────

// writableDirs 返回允许写入/删除/移动的目录绝对路径列表
func writableDirs() []string {
	cwd, _ := os.Getwd()
	return []string{
		filepath.Join(cwd, config.Cfg.UploadDir),
		filepath.Join(cwd, config.Cfg.OutputDir),
		filepath.Join(cwd, config.Cfg.SkillsDir),
	}
}

// checkWritable 验证 path 在允许写入的目录下，防止路径穿越。
// 规则：
//   - 初始化阶段（initialized=false）：不限制目录，允许写入任意项目内路径
//   - uploads/ 和 output/ 对所有用户开放（不含用户隔离，为共享目录）
//   - skills/system/ 只读，任何人不得写入或删除（初始化完成后）
//   - skills/ 下的写操作仅限 skills/users/{userID}/ 目录，防止跨用户污染
func checkWritable(path, userID string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("invalid path: %w", err)
	}

	// 初始化阶段：解除所有目录限制
	appCfg, _ := storage.GetAppConfig()
	if !appCfg.Initialized {
		return nil
	}

	cwd, _ := os.Getwd()

	// 保护系统技能目录：initialized 后永久只读
	systemSkillsDir := filepath.Join(cwd, config.Cfg.SkillsDir, "system")
	if abs == systemSkillsDir || strings.HasPrefix(abs, systemSkillsDir+string(os.PathSeparator)) {
		return fmt.Errorf("路径 %q 属于系统内置技能目录，初始化完成后禁止修改或删除", path)
	}

	// skills/ 下仅允许写入当前用户自己的目录
	skillsDir := filepath.Join(cwd, config.Cfg.SkillsDir)
	if abs == skillsDir || strings.HasPrefix(abs, skillsDir+string(os.PathSeparator)) {
		if userID == "" {
			return fmt.Errorf("无法确认用户身份，拒绝写入 skills 目录")
		}
		userSkillsDir := filepath.Join(skillsDir, "users", userID)
		if abs != userSkillsDir && !strings.HasPrefix(abs, userSkillsDir+string(os.PathSeparator)) {
			return fmt.Errorf("路径 %q 超出当前用户的技能目录范围（仅允许操作 skills/users/%s/）\n提示：写入 skill 的 script/ 或 assets/ 文件请使用 skill(action=write, skill_id=..., sub_path=script/xxx 或 assets/xxx)，无需手动创建目录", path, userID)
		}
		return nil
	}

	// uploads/ 和 output/ 均可写入
	for _, dir := range []string{
		filepath.Join(cwd, config.Cfg.UploadDir),
		filepath.Join(cwd, config.Cfg.OutputDir),
	} {
		if abs == dir || strings.HasPrefix(abs, dir+string(os.PathSeparator)) {
			return nil
		}
	}

	return fmt.Errorf("路径 %q 不在允许操作的目录内（uploads、output、skills/users/%s）", path, userID)
}

// checkInProject 确保路径在项目工作目录或管理员配置的白名单目录内，
// 防止 LLM 访问宿主机上的任意路径。
// 适用于所有读操作（fs_read / fs_list / fs_stat）；写操作另由 checkWritable 控制。
// 白名单目录由管理员在 bootstrap 初始化时通过 update_role_md(extra_fs_dirs=[...]) 配置，
// 持久化于 config/app.json，运行时动态生效，无需重启。
func checkInProject(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("invalid path: %w", err)
	}

	appCfg, _ := storage.GetAppConfig()

	// 初始化阶段：解除目录限制
	if !appCfg.Initialized {
		return nil
	}

	// 允许：项目工作目录
	cwd, _ := os.Getwd()
	if abs == cwd || strings.HasPrefix(abs, cwd+string(os.PathSeparator)) {
		return nil
	}
	// 允许：管理员配置的白名单目录（持久化于 config/app.json）
	for _, dir := range appCfg.ExtraFsDirs {
		if dir == "" {
			continue
		}
		if abs == dir || strings.HasPrefix(abs, dir+string(os.PathSeparator)) {
			return nil
		}
	}
	return fmt.Errorf("路径 %q 超出项目目录范围，禁止访问（如需访问额外路径，请联系管理员在初始化时配置）", path)
}

// checkSensitivePath 阻止读取含敏感凭证的文件或目录
func checkSensitivePath(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("invalid path: %w", err)
	}
	cwd, _ := os.Getwd()

	// 阻止读取 .env 文件（含任何形如 .env.xxx 的变体）
	base := strings.ToLower(filepath.Base(abs))
	if base == ".env" || strings.HasPrefix(base, ".env.") {
		return fmt.Errorf("拒绝访问：.env 文件包含敏感配置，禁止读取")
	}

	// 阻止读取数据库文件
	if strings.HasSuffix(base, ".db") || strings.HasSuffix(base, ".sqlite") || strings.HasSuffix(base, ".sqlite3") {
		return fmt.Errorf("拒绝访问：数据库文件禁止直接读取")
	}

	// 阻止读取 data/ 目录（含所有子路径）
	dataDir := filepath.Join(cwd, "data")
	if abs == dataDir || strings.HasPrefix(abs, dataDir+string(os.PathSeparator)) {
		return fmt.Errorf("拒绝访问：data 目录下的文件禁止读取")
	}

	// 阻止读取编译产物目录
	binDir := filepath.Join(cwd, "bin")
	if abs == binDir || strings.HasPrefix(abs, binDir+string(os.PathSeparator)) {
		return fmt.Errorf("拒绝访问：bin 目录禁止读取")
	}

	return nil
}

// ── fs_list ───────────────────────────────────────────────────────────────────

type fsEntry struct {
	Name    string    `json:"name"`
	IsDir   bool      `json:"is_dir"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
}

func handleFsList(_ context.Context, argsJSON string) (string, error) {
	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("parse fs_list args: %w", err)
	}
	if strings.TrimSpace(args.Path) == "" {
		return "", fmt.Errorf("path is required")
	}
	if err := checkInProject(args.Path); err != nil {
		return "", err
	}
	if err := checkSensitivePath(args.Path); err != nil {
		return "", err
	}
	entries, err := os.ReadDir(args.Path)
	if err != nil {
		return "", fmt.Errorf("list dir: %w", err)
	}
	result := make([]fsEntry, 0, len(entries))
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		result = append(result, fsEntry{
			Name:    e.Name(),
			IsDir:   e.IsDir(),
			Size:    info.Size(),
			ModTime: info.ModTime(),
		})
	}
	b, _ := json.Marshal(result)
	return string(b), nil
}

// ── fs_stat ───────────────────────────────────────────────────────────────────

func handleFsStat(_ context.Context, argsJSON string) (string, error) {
	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("parse fs_stat args: %w", err)
	}
	if strings.TrimSpace(args.Path) == "" {
		return "", fmt.Errorf("path is required")
	}
	if err := checkInProject(args.Path); err != nil {
		return "", err
	}
	info, err := os.Stat(args.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return `{"exists":false}`, nil
		}
		return "", fmt.Errorf("stat: %w", err)
	}
	result := map[string]any{
		"exists":   true,
		"name":     info.Name(),
		"is_dir":   info.IsDir(),
		"size":     info.Size(),
		"mod_time": info.ModTime(),
		"mode":     info.Mode().String(),
	}
	b, _ := json.Marshal(result)
	return string(b), nil
}

// ── fs_read ───────────────────────────────────────────────────────────────────

func handleFsRead(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("parse fs_read args: %w", err)
	}
	if strings.TrimSpace(args.Path) == "" {
		return "", fmt.Errorf("path is required")
	}
	if err := checkInProject(args.Path); err != nil {
		return "", err
	}
	if err := checkSensitivePath(args.Path); err != nil {
		return "", err
	}

	ext := strings.ToLower(filepath.Ext(args.Path))

	// 图片文件：自动路由到 read_image，返回多模态结果供 LLM 视觉分析。
	// argsJSON 含 path 字段，与 handleReadImage 期望的格式兼容（多余的 action 字段被忽略）。
	if _, isImg := imgMediaTypes[ext]; isImg {
		return handleReadImage(ctx, argsJSON)
	}

	// Office 文档：提示使用专用工具，避免尝试以文本方式读取二进制格式。
	switch ext {
	case ".doc", ".docx", ".pptx":
		return "", fmt.Errorf("office document (%s): use read_file(path) for text extraction", ext)
	case ".pdf":
		return "", fmt.Errorf("PDF file: use read_file(path) for quick text extraction, or read_pdf(path) for page-level control")
	}

	// 大小限制：超出则拒绝，避免大文件全量入内存和 token
	info, err := os.Stat(args.Path)
	if err != nil {
		return "", fmt.Errorf("stat file: %w", err)
	}
	if max := config.Cfg.FsReadMaxBytes; max > 0 && info.Size() > int64(max) {
		return "", fmt.Errorf("file too large (%d KB, limit %d KB); for images use read_image, for PDFs use read_pdf",
			info.Size()/1024, max/1024)
	}

	b, err := os.ReadFile(args.Path)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}

	// 二进制检测：空字节是可靠的非文本指示符。
	// 检测到二进制时返回有用错误，避免乱码写入 DB 并污染上下文。
	if bytes.IndexByte(b, 0) >= 0 {
		mime := http.DetectContentType(b)
		return "", fmt.Errorf("binary file (detected: %s); use read_image for images, read_file for .docx/.pdf/.pptx", mime)
	}

	return string(b), nil
}

// ── fs_write ──────────────────────────────────────────────────────────────────

func handleFsWrite(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Path    string `json:"path"`
		Content string `json:"content"`
		Append  bool   `json:"append"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("parse fs_write args: %w", err)
	}
	if strings.TrimSpace(args.Path) == "" {
		return "", fmt.Errorf("path is required")
	}
	if err := checkWritable(args.Path, userIDFromCtx(ctx)); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(args.Path), 0o755); err != nil {
		return "", fmt.Errorf("mkdir parent: %w", err)
	}
	flag := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	if args.Append {
		flag = os.O_WRONLY | os.O_CREATE | os.O_APPEND
	}
	f, err := os.OpenFile(args.Path, flag, 0o644)
	if err != nil {
		return "", fmt.Errorf("open file: %w", err)
	}
	defer f.Close()
	if _, err := f.WriteString(args.Content); err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}
	return fmt.Sprintf("已写入 %s（%d 字节）", args.Path, len(args.Content)), nil
}

// ── fs_delete ─────────────────────────────────────────────────────────────────

func handleFsDelete(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Path      string `json:"path"`
		Recursive bool   `json:"recursive"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("parse fs_delete args: %w", err)
	}
	if strings.TrimSpace(args.Path) == "" {
		return "", fmt.Errorf("path is required")
	}
	if err := checkWritable(args.Path, userIDFromCtx(ctx)); err != nil {
		return "", err
	}
	var err error
	if args.Recursive {
		err = os.RemoveAll(args.Path)
	} else {
		err = os.Remove(args.Path)
	}
	if err != nil {
		return "", fmt.Errorf("delete: %w", err)
	}
	return fmt.Sprintf("已删除 %s", args.Path), nil
}

// ── fs_move ───────────────────────────────────────────────────────────────────

func handleFsMove(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Src string `json:"src"`
		Dst string `json:"dst"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("parse fs_move args: %w", err)
	}
	if strings.TrimSpace(args.Src) == "" || strings.TrimSpace(args.Dst) == "" {
		return "", fmt.Errorf("src and dst are required")
	}
	uid := userIDFromCtx(ctx)
	if err := checkWritable(args.Src, uid); err != nil {
		return "", fmt.Errorf("src: %w", err)
	}
	if err := checkWritable(args.Dst, uid); err != nil {
		return "", fmt.Errorf("dst: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(args.Dst), 0o755); err != nil {
		return "", fmt.Errorf("mkdir dst parent: %w", err)
	}
	if err := os.Rename(args.Src, args.Dst); err != nil {
		// 跨设备时 Rename 不可用，回退到 copy + delete
		if err2 := fsCopyFile(args.Src, args.Dst); err2 != nil {
			return "", fmt.Errorf("move (copy): %w", err2)
		}
		if err2 := os.Remove(args.Src); err2 != nil {
			return "", fmt.Errorf("move (remove src): %w", err2)
		}
	}
	return fmt.Sprintf("已移动 %s → %s", args.Src, args.Dst), nil
}

// fsCopyFile 跨设备回退：逐字节复制
func fsCopyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// ── fs_mkdir ──────────────────────────────────────────────────────────────────

func handleFsMkdir(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("parse fs_mkdir args: %w", err)
	}
	if strings.TrimSpace(args.Path) == "" {
		return "", fmt.Errorf("path is required")
	}
	if err := checkWritable(args.Path, userIDFromCtx(ctx)); err != nil {
		return "", err
	}
	if err := os.MkdirAll(args.Path, 0o755); err != nil {
		return "", fmt.Errorf("mkdir: %w", err)
	}
	return fmt.Sprintf("目录已创建：%s", args.Path), nil
}

// ── fs（统一入口）────────────────────────────────────────────────────────────
// handleFs 通过 action 字段分发到各文件系统操作处理器，替代 7 个独立工具。
// action: list / stat / read / write / delete / move / mkdir
func handleFs(ctx context.Context, argsJSON string) (string, error) {
	var base struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &base); err != nil {
		return "", fmt.Errorf("parse fs action: %w", err)
	}
	switch base.Action {
	case "list":
		return handleFsList(ctx, argsJSON)
	case "stat":
		return handleFsStat(ctx, argsJSON)
	case "read":
		return handleFsRead(ctx, argsJSON)
	case "write":
		return handleFsWrite(ctx, argsJSON)
	case "delete":
		return handleFsDelete(ctx, argsJSON)
	case "move":
		return handleFsMove(ctx, argsJSON)
	case "mkdir":
		return handleFsMkdir(ctx, argsJSON)
	default:
		return "", fmt.Errorf("unknown fs action: %q (valid: list/stat/read/write/delete/move/mkdir)", base.Action)
	}
}
