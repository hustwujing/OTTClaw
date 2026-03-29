# SKILL.md 格式规范与创建指南

---

## 1. SKILL.md 文件结构

技能文件由**两个分隔符**分隔。每个分隔符是**恰好 30 个等号**——不多不少：

```
==============================
```

完整结构（第一段可以为空）：

```
（可选的前置内容）
==============================
（HEAD 元数据段）
==============================
（CONTENT 详细内容段）
```

---

## 2. HEAD 元数据（两个分隔符之间）

`key: value` 格式，每行一个字段：

| 字段 | 是否必填 | 说明 |
|-------|----------|-------------|
| `skill_id` | ✅ | 唯一 ID——只允许小写字母、数字和下划线 |
| `name` | ✅ | 技能名称 |
| `display_name` | 推荐 | 在对话界面中显示的 AI 发言者名称（例如"数据分析师"）；未设置时回退到 `name` |
| `enable` | ✅ | `true` 表示加载该技能；`false` 或缺失则跳过（不区分大小写） |
| `description` | ✅ | 用于提示词摘要的一句话描述 |
| `trigger` | ✅ | 触发时机，例如"当用户想要……" |

---

## 3. CONTENT 推荐结构

```markdown
## 技能目标
一段话：这个技能解决什么问题，适用于哪些场景。

---

## 执行步骤

> Execute all steps strictly in order, one step at a time. Do not skip or merge steps. Wait for each step's result before proceeding to the next.

<!-- 如果没有脚本文件，在第一步之前插入以下警示块 -->
> ⚠️ **Execution Mode:** This skill has no script files. All steps are executed directly
> by the LLM using built-in tools (read_file, kv, etc.). **Never call `skill(action=run_script)`.**

**第一步：收集信息**
- 向用户询问哪些输入内容

**第二步：处理**
- 调用 skill(action=read_file, skill_id=xxx, sub_path="assets/yyy") 读取参考资料
- 调用 skill(action=run_script, skill_id=xxx, script_name=yyy, args=[...]) 执行脚本（仅当有脚本时）
- 使用 kv(action=set/get, ...) 在步骤之间传递数据

**第三步：输出结果**
- 以何种格式将结果返回给用户

---

## 输出格式
（纯文本 / Markdown 表格 / JSON 等）

---

## 注意事项（可选）
（边界情况、错误处理）
```

---

## 4. 脚本（script/）使用指南

### 何时需要脚本

| 场景 | 是否需要脚本 |
|----------|---------------|
| 纯文本生成、摘要、改写 | 否 |
| 数据格式转换（JSON 转表格、CSV 解析） | 是 |
| 外部 API 调用（天气、汇率、搜索） | 是 |
| 复杂计算、统计分析 | 是 |
| 文件读写、批量处理 | 是 |

### Python 脚本骨架模板

创建脚本时，使用以下骨架结构，并在每个 `TODO` 注释处填入业务逻辑：

```python
#!/usr/bin/env python3
"""
{技能名称} — {脚本功能描述}

用法：
    python3 {script_name} '<JSON 参数>'

输入（第一个命令行参数，JSON 字符串）：
    {描述输入结构，例如 {"key": "value"}}

输出（stdout，JSON 字符串）：
    {描述输出结构，例如 {"result": "...", "status": "ok"}}
"""

import json
import os
import sys
import tempfile


# ── 临时目录（需要步骤间中转文件时使用，否则删除）──────────────────────
# tempfile.gettempdir() 跨平台兼容（macOS /tmp、Linux /tmp、Windows %TEMP%）
# realpath 解析软链接（macOS /tmp -> /private/tmp）
# SKILL_SESSION_ID 保证多用户并发隔离
_TMP_ROOT = os.path.realpath(tempfile.gettempdir())

def _get_work_dir(skill_id):
    # type: (str) -> str
    session_id = os.environ.get("SKILL_SESSION_ID", "default")
    path = os.path.join(_TMP_ROOT, "{}_{}".format(skill_id, session_id))
    os.makedirs(path, exist_ok=True)
    return path

# 用法：work_dir = _get_work_dir("my_skill_id")
# 持久文件：写入 output/{SKILL_USER_ID}/ 并将路径 print 到 stdout，
# 由 LLM 调用 output_file(action=download) 生成下载链接。
# ─────────────────────────────────────────────────────────────────────


def main() -> None:
    # ── 参数解析 ───────────────────────────────────
    if len(sys.argv) < 2:
        print(json.dumps({"error": "Missing argument. Usage: python3 script_name '<JSON>'"}),
              file=sys.stderr)
        sys.exit(1)

    try:
        data = json.loads(sys.argv[1])
    except json.JSONDecodeError as e:
        print(json.dumps({"error": f"Failed to parse argument JSON: {e}"}), file=sys.stderr)
        sys.exit(1)

    # ── 业务逻辑 ─────────────────────────────────────
    # TODO: 从 data 中读取所需字段
    # TODO: 实现核心处理逻辑
    result = {}  # TODO: 填充 result

    # ── 输出结果 ──────────────────────────────────────
    print(json.dumps(result, ensure_ascii=False))


if __name__ == "__main__":
    main()
```

