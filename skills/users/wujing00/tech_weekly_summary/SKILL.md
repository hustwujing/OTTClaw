==============================
skill_id: tech_weekly_summary
name: Tech Weekly Summary
display_name: Weekly Report Refiner
enable: true
description: Helps tech directors distill detailed weekly reports into concise summaries for non-technical executives, emphasizing quantification, rigor, and historical comparison — no vague or exaggerated language allowed
trigger: When the user says "summarize weekly report", "refine my weekly", "weekly summary", "this week's report", or pastes/uploads weekly report content
==============================

## Skill Goal

Help tech directors transform detailed technical weekly reports into executive-friendly summaries that non-technical bosses can quickly understand. The output must be:
- Quantified with specific numbers
- Rigorous without vague adjectives (禁止：特别、显著、非常、大幅)
- Compared against historical data when available
- Structured by topic with clear categories

---

## Executive Focus Areas

1. Team Efficiency (AI adoption, R&D productivity)
2. App Performance (launch time, stutter rate)
3. App Experience (user feedback, feature quality)
4. Tech Cost (bandwidth, CDN, servers)
5. Major Project Progress
6. Major Initiative Progress
7. External Customer Complaints
8. Channel Issue Handling
9. Risk Exposure
10. Online Incidents
11. Image Quality Progress

**不关注的内容（输出时忽略）：**
- DAU 相关数据（日活、周活、月活、PCU 等）
- 商业化相关内容（广告收入、效果广告、硬广、整合营销等）

---

## Output Format Requirements

### Style Rules
- One line per topic, concise and complete
- FORBIDDEN words: 特别、显著、非常、大幅、明显改善、效果很好
- MUST quantify: use numbers, percentages, week-over-week changes
- Clear structure: Category + Event + Solution + Outcome + Risk (as needed)
- Rigorous: if uncertain, don't write it; if written, must have source

### Output Example
```
研发：1)AI进展：服务端AI出码率58.25%（2802/4810新增行），AI提交commit占比33.47%，AI已超越全体人工成为本周第一大代码产出来源；2)版本迭代：185版本提测中，千行bug率0.78（优于184版1.19），Q1需求满足度100%、延期率0%；3)带宽成本：高峰期降低播放缓存策略全量上线，全天码率优化2.2%，卡顿率劣化12.2%但仍在1%以内；4)画质：VQA大盘56.47，OGV 78.52；5)性能：ijk播放器卡顿率0.70%，启动时长中位数2.0s（较均值2.5s提升20%）；6)客诉：本周进线266例，环比-14.19%，播放问题最多（10例）
```

---

## Execution Steps

### Step One: Get Weekly Report Content

If user pastes text directly:
- Use the pasted content as this week's report

If user uploads a file (.docx/.pdf/.txt):
- Call `read_file` for Office documents
- Call `read_pdf` for PDF files
- Use the extracted content as this week's report

Ask user: "请提供本周周报内容（可直接粘贴文本，或上传文件）"

---

### Step Two: Extract Structured Summary

Parse the weekly report and extract key information by executive focus area. Create a structured summary object:

```json
{
  "week": "YYYY-Www",
  "date": "YYYY-MM-DD",
  "summary": {
    "ai_efficiency": { "metrics": [], "progress": [], "risks": [] },
    "app_performance": { "metrics": [], "progress": [], "risks": [] },
    "app_experience": { "metrics": [], "progress": [], "risks": [] },
    "tech_cost": { "metrics": [], "progress": [], "risks": [] },
    "major_projects": { "metrics": [], "progress": [], "risks": [] },
    "major_initiatives": { "metrics": [], "progress": [], "risks": [] },
    "customer_complaints": { "metrics": [], "progress": [], "risks": [] },
    "channel_issues": { "metrics": [], "progress": [], "risks": [] },
    "risk_exposure": { "metrics": [], "progress": [], "risks": [] },
    "online_incidents": { "metrics": [], "progress": [], "risks": [] },
    "image_quality": { "metrics": [], "progress": [], "risks": [] }
  }
}
```

Extraction rules:
- metrics: Quantified indicators (numbers, percentages, week-over-week changes)
- progress: What was done and results achieved
- risks: Problems, pending issues, concerns
- **排除** DAU、商业化相关数据，不提取这些内容

---

### Step Three: Load Historical Data

Call `kv(action=get, key="tech_weekly_history")` to retrieve historical summaries from the past 3 months.

If no history exists (returns null), this is the first report — skip comparison and proceed to Step Five.

---

### Step Four: Compare with History

Compare this week's summary against historical data:

1. **Trend Analysis**: For each metric that appears in both this week and history, calculate the change direction and magnitude
2. **New Items**: Flag topics/metrics that appear this week but not in recent history
3. **Significant Changes**: Highlight metrics with >10% change that need explanation
4. **Persistent Risks**: Identify risks that have appeared for multiple consecutive weeks

Document key findings for inclusion in the final summary.

---

### Step Five: Generate Refined Summary

Based on the extracted data and historical comparison, generate the refined output:

1. Only include topics with significant progress, results, or changes
2. When referencing historical data, clearly state the change and your analysis of why
3. Strictly follow the output format and style rules
4. ALL information must come from the original weekly report — NEVER fabricate data
5. Use Chinese for the output content (matching the input language)
6. **不输出 DAU 和商业化相关内容**

Structure the output as:
```
研发：1)类别1：具体内容；2)类别2：具体内容；...
```

---

### Step Six: Save Data

1. Append this week's structured summary to history:
   ```
   kv(action=append, key="tech_weekly_history", value=<this week's summary object>)
   ```

2. Save this week's refined output:
   ```
   kv(action=append, key="tech_weekly_outputs", value={
     "week": "YYYY-Www",
     "date": "YYYY-MM-DD",
     "output": "<refined summary text>"
   })
   ```

3. Clean up old records: If history exceeds 13 weeks (3 months), retrieve all records with `kv(action=get)`, remove the oldest entries, and save back with `kv(action=set)`.

---

### Step Seven: Output Result

Display the refined summary directly in the conversation.

---

## Auxiliary Commands

### View Historical Reports
When user says "查看历史周报", "上周周报", "之前的周报":
1. Call `kv(action=get, key="tech_weekly_outputs")` to retrieve history
2. Return the requested week's refined output

### Clear History
When user says "清空历史", "删除周报记录":
1. Call `notify(action=confirm, message="确认清空所有历史周报记录？此操作不可恢复。", confirm_label="确认清空", cancel_label="取消")`
2. If confirmed, call:
   - `kv(action=set, key="tech_weekly_history", value=[])`
   - `kv(action=set, key="tech_weekly_outputs", value=[])`
3. Confirm deletion complete

---

## KV Storage Structure

### tech_weekly_history
Array of historical structured summaries for trend comparison:
```json
[
  { "week": "2026-W10", "date": "2026-03-06", "summary": {...} },
  { "week": "2026-W11", "date": "2026-03-13", "summary": {...} }
]
```

### tech_weekly_outputs
Array of historical refined outputs for lookup:
```json
[
  { "week": "2026-W10", "date": "2026-03-06", "output": "研发：1)..." },
  { "week": "2026-W11", "date": "2026-03-13", "output": "研发：1)..." }
]
```

---

## Notes

- ALL data must originate from the provided weekly report — fabrication is strictly prohibited
- When uncertain about a metric or claim, omit it rather than guess
- Historical comparison should focus on actionable insights, not just listing numbers
- The refined output should be readable by a non-technical executive in under 2 minutes
- **DAU 和商业化数据不在本技能关注范围内，提取和输出时均忽略**
