// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/skill/evict.go — Self-improving skill Redis-style approximate LFU eviction
//
// Three core ideas (same as Redis approximate LFU):
//
//  1. Time-decay counter
//     Counter decays exponentially with a configurable half-life (default 24 h).
//     Effective score at time T = counter × 0.5^((T−lastAccess)/halfLifeHours)
//     Old high-frequency skills slowly cool down; new hot skills rise naturally.
//
//  2. New-skill protection window
//     A freshly created skill is immune to eviction for protectMinutes (default 60 min).
//     This gives it time to accumulate heat before competing.
//
//  3. Recency-weighted frequency (not historical sum)
//     Because of the decay, the counter only reflects recent usage,
//     not lifetime totals.  Old skills cannot permanently monopolise the cache.
//
// Usage data is persisted in {userSkillsDir}/self-improving/usage.json.
package skill

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"OTTClaw/internal/logger"
)

// SkillUsageEntry stores Redis-style approximate LFU state for one self-improving skill.
type SkillUsageEntry struct {
	// Counter is the decayed frequency value.
	// Creation: 1.0.  Each access: decay to now, then +1.
	// Effective hotness at time T = Counter × 0.5^((T−LastAccessAt)/halfLifeHours)
	Counter      float64 `json:"counter"`
	LastAccessAt int64   `json:"last_access_at"` // Unix seconds — used for decay
	CreatedAt    int64   `json:"created_at"`      // Unix seconds — used for protection window
}

// usageMu serialises all reads/writes of usage.json to prevent concurrent corruption.
var usageMu sync.Mutex

// SelfImprovingSkillsDir returns the skills sub-directory for self-improving skills.
// userSkillsDir = {skills_root}/users/{userID}
func SelfImprovingSkillsDir(userSkillsDir string) string {
	return filepath.Join(userSkillsDir, "self-improving", "skills")
}

func usageFilePath(userSkillsDir string) string {
	return filepath.Join(userSkillsDir, "self-improving", "usage.json")
}

// userIDHint extracts the last path component as a best-effort user ID for log tags.
func userIDHint(userSkillsDir string) string {
	return filepath.Base(userSkillsDir)
}

func loadUsage(userSkillsDir string) map[string]SkillUsageEntry {
	data, err := os.ReadFile(usageFilePath(userSkillsDir))
	if err != nil {
		return make(map[string]SkillUsageEntry)
	}
	var m map[string]SkillUsageEntry
	if err := json.Unmarshal(data, &m); err != nil {
		return make(map[string]SkillUsageEntry)
	}
	return m
}

