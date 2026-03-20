// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/cron/schedule.go — schedule JSON 解析与 NextRunAt 计算
package cron

import (
	"encoding/json"
	"fmt"
	"time"

	robfigcron "github.com/robfig/cron/v3"
)

// ScheduleKind 调度类型
type ScheduleKind string

const (
	KindCron  ScheduleKind = "cron"
	KindEvery ScheduleKind = "every"
	KindAt    ScheduleKind = "at"
)

// Schedule 调度配置，对应 ScheduleJSON 字段
type Schedule struct {
	Kind    ScheduleKind `json:"kind"`
	Expr    string       `json:"expr,omitempty"`    // cron：标准 5 字段表达式
	TZ      string       `json:"tz,omitempty"`      // cron：时区（如 "Asia/Shanghai"），默认 UTC
	EveryMs int64        `json:"everyMs,omitempty"` // every：固定间隔（毫秒）
	At      string       `json:"at,omitempty"`      // at：单次绝对时间（ISO 8601 / RFC3339）
}

// ParseSchedule 反序列化 schedule JSON 字符串
func ParseSchedule(schedJSON string) (Schedule, error) {
	var s Schedule
	if err := json.Unmarshal([]byte(schedJSON), &s); err != nil {
		return s, fmt.Errorf("parse schedule: %w", err)
	}
	if s.Kind == "" {
		return s, fmt.Errorf("schedule.kind is required (cron|every|at)")
	}
	return s, nil
}

// CalcNextRunAt 根据调度配置和上次运行时间计算下一次运行时间。
// lastRunAt 为 nil 表示从未运行过，此时以 createdAt 为基准计算首次运行时间。
// 返回 nil 表示不再需要运行（at 类型已过期或已完成）。
func CalcNextRunAt(sched Schedule, lastRunAt *time.Time, createdAt time.Time) (*time.Time, error) {
	switch sched.Kind {
	case KindCron:
		loc := time.UTC
		if sched.TZ != "" {
			var err error
			loc, err = time.LoadLocation(sched.TZ)
			if err != nil {
				return nil, fmt.Errorf("load timezone %q: %w", sched.TZ, err)
			}
		}
		parser := robfigcron.NewParser(
			robfigcron.Minute | robfigcron.Hour | robfigcron.Dom | robfigcron.Month | robfigcron.Dow,
		)
		cronSched, err := parser.Parse(sched.Expr)
		if err != nil {
			return nil, fmt.Errorf("parse cron expr %q: %w", sched.Expr, err)
		}
		from := createdAt.In(loc)
		if lastRunAt != nil {
			from = lastRunAt.In(loc)
		}
		next := cronSched.Next(from)
		return &next, nil

	case KindEvery:
		if sched.EveryMs <= 0 {
			return nil, fmt.Errorf("everyMs must be > 0")
		}
		from := createdAt
		if lastRunAt != nil {
			from = *lastRunAt
		}
		next := from.Add(time.Duration(sched.EveryMs) * time.Millisecond)
		return &next, nil

	case KindAt:
		t, err := time.Parse(time.RFC3339, sched.At)
		if err != nil {
			return nil, fmt.Errorf("parse at time %q: %w", sched.At, err)
		}
		if time.Now().After(t) {
			return nil, nil // 已过期，不再调度
		}
		return &t, nil

	default:
		return nil, fmt.Errorf("unknown schedule kind %q", sched.Kind)
	}
}
