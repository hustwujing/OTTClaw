// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/skill/loader.go — 扫描 skills/ 目录，解析每个技能文件夹中的 SKILL.md
//
// 目录约定（多用户隔离版）：
//
//	skills/
//	  system/              ← 系统技能（所有用户可见）
//	    {skill_name}/
//	      SKILL.md
//	      script/
//	      assets/
//	  users/               ← 用户个人技能（仅对应用户可见）
//	    {user_id}/
//	      {skill_name}/
//	        SKILL.md
//	        script/
//	        assets/
//
// 服务启动时只加载 HEAD；LLM 需要时通过 get_skill_content 工具懒加载 CONTENT
package skill

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// Separator 技能文件中 HEAD 与 CONTENT 的标准分割线（30 个 '='）。
// 写入时使用此标准格式；解析时放宽为 10 个以上连续 '=' 即可匹配。
const Separator = "=============================="

// separatorRe 匹配行首 10 个以上连续 '='（容忍 LLM 输出数量不精确）
var separatorRe = regexp.MustCompile(`(?m)^={10,}\s*$`)

// Head 技能头部信息（仅元数据，不含完整内容）
type Head struct {
	SkillID     string
	Name        string
	DisplayName string // 向用户展示的技能名称（优雅名字），未设置时回退到 Name
	Description string
	Trigger     string
	Enable      bool // enable: true 时才加载；缺省或 false 时跳过
}

// Skill 完整技能（HEAD + CONTENT + 根目录路径）
type Skill struct {
	Head
	Content  string // 完整内容，懒加载时才有值
	RootPath string // 技能文件夹的绝对路径，供 run_script / read_asset 使用
}

// Store 全局技能仓库，服务启动时初始化
var Store = &store{
	systemSkills: make(map[string]*Skill),
	userSkills:   make(map[string]map[string]*Skill),
}

type store struct {
	mu           sync.RWMutex
	systemSkills map[string]*Skill            // skill_id → Skill（系统技能，所有用户可见）
	userSkills   map[string]map[string]*Skill // userID → skill_id → Skill（用户个人技能）
	dir          string                       // skills 根目录，LoadAll 调用后保存，供 Reload 使用
}

// LoadAll 扫描 dir/system/（系统技能）和 dir/users/{userID}/（用户个人技能）。
// 先在不加锁的情况下读取所有文件，再原子替换内部 map。
func (s *store) LoadAll(dir string) error {
	newSystem := make(map[string]*Skill)
	newUsers := make(map[string]map[string]*Skill)

	// -- 加载系统技能 --
	systemDir := filepath.Join(dir, "system")
	if entries, err := os.ReadDir(systemDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			skillDir := filepath.Join(systemDir, e.Name())
			mdPath := filepath.Join(skillDir, "SKILL.md")
			if _, err := os.Stat(mdPath); os.IsNotExist(err) {
				continue
			}
			sk, err := parseSkillFile(mdPath, skillDir)
			if err != nil {
				return fmt.Errorf("parse system skill in %q: %w", skillDir, err)
			}
			if !sk.Enable {
				continue
			}
			newSystem[sk.SkillID] = sk
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read system skills dir %q: %w", systemDir, err)
	}

	// -- 加载用户个人技能 --
	usersDir := filepath.Join(dir, "users")
	if userEntries, err := os.ReadDir(usersDir); err == nil {
		for _, userEntry := range userEntries {
			if !userEntry.IsDir() {
				continue
			}
			userID := userEntry.Name()
			userDir := filepath.Join(usersDir, userID)
			userSkillMap := make(map[string]*Skill)

			if skillEntries, err := os.ReadDir(userDir); err == nil {
				for _, e := range skillEntries {
					if !e.IsDir() {
						continue
					}
					skillDir := filepath.Join(userDir, e.Name())
					mdPath := filepath.Join(skillDir, "SKILL.md")
					if _, err := os.Stat(mdPath); os.IsNotExist(err) {
						continue
					}
					sk, err := parseSkillFile(mdPath, skillDir)
					if err != nil {
						return fmt.Errorf("parse user skill in %q: %w", skillDir, err)
					}
					if !sk.Enable {
						continue
					}
					userSkillMap[sk.SkillID] = sk
				}
			}

			// -- 加载 self-improving 自动生成技能 --
			selfImprovingDir := filepath.Join(userDir, "self-improving", "skills")
			if siEntries, err := os.ReadDir(selfImprovingDir); err == nil {
				for _, e := range siEntries {
					if !e.IsDir() {
						continue
					}
					skillDir := filepath.Join(selfImprovingDir, e.Name())
					mdPath := filepath.Join(skillDir, "SKILL.md")
					if _, err := os.Stat(mdPath); os.IsNotExist(err) {
						continue
					}
					sk, err := parseSkillFile(mdPath, skillDir)
					if err != nil {
						return fmt.Errorf("parse self-improving skill in %q: %w", skillDir, err)
					}
					if !sk.Enable {
						continue
					}
					userSkillMap[sk.SkillID] = sk
				}
			}

			if len(userSkillMap) > 0 {
				newUsers[userID] = userSkillMap
			}
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read users skills dir %q: %w", usersDir, err)
	}

	if len(newSystem) == 0 && len(newUsers) == 0 {
		return fmt.Errorf("no skills found in %q (expected system/ and/or users/ subdirectories)", dir)
	}

	// 原子替换 map 并保存 dir
	s.mu.Lock()
	s.systemSkills = newSystem
	s.userSkills = newUsers
	s.dir = dir
	s.mu.Unlock()
	return nil
}

// Reload 重新扫描 skills 目录，原子替换 map。
// 用于新增技能文件后热更新，无需重启服务。
func (s *store) Reload() error {
	s.mu.RLock()
	dir := s.dir
	s.mu.RUnlock()
	if dir == "" {
		return fmt.Errorf("skills directory not initialized, call LoadAll first")
	}
	return s.LoadAll(dir)
}

// GetBaseDir 返回 skills 根目录路径
func (s *store) GetBaseDir() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.dir
}