// saveUsage writes usage.json atomically via temp-file rename.
func saveUsage(userSkillsDir string, m map[string]SkillUsageEntry) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	path := usageFilePath(userSkillsDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// effectiveScore returns the decayed counter value of e at the given moment.
// halfLifeHours ≤ 0 defaults to 24 h.
func effectiveScore(e SkillUsageEntry, now time.Time, halfLifeHours int) float64 {
	h := float64(halfLifeHours)
	if h <= 0 {
		h = 24
	}
	elapsed := now.Sub(time.Unix(e.LastAccessAt, 0)).Hours()
	if elapsed <= 0 {
		return e.Counter
	}
	return e.Counter * math.Pow(0.5, elapsed/h)
}

// InitSelfImprovingUsage seeds a brand-new skill with counter=1 and records its birth time.
// The skill will be protected from eviction for protectMinutes after creation.
// No-op if an entry already exists (update is handled by RecordSelfImprovingUse).
// userSkillsDir = {skills_root}/users/{userID}
func InitSelfImprovingUsage(userSkillsDir, skillID string) {
	uid := userIDHint(userSkillsDir)
	usageMu.Lock()
	defer usageMu.Unlock()
	m := loadUsage(userSkillsDir)
	if _, exists := m[skillID]; exists {
		logger.Debug("skill", uid, "", fmt.Sprintf("[lfu-init] skill=%s already tracked, skip", skillID), 0)
		return
	}
	now := time.Now()
	m[skillID] = SkillUsageEntry{Counter: 1.0, LastAccessAt: now.Unix(), CreatedAt: now.Unix()}
	_ = saveUsage(userSkillsDir, m)
	logger.Debug("skill", uid, "", fmt.Sprintf(
		"[lfu-init] skill=%s counter=1.0 created_at=%s (protected until %s)",
		skillID,
		now.Format("15:04:05"),
		now.Add(0).Format("15:04:05"), // placeholder; actual protect window set at evict time
	), 0)
}

// RecordSelfImprovingUse applies time-decay then increments the counter by 1.
// This is the Redis LFU pattern: decay first so old heat fades, then reward recent access.
// decayHours is the half-life in hours (≤0 defaults to 24).
// userSkillsDir = {skills_root}/users/{userID}
func RecordSelfImprovingUse(userSkillsDir, skillID string, decayHours int) {
	uid := userIDHint(userSkillsDir)
	usageMu.Lock()
	defer usageMu.Unlock()
	m := loadUsage(userSkillsDir)
	now := time.Now()
	e := m[skillID]
	if e.CreatedAt == 0 {
		e.CreatedAt = now.Unix()
	}

	elapsedH := now.Sub(time.Unix(e.LastAccessAt, 0)).Hours()
	before := e.Counter
	// Decay existing heat, then reward this access.
	decayed := effectiveScore(e, now, decayHours)
	e.Counter = decayed + 1.0
	e.LastAccessAt = now.Unix()
	m[skillID] = e
	_ = saveUsage(userSkillsDir, m)

	logger.Debug("skill", uid, "", fmt.Sprintf(
		"[lfu-use] skill=%s raw=%.3f elapsed_h=%.2f decayed=%.3f → +1 → %.3f (half_life=%dh)",
		skillID, before, elapsedH, decayed, e.Counter, decayHours,
	), 0)
}

// EvictSelfImprovingSkills removes the coldest skills when total count > maxSkills.
//
// Eviction rules:
//   - Skills within their protection window (age < protectMinutes) are immune.
//   - Among eviction candidates, sort by effective (decayed) score ASC.
//   - If all skills are protected and we are still over limit, skip eviction
//     (wait for protection windows to expire).
//
// Returns the list of evicted skill IDs.
// decayHours: half-life in hours (≤0 defaults to 24).
// protectMinutes: new-skill protection window (≤0 disables protection).
// userSkillsDir = {skills_root}/users/{userID}
func EvictSelfImprovingSkills(userSkillsDir string, maxSkills, decayHours, protectMinutes int) ([]string, error) {
	uid := userIDHint(userSkillsDir)

	if maxSkills <= 0 {
		logger.Debug("skill", uid, "", "[lfu-evict] disabled (maxSkills=0), skip", 0)
		return nil, nil
	}
	siSkillsDir := SelfImprovingSkillsDir(userSkillsDir)
	dirEntries, err := os.ReadDir(siSkillsDir)
	if err != nil {
		if os.IsNotExist(err) {
			logger.Debug("skill", uid, "", "[lfu-evict] skills dir not found, skip", 0)
			return nil, nil
		}
		return nil, err
	}

	// Collect valid skill IDs (directories with a SKILL.md).
	var skillIDs []string
	for _, e := range dirEntries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(siSkillsDir, e.Name(), "SKILL.md")); err == nil {
			skillIDs = append(skillIDs, e.Name())
		}
	}

	logger.Debug("skill", uid, "", fmt.Sprintf(
		"[lfu-evict] check: total=%d limit=%d decay_h=%d protect_min=%d",
		len(skillIDs), maxSkills, decayHours, protectMinutes,
	), 0)

	if len(skillIDs) <= maxSkills {
		logger.Debug("skill", uid, "", fmt.Sprintf(
			"[lfu-evict] within limit (%d/%d), nothing to evict", len(skillIDs), maxSkills,
		), 0)
		return nil, nil
	}

	usageMu.Lock()
	defer usageMu.Unlock()

	usage := loadUsage(userSkillsDir)
	now := time.Now()
	protectDur := time.Duration(protectMinutes) * time.Minute
	need := len(skillIDs) - maxSkills

	logger.Debug("skill", uid, "", fmt.Sprintf(
		"[lfu-evict] over limit by %d — ranking all %d skills:", need, len(skillIDs),
	), 0)

	type candidate struct {
		id        string
		score     float64
		protected bool
		age       time.Duration
	}
	all := make([]candidate, 0, len(skillIDs))

	for _, id := range skillIDs {
		e := usage[id]
		score := effectiveScore(e, now, decayHours)
		age := now.Sub(time.Unix(e.CreatedAt, 0))
		protected := protectMinutes > 0 && e.CreatedAt > 0 && age < protectDur

		status := "candidate"
		if protected {
			status = "PROTECTED"
		}
		logger.Debug("skill", uid, "", fmt.Sprintf(
			"[lfu-evict]   %-30s score=%.4f  raw_counter=%.3f  age=%s  %s",
			id, score, e.Counter,
			fmtDuration(age),
			status,
		), 0)

		all = append(all, candidate{id: id, score: score, protected: protected, age: age})
	}

	// Separate protected from eviction candidates.
	var candidates []candidate
	for _, c := range all {
		if !c.protected {
			candidates = append(candidates, c)
		}
	}

	// Sort ascending: lowest decayed score evicted first.
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].score < candidates[j].score
	})

	if len(candidates) == 0 {
		logger.Debug("skill", uid, "", fmt.Sprintf(
			"[lfu-evict] all %d skills are protected, cannot evict yet", len(skillIDs),
		), 0)
		return nil, nil
	}
	if len(candidates) < need {
		logger.Debug("skill", uid, "", fmt.Sprintf(
			"[lfu-evict] only %d/%d skills are evictable (rest protected), will evict %d",
			len(candidates), len(skillIDs), len(candidates),
		), 0)
	}

	// Log the sorted eviction order.
	orderParts := make([]string, len(candidates))
	for i, c := range candidates {
		orderParts[i] = fmt.Sprintf("%s(%.4f)", c.id, c.score)
	}
	logger.Debug("skill", uid, "", fmt.Sprintf(
		"[lfu-evict] eviction order (coldest first): %s", strings.Join(orderParts, " > "),
	), 0)

	evicted := make([]string, 0, need)
	for i := 0; i < need && i < len(candidates); i++ {
		c := candidates[i]
		if err := os.RemoveAll(filepath.Join(siSkillsDir, c.id)); err != nil {
			logger.Warn("skill", uid, "", fmt.Sprintf(
				"[lfu-evict] failed to remove skill=%s: %v", c.id, err,
			), 0)
			continue // best-effort
		}
		delete(usage, c.id)
		evicted = append(evicted, c.id)
		logger.Debug("skill", uid, "", fmt.Sprintf(
			"[lfu-evict] ✓ evicted skill=%s score=%.4f age=%s",
			c.id, c.score, fmtDuration(c.age),
		), 0)
	}

	if len(evicted) > 0 {
		_ = saveUsage(userSkillsDir, usage)
	}

	logger.Debug("skill", uid, "", fmt.Sprintf(
		"[lfu-evict] done: evicted=%d protected_skipped=%d remaining=%d/%d",
		len(evicted), len(skillIDs)-len(candidates), len(skillIDs)-len(evicted), maxSkills,
	), 0)

	return evicted, nil
}

// fmtDuration formats a duration as a compact human-readable string.
func fmtDuration(d time.Duration) string {
	if d < 0 {
		return "0s"
	}
	if d < time.Minute {
		return fmt.Sprintf("%.0fs", d.Seconds())
	}
	if d < time.Hour {
		return fmt.Sprintf("%.0fm", d.Minutes())
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%.1fh", d.Hours())
	}
	return fmt.Sprintf("%.1fd", d.Hours()/24)
}
