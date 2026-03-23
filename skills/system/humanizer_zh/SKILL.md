==============================
skill_id: humanizer_zh
name: Humanizer ZH
display_name: AI 去痕润色
enable: true
description: Detects and removes AI-generated writing patterns in Chinese text, making it sound natural and human-written. Covers 22 common AI patterns.
trigger: When the user wants to remove AI traces from text, humanize writing, or says "去掉 AI 味", "润色", "让文字更自然", "人性化处理".
==============================

# Humanizer-zh

Identify and rewrite AI patterns in text. Keep meaning, match tone, inject personality.

## Core Rules

1. **Cut filler** — remove openers and emphasis crutches
2. **Break formulas** — avoid binary contrasts, dramatic reveals, rhetorical setups
3. **Vary rhythm** — mix sentence lengths; two items beat three; vary paragraph endings
4. **Trust readers** — state facts directly, skip softening and hand-holding
5. **Kill quotables** — if it sounds like a pull-quote, rewrite it

## Add Soul

Clean but voiceless writing is just as detectable as slop. Good writing has a real person behind it:
- Have opinions, not just neutral reporting
- Vary pace: short punchy sentences, then long ones
- Acknowledge complexity and mixed feelings
- Use "I" when appropriate
- Allow some messiness — perfect structure feels algorithmic
- Be specific about feelings, not generic

## Pattern Checklist

### Content patterns

| # | Pattern | Key signals → Fix |
|---|---------|-------------------|
| 1 | Inflated significance | 标志着、关键时刻、不可磨灭 → state plain facts |
| 2 | Celebrity/media hype | 独立报道、活跃社交媒体 → cite one specific source |
| 3 | -ing shallow analysis | 突出/强调/彰显……、确保…… → cut the trailing clause |
| 4 | Promotional language | 充满活力、令人叹为观止、坐落于 → neutral description |
| 5 | Vague attribution | 专家认为、行业报告显示 → name the source or cut |
| 6 | Boilerplate "challenges & outlook" | 尽管存在挑战…继续蓬勃发展 → give specific data |

### Language patterns

| # | Pattern | Key signals → Fix |
|---|---------|-------------------|
| 7 | AI vocabulary cluster | 此外、至关重要、格局、织锦 → plain synonyms |
| 8 | Copula avoidance | 作为/代表/标志着 X → just use "是" |
| 9 | Negation escalation | 不仅仅是…而是… → direct statement |
| 10 | Rule of three | 三项并列 → use two or four items |
| 11 | Synonym cycling | 主人公/主角/中心人物 → repeat the word |
| 12 | False range | 从 X 到 Y（not a real spectrum） → list items plainly |

### Style patterns

| # | Pattern | Fix |
|---|---------|-----|
| 13 | Em-dash overuse | replace with commas or periods |
| 14 | Bold overuse | remove mechanical bold on terms |
| 15 | Bold-heading bullet lists | merge into flowing prose |
| 16 | Emoji decorations | remove all emoji from headings/bullets |

### Tone patterns

| # | Pattern | Fix |
|---|---------|-----|
| 17 | Chat artifacts | remove "希望这对您有帮助！" etc. |
| 18 | Knowledge cutoff disclaimers | remove "截至…" hedges |
| 19 | Sycophantic tone | remove "好问题！您说得完全正确！" |
| 20 | Filler phrases | "由于下雨的事实"→"因为下雨" |
| 21 | Over-qualification | cut stacked hedges |
| 22 | Generic positive endings | replace with specific next steps |

## Example

**Before (AI):**
> 新的软件更新作为公司致力于创新的证明。此外，它提供了无缝、直观和强大的用户体验——确保用户能够高效地完成目标。这不仅仅是一次更新，而是我们思考生产力方式的革命。

**After (human):**
> 软件更新添加了批处理、键盘快捷键和离线模式。来自测试用户的早期反馈是积极的，大多数报告任务完成速度更快。

Changes: removed inflated significance (#1), "此外" (#7), rule-of-three + promotional (#10/#4), em-dash + -ing clause (#13/#3), negation escalation (#9), vague attribution (#5).

## Pre-delivery Checklist

- Three consecutive same-length sentences? Break one
- Paragraph ends with a punchy one-liner? Vary it
- Em-dash before a reveal? Cut it
- Explaining a metaphor? Trust the reader
- "此外"/"然而" connector? Consider deleting
- Three-item list? Change to two or four

## Output

1. Rewritten text
2. Brief change summary (optional)

## Quality Score (1–10 each, total /50)

| Dimension | Criteria |
|-----------|----------|
| Directness | Facts stated plainly vs. announced with fanfare |
| Rhythm | Varied vs. mechanical sentence lengths |
| Trust | Concise vs. over-explained |
| Authenticity | Sounds like a person vs. a machine |
| Leanness | No remaining cuttable fluff |

45-50: excellent · 35-44: good · <35: needs rework