// GetUserSkillsDir 返回用户个人技能存放根目录：{skills_root}/users/{userID}
// write_skill_file 工具调用时用于确定写入位置。
func (s *store) GetUserSkillsDir(userID string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.dir == "" {
		return ""
	}
	return filepath.Join(s.dir, "users", userID)
}

// GetSystemSkillsDir 返回系统技能存放根目录：{skills_root}/system
// 初始化阶段（initialized=false）write_skill_* 工具写入此目录。
func (s *store) GetSystemSkillsDir() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.dir == "" {
		return ""
	}
	return filepath.Join(s.dir, "system")
}

// GetHead 返回指定用户可见的技能 HEAD 列表（系统技能 + 用户个人技能）。
// 若用户技能与系统技能的 skill_id 相同，用户技能优先（允许个性化覆盖）。
func (s *store) GetHead(userID string) []Head {
	s.mu.RLock()
	defer s.mu.RUnlock()

	merged := make(map[string]*Skill, len(s.systemSkills))
	for id, sk := range s.systemSkills {
		merged[id] = sk
	}
	if userSkills, ok := s.userSkills[userID]; ok {
		for id, sk := range userSkills {
			merged[id] = sk
		}
	}

	heads := make([]Head, 0, len(merged))
	for _, sk := range merged {
		heads = append(heads, sk.Head)
	}
	return heads
}

// GetAllHeads 返回所有技能（系统 + 全部用户）的 HEAD 列表，供内部管理场景使用（不做用户过滤）。
func (s *store) GetAllHeads() []Head {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var heads []Head
	for _, sk := range s.systemSkills {
		heads = append(heads, sk.Head)
	}
	for _, userSkills := range s.userSkills {
		for _, sk := range userSkills {
			heads = append(heads, sk.Head)
		}
	}
	return heads
}

// GetSkillHead 返回指定用户可见的指定技能 HEAD；用户技能优先于系统技能。
func (s *store) GetSkillHead(userID, skillID string) (Head, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if userSkills, ok := s.userSkills[userID]; ok {
		if sk, ok := userSkills[skillID]; ok {
			return sk.Head, true
		}
	}
	if sk, ok := s.systemSkills[skillID]; ok {
		return sk.Head, true
	}
	return Head{}, false
}

