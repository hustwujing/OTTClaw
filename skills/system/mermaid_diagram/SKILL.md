---
skill_id: mermaid_diagram
name: Diagram & Chart Generator
display_name: Diagram Designer
enable: true
description: Generates diagrams and data charts as PNG images. Uses Python matplotlib for data charts (line, bar, pie, temperature, trend, etc.) and Mermaid for structural diagrams (flowchart, sequence, ER, class, state, Gantt, etc.)
trigger: When the user wants to draw a flowchart, sequence diagram, ER diagram, class diagram, state diagram, Gantt chart, line chart, bar chart, pie chart, temperature curve, trend chart, data chart, or says "draw me a diagram", "draw a chart", "generate an image", "plot the data", "visualize", etc.
---

## Step 1: Choose Engine

**CRITICAL: numeric data (temperature, revenue, counts, time series) → matplotlib (Path B). Structural diagrams → Mermaid (Path A). NEVER use `xychart-beta` or Mermaid for data.**

| Scenario | Engine |
|----------|--------|
| Line chart, area chart (temperature curve, trend) | **matplotlib (Path B)** |
| Bar chart, column chart | **matplotlib (Path B)** |
| Pie chart, doughnut chart | **matplotlib (Path B)** |
| Scatter chart, histogram, any numeric data | **matplotlib (Path B)** |
| Business process, step sequence | Mermaid `flowchart` (Path A) |
| Interaction sequence between systems/services | Mermaid `sequenceDiagram` (Path A) |
| Database table structure | Mermaid `erDiagram` (Path A) |
| Code class / interface structure | Mermaid `classDiagram` (Path A) |
| State machine, state transitions | Mermaid `stateDiagram-v2` (Path A) |
| Project scheduling | Mermaid `gantt` (Path A) |

---

## Path B: matplotlib (data charts — numbers, trends, comparisons)

No browser needed. Generate a Python script and run directly.

### Python script template

```python
import matplotlib
matplotlib.use('Agg')  # headless, no display required
import matplotlib.pyplot as plt
import os, time

# ── CJK font: explicit path, cross-platform (see TOOL.md matplotlib section) ──
import os
from matplotlib import font_manager as _fm
def _get_cn_font():
    for p in ['/System/Library/Fonts/Hiragino Sans GB.ttc','/System/Library/Fonts/PingFang.ttc',
              'C:/Windows/Fonts/msyh.ttc','C:/Windows/Fonts/simhei.ttf',
              '/usr/share/fonts/truetype/wqy/wqy-microhei.ttc',
              '/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc']:
        if os.path.exists(p): return _fm.FontProperties(fname=p)
    for f in _fm.fontManager.ttflist:
        if any(k in f.name for k in ('CJK','Heiti','YaHei','WenQuanYi')): return _fm.FontProperties(fname=f.fname)
    return None
cn_font = _get_cn_font()
plt.rcParams['axes.unicode_minus'] = False

# ── Data (fill in actual values) ──
labels = ['label1', 'label2', ...]
series1 = [v1, v2, ...]
series2 = [v1, v2, ...]

# ── Plot ──
fig, ax = plt.subplots(figsize=(10, 5), dpi=120)
ax.plot(labels, series1, 'o-', color='#FF6B6B', linewidth=2, markersize=6, label='Series 1')
ax.plot(labels, series2, 'o-', color='#4ECDC4', linewidth=2, markersize=6, label='Series 2')
ax.fill_between(labels, series1, series2, alpha=0.08, color='#FF6B6B')

ax.set_title('Chart Title', fontsize=16, fontweight='bold', pad=15,
             **({"fontproperties": cn_font} if cn_font else {}))
ax.set_xlabel('X Axis', fontsize=12, **({"fontproperties": cn_font} if cn_font else {}))
ax.set_ylabel('Y Axis', fontsize=12, **({"fontproperties": cn_font} if cn_font else {}))
ax.legend(prop=cn_font if cn_font else None, fontsize=None if cn_font else 11)
ax.grid(True, linestyle='--', alpha=0.5)
plt.tight_layout()

# ── Save to output directory ──
out_dir = 'output/charts'
os.makedirs(out_dir, exist_ok=True)
out_path = f'{out_dir}/chart_{int(time.time()*1000)}.png'
plt.savefig(out_path, bbox_inches='tight', facecolor='white')
print(os.path.abspath(out_path))
```

**Color palette**:
- `#FF6B6B` — red/warm (max temperature, revenue, etc.)
- `#4ECDC4` — teal (min temperature, cost, etc.)
- `#45B7D1` — blue
- `#96CEB4` — green
- `#F7DC6F` — yellow

### Execution steps

1. Use `exec` to run the Python script (inline via `python3 -c "..."` or write to a temp file first).
   The script prints the **absolute path** of the saved PNG to stdout.

2. Read the absolute path from `exec` result's `stdout` field.

3. Call `output_file(action=download, file_path=<abs_path>)` — system auto-delivers the image.
4. **IMPORTANT: Do NOT embed `![...]()` or any URL in your reply** — image already sent; embedding breaks display.

---

## Path A: Mermaid (structural diagrams only — NO numeric data)

Output the Mermaid code block in your reply:

````markdown
```mermaid
(Mermaid syntax here)
```
````

Then write HTML and screenshot — see Step A2.

**Mermaid HTML template**:
```html
<!DOCTYPE html>
<html><head>
<meta charset="utf-8">
<script src="https://cdn.jsdelivr.net/npm/mermaid@11/dist/mermaid.min.js"></script>
<style>
  body { margin: 0; padding: 24px; background: #fff; display: inline-block; }
  .mermaid svg { max-width: none !important; }
</style>
</head><body>
<div class="mermaid">
(paste Mermaid code here — NO ```mermaid fences, raw code only)
</div>
<script>mermaid.initialize({ startOnLoad: true, theme: 'default', useMaxWidth: false });</script>
</body></html>
```

### Step A2: Export Mermaid as Image

1. Use `browser(action=render, html="<full HTML string>", selector=".mermaid")` — one step: auto-launches browser if needed, renders HTML, waits for SVG, takes screenshot, cleans up temp file.
2. System auto-delivers the screenshot.
3. **IMPORTANT: Do NOT embed the screenshot URL or `![...]()` in your reply** — already sent.

---

## Output Format

1. **Image** — auto-delivered (never embed `![alt](url)` in reply)
2. **Code block** — Python or Mermaid
3. **2–4 sentence explanation**: chart type, key insights, how to request changes

## Notes

- Never use `xychart-beta`; never use Mermaid for numeric data.
- Use `_get_cn_font()` (defined in template) for all CJK text; never use `rcParams['font.sans-serif']`.
- Use `figsize=(10,5), dpi=120`. Pie charts: `ax.pie(...)`, omit xlabel/ylabel; bar charts: `ax.bar(...)`.