### 脚本调用示例（在 SKILL.md CONTENT 中的写法）

```markdown
**第二步：格式转换**

调用 `skill(action=run_script)` 对上一步收集的原始数据进行格式化：
- skill_id: my_skill
- script_name: format_output.py
- args: ['{"raw_data": "..."}']

脚本返回 JSON；解析后呈现给用户。如果脚本报错，向用户说明错误并以原始数据作为备用输出。
```

---

## 5. 参考资源（assets/）使用指南

### 何时需要资源文件

| 场景 | 是否需要资源 |
|----------|--------------|
| 风格指南、写作规范 | 是 |
| 固定模板（邮件模板、报告框架） | 是 |
| 词汇表、分类标准 | 是 |
| 示例文档、参考案例 | 是 |
| 动态生成的内容 | 否——不适合放入 assets |

### 资源文件调用示例（在 SKILL.md CONTENT 中的写法）

```markdown
**第一步：读取风格指南**

调用 `skill(action=read_file, skill_id=my_skill, sub_path="assets/style_guide.md")` 获取本技能的写作规范：

读取后，在所有后续生成步骤中始终以该指南作为约束。
```

---

## 6. 完整示例（包含脚本和资源的技能）

```
==============================
skill_id: csv_analyzer
name: CSV 数据分析器
display_name: 数据分析师
enable: true
description: 读取用户粘贴的 CSV 数据，进行统计分析并生成可读报告
trigger: 当用户想要分析 CSV 数据、查看数据统计信息或生成数据摘要时
==============================

## 技能目标

接收用户提供的 CSV 格式数据，进行基本统计分析（行数、列信息、数值列的均值/最大值/最小值），
并通过格式化脚本生成可读的 Markdown 报告。

---

## 执行步骤

**第一步：读取报告模板**

调用 `skill(action=read_file, skill_id=csv_analyzer, sub_path="assets/report_template.md")` 获取标准报告格式。
生成报告时严格遵循该模板。

**第二步：收集数据**

请用户粘贴 CSV 内容（纯文本，包含表头）。

**第三步：运行分析**

调用 `skill(action=run_script, skill_id=csv_analyzer, script_name=analyze.py, args=['<csv 内容>'])`。
脚本返回包含统计信息的 JSON 对象：行数、列名，以及每个数值列的均值/最大值/最小值。

如果脚本失败，向用户说明错误并询问是否愿意手动描述数据。

**第四步：生成报告**

根据分析结果和报告模板，生成完整的 Markdown 分析报告并呈现给用户。

---

## 输出格式

Markdown 报告，包含：数据概览表 + 每个数值列的统计表 + 简短的文字解读。

---

## 注意事项

- 如果 CSV 超过 10,000 行，提醒用户处理可能需要一些时间。
- 如果 CSV 解析失败，提示用户检查格式（例如多余的引号、意外的换行符）。
```

---

## 7. 关键约束提醒

1. 每个分隔符必须是**恰好 30 个 `=` 字符**——多一个或少一个都会导致解析失败（工具将返回错误）。
2. `skill_id` 只允许小写字母、数字和下划线。**不允许**：大写字母、中文字符、空格或连字符。
3. 在 HEAD 中，`skill_id` 和 `name` 为必填项——留空将被工具拒绝。强烈推荐填写 `display_name`；若省略，对话界面将回退显示 `name`。`enable: true` 为必填项——如果缺失或设为 `false`，该技能将被跳过且不可用。
4. CONTENT 段不能为空——必须包含实质性内容。
5. 如果 SKILL.md 引用了 `skill(action=run_script)`，对应的脚本文件**必须通过 `skill(action=write, skill_id=..., content=..., sub_path="script/<文件名>")` 创建**。
6. 如果 SKILL.md 引用了 `skill(action=read_file, ..., sub_path="assets/...")`，对应的资源文件**必须通过 `skill(action=write, skill_id=..., content=..., sub_path="assets/<文件名>")` 创建**。
7. **无脚本警示（必填，仅无脚本技能）**：如果该技能没有任何脚本文件，必须在执行步骤第一步之前插入以下警示块，防止 LLM 错误调用 `run_script`：
   ```
   > ⚠️ **Execution Mode:** This skill has no script files. All steps are executed directly by the LLM using built-in tools (read_file, kv, etc.). **Never call `skill(action=run_script)`.**
   ```
8. **顺序执行声明（必填，所有技能）**：所有技能的执行步骤列表顶部（第一步之前）都必须包含以下声明，防止 LLM 跳步或合并步骤：
   ```
   > Execute all steps strictly in order, one step at a time. Do not skip or merge steps. Wait for each step's result before proceeding to the next.
   ```