// GetContent 返回指定用户可见的指定技能 CONTENT；用户技能优先于系统技能。
func (s *store) GetContent(userID, skillID string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if userSkills, ok := s.userSkills[userID]; ok {
		if sk, ok := userSkills[skillID]; ok {
			return sk.Content, nil
		}
	}
	if sk, ok := s.systemSkills[skillID]; ok {
		return sk.Content, nil
	}
	return "", fmt.Errorf("skill %q not found", skillID)
}

// GetSkillDir 返回指定用户可见的指定技能根目录绝对路径；用户技能优先于系统技能。
// 供 run_script / read_asset 工具解析脚本和资产路径。
func (s *store) GetSkillDir(userID, skillID string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if userSkills, ok := s.userSkills[userID]; ok {
		if sk, ok := userSkills[skillID]; ok {
			return sk.RootPath, true
		}
	}
	if sk, ok := s.systemSkills[skillID]; ok {
		return sk.RootPath, true
	}
	return "", false
}

// ----- 文件解析 -----

// ParseContent 解析 SKILL.md 字符串内容，校验格式并返回 Skill（RootPath 留空）。
// 供 write_skill_file 等工具在写入前做格式验证。
//
// 校验规则：
//   - 存在两个分隔符（恰好 30 个 '='）
//   - HEAD 中 skill_id 和 name 字段非空
//   - CONTENT 区（第二分隔符之后）非空
func ParseContent(content string) (*Skill, error) {
	locs := separatorRe.FindAllStringIndex(content, 3)
	if len(locs) < 2 {
		return nil, fmt.Errorf("缺少分隔符（需要至少两行连续 10 个以上 '='）")
	}

	headSection := strings.TrimSpace(content[locs[0][1]:locs[1][0]])
	bodySection := strings.TrimSpace(content[locs[1][1]:])

	sk := &Skill{Content: bodySection}
	sk.SkillID, sk.Name, sk.DisplayName, sk.Description, sk.Trigger, sk.Enable = parseHead(headSection)

	if sk.SkillID == "" {
		return nil, fmt.Errorf("HEAD 中 skill_id 字段为空或缺失")
	}
	if sk.Name == "" {
		return nil, fmt.Errorf("HEAD 中 name 字段为空或缺失")
	}
	if sk.Content == "" {
		return nil, fmt.Errorf("CONTENT 区（第二个分隔符之后）不能为空")
	}
	return sk, nil
}

// parseSkillFile 读取 SKILL.md 并解析 HEAD + CONTENT，同时记录根目录路径
func parseSkillFile(mdPath, rootPath string) (*Skill, error) {
	data, err := os.ReadFile(mdPath)
	if err != nil {
		return nil, err
	}
	sk, err := ParseContent(string(data))
	if err != nil {
		return nil, fmt.Errorf("in %q: %w", mdPath, err)
	}
	sk.RootPath = rootPath
	return sk, nil
}

// parseHead 从 HEAD 文本段中提取各字段（简单 key: value 格式）
func parseHead(head string) (skillID, name, displayName, desc, trigger string, enable bool) {
	for _, line := range strings.Split(head, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		idx := strings.Index(line, ":")
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		switch key {
		case "skill_id":
			skillID = val
		case "name":
			name = val
		case "display_name":
			displayName = val
		case "description":
			desc = val
		case "trigger":
			trigger = val
		case "enable":
			enable = strings.EqualFold(val, "true")
		}
	}
	return
}

// FormatHeadsForPrompt 将所有 HEAD 格式化为 prompt 文本块
func FormatHeadsForPrompt(heads []Head) string {
	var sb strings.Builder
	for i, h := range heads {
		if i > 0 {
			sb.WriteString("\n---\n")
		}
		displayName := h.DisplayName
		if displayName == "" {
			displayName = h.Name
		}
		sb.WriteString(fmt.Sprintf("skill_id: %s\nname: %s\ndisplay_name: %s\ndescription: %s\ntrigger: %s",
			h.SkillID, h.Name, displayName, h.Description, h.Trigger))
	}
	return sb.String()
}
