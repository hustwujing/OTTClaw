#!/usr/bin/env python3
"""
Berserker Hive Query Tool (OTTClaw edition)

- List accessible Hive tables in Berserker
- Run SQL rule check before execution
- Execute read-only Hive SQL and poll results via WebSocket
- Save large outputs to local files under output/berserker/

Cookie source: output/browser-cookies/<SKILL_USER_ID>/berserker.json
  (Playwright JSON format, written by browser(action=save_cookies, cookieName="berserker"))

Env vars injected automatically by OTTClaw run_script:
  SKILL_USER_ID  — current authenticated user ID
  SKILL_DIR      — absolute path of this skill's root directory
"""

import argparse
import base64
import hashlib
import json
import re
import secrets
import socket
import ssl
import struct
import sys
import os
import time
from datetime import datetime, timedelta
from pathlib import Path
from typing import Any, Dict, List, Optional, Sequence, Set, Tuple
from urllib.parse import quote as _url_quote

try:
    import requests
except ImportError:
    print("❌ 需要安装 requests: pip install requests")
    sys.exit(1)

import urllib3
if hasattr(urllib3.exceptions, 'NotOpenSSLWarning'):
    urllib3.disable_warnings(urllib3.exceptions.NotOpenSSLWarning)


# ---------------------------------------------------------------------------
# Constants
# ---------------------------------------------------------------------------

BASE_URL = "https://berserker.bilibili.co"
DEFAULT_WORKSPACE = "b_live"
DEFAULT_ENGINE_TYPE = 12
DEFAULT_LIMIT_SIZE = 20000
DEFAULT_PAGE_SIZE = 50
DEFAULT_HISTORY_SIZE = 20
DEFAULT_TIMEOUT = 300
DEFAULT_POLL_INTERVAL = 3
DEFAULT_HISTORY_DAYS = 30
DEFAULT_SOCKET_IDLE_TIMEOUT = 15
TABLE_FILE_THRESHOLD = 20
RESULT_FILE_THRESHOLD = 20
RESULT_TEXT_THRESHOLD = 20000
DOWNLOAD_TIMEOUT = 600
RUNNING_STATE_HINTS = ("未启动", "排队", "执行中", "运行中", "启动中", "初始化", "调度中")
READONLY_ALLOWED = {"select", "with", "show", "desc", "describe", "explain"}
READONLY_PREFIX_ALLOWED = {"set", "use"}
SOCKET_RUNNING_STATUSES = {0, 3, 4, 6}
C4_LEVEL = "C4"
DOWNLOAD_FORMATS: Dict[str, Dict[str, Any]] = {
    "csv": {"export_type": 1, "original_type": None},
    "excel": {"export_type": 2, "original_type": True},
    "excel-string": {"export_type": 2, "original_type": False},
    "copy-link": {"export_type": 3, "original_type": None},
}
READONLY_DENY_PATTERN = re.compile(
    r"\b(insert|update|delete|drop|truncate|alter|create|replace|merge|load|export|grant|revoke)\b",
    re.IGNORECASE,
)

# Output directories (relative to project root, which is cmd working dir)
OUTPUT_DIR = Path("output")
BERSERKER_OUTPUT_DIR = OUTPUT_DIR / "berserker"


def get_berserker_download_dir() -> Path:
    """下载目录：{tempdir}/{user_id}/berserker/，便于 read_file 工具访问。"""
    import tempfile
    user_id = os.environ.get("SKILL_USER_ID", "default").strip() or "default"
    return Path(tempfile.gettempdir()) / user_id / "berserker"


# ---------------------------------------------------------------------------
# Cookie helpers
# ---------------------------------------------------------------------------

def get_cookie() -> str:
    """
    Read Berserker cookie from Playwright JSON file saved by OTTClaw browser tool.

    File location: output/browser-cookies/<SKILL_USER_ID>/berserker.json
    Written by: browser(action=save_cookies, cookieName="berserker")

    SKILL_USER_ID is injected automatically by OTTClaw's run_script handler.
    """
    def _safe_val(v: str) -> str:
        """URL-encode cookie values that contain non-ASCII characters (latin-1 incompatible)."""
        try:
            v.encode("latin-1")
            return v
        except (UnicodeEncodeError, UnicodeDecodeError):
            return _url_quote(v, safe="")

    user_id = os.environ.get("SKILL_USER_ID", "").strip()
    if not user_id:
        raise RuntimeError(
            "❌ SKILL_USER_ID 未注入，无法定位 Cookie 文件。\n"
            "这是 OTTClaw 的 run_script 内部错误，请联系管理员。"
        )

    cookie_file = OUTPUT_DIR / "browser-cookies" / user_id / "berserker.json"
    if not cookie_file.exists():
        raise RuntimeError("❌ NEED_LOGIN")

    try:
        cookies = json.loads(cookie_file.read_text(encoding="utf-8"))
    except (json.JSONDecodeError, OSError) as exc:
        raise RuntimeError(f"❌ Cookie 文件读取失败: {exc}\n文件路径: {cookie_file}") from exc

    if not isinstance(cookies, list) or not cookies:
        raise RuntimeError("❌ NEED_LOGIN")

    cookie_str = "; ".join(
        f"{c['name']}={_safe_val(str(c['value']))}"
        for c in cookies
        if c.get("name") and c.get("value") is not None
        and (c.get("domain", "") == ".bilibili.co" or c.get("domain", "").endswith(".bilibili.co"))
    )
    if not cookie_str:
        raise RuntimeError("❌ NEED_LOGIN")
    return cookie_str


def extract_username_from_cookie(cookie: str) -> str:
    """Extract username from cookie string if present."""
    match = re.search(r"(?:^|;\s*)username=([^;]+)", cookie)
    return match.group(1) if match else ""


# ---------------------------------------------------------------------------
# Utility helpers
# ---------------------------------------------------------------------------

def pick_first(mapping: Dict[str, Any], keys: Sequence[str], default: Any = "") -> Any:
    for key in keys:
        value = mapping.get(key)
        if value not in (None, ""):
            return value
    return default


def truncate_text(text: Any, limit: int = 80) -> str:
    value = "" if text is None else str(text).replace("\n", " ").replace("\r", " ")
    return value if len(value) <= limit else value[: limit - 3] + "..."


def format_timestamp_ms(value: Any) -> str:
    if value in (None, ""):
        return ""
    try:
        raw = int(value)
    except (TypeError, ValueError):
        return str(value)
    if raw <= 0:
        return str(value)
    if raw < 10_000_000_000:
        raw *= 1000
    return datetime.fromtimestamp(raw / 1000).strftime("%Y-%m-%d %H:%M:%S")


def format_instance_status(value: Any) -> str:
    mapping = {0: "未启动", 1: "成功", 5: "停止"}
    try:
        status = int(value)
    except (TypeError, ValueError):
        return str(value)
    return mapping.get(status, str(status))


def parse_time_input(value: str, *, end_of_day: bool = False) -> int:
    candidates = [
        ("%Y-%m-%d %H:%M:%S", False),
        ("%Y-%m-%d %H:%M", False),
        ("%Y-%m-%d", True),
        ("%Y%m%d%H%M%S", False),
        ("%Y%m%d%H%M", False),
        ("%Y%m%d", True),
    ]
    for pattern, date_only in candidates:
        try:
            parsed = datetime.strptime(value, pattern)
            if date_only and end_of_day:
                parsed = parsed.replace(hour=23, minute=59, second=59)
            return int(parsed.timestamp() * 1000)
        except ValueError:
            continue
    raise ValueError("时间格式不合法，支持 YYYY-MM-DD / YYYY-MM-DD HH:MM[:SS] / YYYYMMDD / YYYYMMDDHHMM[SS]")


def resolve_history_time_range(
    *, days: Optional[int], start_time: Optional[str], end_time: Optional[str]
) -> Tuple[Optional[int], Optional[int]]:
    start_ms = parse_time_input(start_time, end_of_day=False) if start_time else None
    end_ms = parse_time_input(end_time, end_of_day=True) if end_time else None
    if days is None and start_ms is None and end_ms is None:
        days = DEFAULT_HISTORY_DAYS
    if days is not None and start_ms is None and end_ms is None:
        end = datetime.now()
        start = end - timedelta(days=days)
        return int(start.timestamp() * 1000), int(end.timestamp() * 1000)
    if start_ms and end_ms and start_ms > end_ms:
        raise ValueError("history 的开始时间不能晚于结束时间")
    return start_ms, end_ms


def extract_filename_from_disposition(content_disposition: str) -> str:
    if not content_disposition:
        return ""
    filename_star = re.search(r"filename\*=UTF-8''([^;]+)", content_disposition, re.IGNORECASE)
    if filename_star:
        return filename_star.group(1).strip().strip('"').replace("%20", " ")
    filename = re.search(r'filename="?([^";]+)"?', content_disposition, re.IGNORECASE)
    return filename.group(1).strip() if filename else ""


def guess_download_extension(content_type: str, export_type: int) -> str:
    lowered = (content_type or "").lower()
    if "csv" in lowered or export_type == 1:
        return ".csv"
    if "sheet" in lowered or "excel" in lowered or export_type == 2:
        return ".xlsx"
    if "json" in lowered:
        return ".json"
    return ".bin"


def strip_sql_comments(sql: str) -> str:
    sql = re.sub(r"/\*.*?\*/", " ", sql, flags=re.DOTALL)
    sql = re.sub(r"--[^\n]*", " ", sql)
    return sql


def normalize_level(value: Any) -> str:
    return str(value or "").strip().upper()


def is_c4_level(value: Any) -> bool:
    return normalize_level(value) == C4_LEVEL


def validate_readonly_sql(sql: str) -> None:
    cleaned = strip_sql_comments(sql).strip()
    if not cleaned:
        raise ValueError("SQL 不能为空")
    denied = READONLY_DENY_PATTERN.search(cleaned)
    if denied:
        raise ValueError(f"检测到非只读关键字 `{denied.group(1).upper()}`，已拒绝执行")
    statements = [part.strip() for part in cleaned.split(";") if part.strip()]
    if not statements:
        raise ValueError("SQL 不能为空")
    tokens: List[str] = []
    for statement in statements:
        matched = re.match(r"([a-zA-Z]+)", statement)
        tokens.append(matched.group(1).lower() if matched else "")
    final_token = tokens[-1]
    if final_token not in READONLY_ALLOWED:
        raise ValueError("只支持只读 Hive SQL，结尾语句请使用 SELECT / WITH / SHOW / DESC / DESCRIBE / EXPLAIN")
    for token in tokens[:-1]:
        if token not in READONLY_PREFIX_ALLOWED:
            raise ValueError("仅支持前置 SET / USE 语句，写表、建表或其它副作用语句已禁止")


def extract_statement_tokens(sql: str) -> List[str]:
    cleaned = strip_sql_comments(sql).strip()
    statements = [part.strip() for part in cleaned.split(";") if part.strip()]
    tokens: List[str] = []
    for statement in statements:
        matched = re.match(r"([a-zA-Z]+)", statement)
        tokens.append(matched.group(1).lower() if matched else "")
    return tokens


def collect_sql_guidance(sql: str) -> List[str]:
    cleaned = strip_sql_comments(sql)
    lowered = cleaned.lower()
    tokens = extract_statement_tokens(sql)
    suggestions: List[str] = []
    if any(token in READONLY_PREFIX_ALLOWED for token in tokens[:-1]):
        suggestions.append("检测到前置 SET/USE 语句，当前脚本已按 Hive 脚本模式处理。")
    if tokens and tokens[-1] in {"select", "with"}:
        suggestions.append("先确认目标表是全量快照还是增量流水；全量表通常查最新 T-1 分区，增量表再按时间范围筛选。")
        if not re.search(r"\blimit\s+\d+", lowered):
            suggestions.append("即席查询建议显式加 LIMIT，避免一次拉取过大结果集。")
        partition_patterns = [
            r"\blog_date\b\s*(=|>=|<=|>|<|between|in)",
            r"\bdt\b\s*(=|>=|<=|>|<|between|in)",
            r"\bds\b\s*(=|>=|<=|>|<|between|in)",
            r"\bdate\b\s*(=|>=|<=|>|<|between|in)",
        ]
        if not any(re.search(pattern, lowered) for pattern in partition_patterns):
            suggestions.append("建议优先命中分区字段（如 log_date/dt/ds/date），避免全表扫描。")
        join_count = len(re.findall(r"\bjoin\b", lowered))
        if join_count > 4:
            suggestions.append("连续 JOIN 超过 4 个，建议拆成临时表或 CTE 分段处理。")
    return suggestions


def parse_running_state(message: str) -> str:
    match = re.search(r"\[(.*?)\]", message or "")
    return match.group(1) if match else (message or "")


def load_sql(sql: Optional[str], sql_file: Optional[str]) -> str:
    if sql_file:
        return Path(sql_file).read_text(encoding="utf-8").strip()
    if sql:
        return sql.strip()
    raise ValueError("请提供 SQL 字符串或 --sql-file")


def parse_table_reference(value: str) -> Tuple[str, str]:
    if "." not in value:
        raise ValueError("表名请传 `database.table` 格式，例如 `b_ods.ods_ds7560_common_traffic_support_detail_a`")
    database, table = value.split(".", 1)
    database, table = database.strip(), table.strip()
    if not database or not table:
        raise ValueError("表名请传 `database.table` 格式，例如 `b_ods.ods_ds7560_common_traffic_support_detail_a`")
    return database, table


def build_markdown_table(headers: Sequence[str], rows: Sequence[Sequence[Any]]) -> str:
    if not headers:
        return ""

    def norm(value: Any) -> str:
        text = "" if value is None else str(value)
        return text.replace("\n", " ").replace("\r", " ").replace("|", "\\|")

    lines = [
        "| " + " | ".join(norm(item) for item in headers) + " |",
        "| " + " | ".join(["---"] * len(headers)) + " |",
    ]
    for row in rows:
        lines.append("| " + " | ".join(norm(item) for item in row) + " |")
    return "\n".join(lines)


def get_column_names(columns: Sequence[Any], rows: Sequence[Any]) -> List[str]:
    names: List[str] = []
    for column in columns or []:
        if isinstance(column, str):
            names.append(column)
        elif isinstance(column, dict):
            names.append(str(
                column.get("name") or column.get("columnName") or column.get("label")
                or column.get("title") or column.get("field") or f"col{len(names) + 1}"
            ))
        else:
            names.append(f"col{len(names) + 1}")
    if names:
        return names
    if rows and isinstance(rows[0], dict):
        return [str(key) for key in rows[0].keys()]
    if rows and isinstance(rows[0], list):
        return [f"col{i + 1}" for i in range(len(rows[0]))]
    return []


def normalize_rows(columns: Sequence[Any], rows: Sequence[Any]) -> Tuple[List[str], List[List[Any]]]:
    names = get_column_names(columns, rows)
    normalized: List[List[Any]] = []
    for row in rows or []:
        if isinstance(row, dict):
            normalized.append([row.get(name) for name in names])
        elif isinstance(row, list):
            normalized.append(row)
        else:
            normalized.append([row])
    return names, normalized


def save_result_file(prefix: str, content: str, extra_meta: Optional[Dict[str, Any]] = None) -> Path:
    """Save result to output/berserker/ directory."""
    BERSERKER_OUTPUT_DIR.mkdir(parents=True, exist_ok=True)
    path = BERSERKER_OUTPUT_DIR / f"{prefix}_{int(time.time())}.md"
    path.write_text(content, encoding="utf-8")
    return path


def print_or_save_output(
    prefix: str,
    content: str,
    *,
    extra_meta: Optional[Dict[str, Any]] = None,
    force_file: bool = False,
    allow_stdout: bool = False,
    threshold_rows: int = 0,
    row_count: int = 0,
) -> None:
    should_save = force_file or (threshold_rows > 0 and row_count > threshold_rows) or len(content) > RESULT_TEXT_THRESHOLD
    if should_save and not allow_stdout:
        path = save_result_file(prefix, content, extra_meta=extra_meta)
        print(f"📁 结果已保存: {path}")
        return
    print(content)


def parse_partition_spec(value: Any) -> Dict[str, str]:
    text = str(value or "").strip()
    parsed: Dict[str, str] = {}
    for item in [part for part in text.split("/") if part]:
        if "=" not in item:
            continue
        key, raw_value = item.split("=", 1)
        parsed[key.strip()] = raw_value.strip()
    return parsed


def build_partition_where_clause(partition_map: Dict[str, str], partition_columns: Sequence[str]) -> str:
    clauses: List[str] = []
    for column in partition_columns:
        value = partition_map.get(column)
        if value is None:
            continue
        escaped = value.replace("'", "\\'")
        clauses.append(f"{column} = '{escaped}'")
    return " and ".join(clauses)


def select_download_link(links: Sequence[Dict[str, Any]], export_type: int) -> str:
    title_map = {1: ("csv",), 2: ("excel",), 3: ("复制链接", "link")}
    expected = title_map.get(export_type, ())
    for item in links or []:
        title = str(item.get("title") or "").lower()
        if any(flag.lower() in title for flag in expected):
            return str(item.get("url") or "")
    return str(links[0].get("url", "")) if links else ""


def build_download_request_url(*, download_id: int, export_type: int, original_type: Optional[bool], order_id: str) -> str:
    params = [f"id={download_id}", f"exportType={export_type}"]
    if original_type is not None:
        params.append(f"originalType={str(original_type).lower()}")
    if order_id:
        params.append(f"orderId={order_id}")
    return f"{BASE_URL}/api/adhoc/sql/run/download?{'&'.join(params)}"


def build_download_output_path(*, save_to: Optional[str], filename: str, export_type: int, content_type: str) -> Path:
    if save_to:
        requested = Path(save_to).expanduser()
        if requested.exists() and requested.is_dir():
            target_name = filename or f"berserker_download_{int(time.time())}{guess_download_extension(content_type, export_type)}"
            return requested / target_name
        if requested.suffix:
            return requested
        requested.mkdir(parents=True, exist_ok=True)
        target_name = filename or f"berserker_download_{int(time.time())}{guess_download_extension(content_type, export_type)}"
        return requested / target_name
    download_dir = get_berserker_download_dir()
    download_dir.mkdir(parents=True, exist_ok=True)
    target_name = filename or f"berserker_download_{int(time.time())}{guess_download_extension(content_type, export_type)}"
    return download_dir / target_name


def save_download_response(response: "requests.Response", *, export_type: int, save_to: Optional[str] = None) -> Tuple[Path, str, str, int]:
    content_type = response.headers.get("content-type", "")
    disposition = response.headers.get("content-disposition", "")
    filename = extract_filename_from_disposition(disposition)
    output_path = build_download_output_path(save_to=save_to, filename=filename, export_type=export_type, content_type=content_type)
    file_size = 0
    with output_path.open("wb") as file_obj:
        for chunk in response.iter_content(chunk_size=65536):
            if not chunk:
                continue
            file_obj.write(chunk)
            file_size += len(chunk)
    return output_path, output_path.name, content_type, file_size


# ---------------------------------------------------------------------------
# Format helpers
# ---------------------------------------------------------------------------

def format_table_listing(tables: Sequence[Dict[str, Any]], *, keyword: str, workspace: str) -> str:
    lines = ["# Berserker Hive 表权限", ""]
    lines.append(f"- Workspace: `{workspace}`")
    lines.append(f"- Keyword: `{keyword or '(无)'}`")
    lines.append(f"- Count: {len(tables)}")
    lines.append("")
    headers = ["fullTabName", "workspaceName", "policyDuration"]
    preview_rows = [
        [item.get("fullTabName", ""), item.get("workspaceName", ""), item.get("policyDuration", "")]
        for item in tables[:20]
    ]
    if preview_rows:
        lines.append(build_markdown_table(headers, preview_rows))
    else:
        lines.append("（无匹配结果）")
    if len(tables) > 20:
        lines.append("")
        lines.append(f"> 仅预览前 20 条，共 {len(tables)} 条。")
    return "\n".join(lines).strip() + "\n"


def format_check_output(result: Dict[str, Any], *, sql: str) -> str:
    data = result.get("data") or {}
    lines = ["# Berserker SQL 检查", ""]
    lines.append(f"- isPass: `{data.get('isPass')}`")
    lines.append(f"- isDisplay: `{data.get('isDisplay')}`")
    lines.append(f"- traceId: `{data.get('traceId')}`")
    lines.append(f"- sqlLength: {len(sql)}")
    suggestions = collect_sql_guidance(sql)
    if suggestions:
        lines.append("")
        lines.append("## 查询建议")
        for item in suggestions:
            lines.append(f"- {item}")
    details = data.get("checkItemDetails") or []
    if details:
        lines.append("")
        lines.append("## checkItemDetails")
        lines.append("```json")
        lines.append(json.dumps(details, ensure_ascii=False, indent=2))
        lines.append("```")
    return "\n".join(lines).strip() + "\n"


def format_query_output(query_id: int, result: Dict[str, Any]) -> Tuple[str, int, int]:
    data = result.get("data") or {}
    columns = data.get("columns") or []
    rows = data.get("result") or []
    column_names, normalized_rows = normalize_rows(columns, rows)
    lines = ["# Berserker Query Result", ""]
    lines.append(f"- queryId: `{query_id}`")
    lines.append(f"- currentSize: {data.get('currentSize', 0)}")
    lines.append(f"- totalSize: {data.get('totalSize', 0)}")
    lines.append(f"- rows: {len(normalized_rows)}")
    lines.append("")
    if not normalized_rows:
        lines.append("（查询成功，但结果为空）")
        return "\n".join(lines).strip() + "\n", 0, 0
    preview_rows = normalized_rows[:20]
    lines.append(build_markdown_table(column_names, preview_rows))
    if len(normalized_rows) > 20:
        lines.append("")
        lines.append(f"> 仅预览前 20 行，共 {len(normalized_rows)} 行。")
    output = "\n".join(lines).strip() + "\n"
    return output, len(normalized_rows), len(output)


def format_query_summary(query_id: int, result: Dict[str, Any], *, note: str = "") -> str:
    data = result.get("data") or {}
    lines = ["# Berserker Query Result", ""]
    lines.append(f"- queryId: `{query_id}`")
    lines.append(f"- currentSize: {data.get('currentSize', 0)}")
    lines.append(f"- totalSize: {data.get('totalSize', 0)}")
    if note:
        lines.append(f"- note: {note}")
    return "\n".join(lines).strip() + "\n"


def extract_query_rows(result: Dict[str, Any]) -> Tuple[List[str], List[List[Any]]]:
    data = result.get("data") or {}
    return normalize_rows(data.get("columns") or [], data.get("result") or [])


def format_submit_output(query_id: int, trace_id: str) -> str:
    return (
        "# Berserker Query Submitted\n\n"
        f"- queryId: `{query_id}`\n"
        f"- traceId: `{trace_id}`\n"
        f"- next: `result {query_id} --wait`\n"
    )


def format_history_output(
    result: Dict[str, Any], *, keyword: str, username: str,
    start_time: Optional[int], end_time: Optional[int],
) -> Tuple[str, int]:
    data = result.get("data") or {}
    records = data.get("records") or data.get("list") or []
    total = data.get("total") or data.get("totalSize") or len(records)
    lines = ["# Berserker Query History", ""]
    lines.append(f"- keyword: `{keyword or '(无)'}`")
    lines.append(f"- username: `{username or '(当前用户)'}`")
    if start_time:
        lines.append(f"- startTime: `{format_timestamp_ms(start_time)}`")
    if end_time:
        lines.append(f"- endTime: `{format_timestamp_ms(end_time)}`")
    lines.append(f"- count: {len(records)}")
    lines.append(f"- total: {total}")
    lines.append("")
    preview_rows: List[List[Any]] = []
    for item in records[:20]:
        preview_rows.append([
            pick_first(item, ("id", "queryId")),
            format_instance_status(pick_first(item, ("statusDesc", "statusText", "instanceStatus", "status"))),
            pick_first(item, ("resultCount", "totalSize", "currentSize"), 0),
            format_timestamp_ms(pick_first(item, ("mtime", "updateTime", "endTime", "ctime", "createTime", "startTime"))),
            truncate_text(pick_first(item, ("sqlCommand", "originalSqlCommand", "sql")), 96),
        ])
    if preview_rows:
        lines.append(build_markdown_table(["id", "status", "resultCount", "time", "sql"], preview_rows))
    else:
        lines.append("（无匹配记录）")
    if len(records) > 20:
        lines.append("")
        lines.append("> 仅预览前 20 条历史记录。")
    return "\n".join(lines).strip() + "\n", len(records)


def format_schema_output(result: Dict[str, Any], *, table_ref: str) -> Tuple[str, int]:
    data = result.get("data") or {}
    columns = data.get("columns") or []
    partition_columns = data.get("partitionColumns") or []
    lines = ["# Berserker Table Schema", ""]
    lines.append(f"- table: `{table_ref}`")
    lines.append(f"- desc: {data.get('desc', '').strip() or '(无)'}")
    lines.append(f"- workspace: `{data.get('workspace') or ''}`")
    lines.append(f"- privilegeLevel: `{data.get('privilegeLevel') or ''}`")
    lines.append(f"- owner: `{data.get('ownerNickname') or ''}`")
    lines.append(f"- columnCount: {len(columns)}")
    lines.append(f"- partitionColumnCount: {len(partition_columns)}")
    lines.append("")
    preview_rows = [
        [item.get("column", ""), item.get("type", ""), item.get("columnDesc", ""), item.get("privilegeLevel", "")]
        for item in columns[:30]
    ]
    if preview_rows:
        lines.append(build_markdown_table(["column", "type", "desc", "privilege"], preview_rows))
    else:
        lines.append("（无字段信息）")
    if len(columns) > 30:
        lines.append("")
        lines.append(f"> 仅预览前 30 个字段，共 {len(columns)} 个。")
    if partition_columns:
        lines.append("")
        lines.append("## Partition Columns")
        for item in partition_columns:
            if isinstance(item, dict):
                name = item.get("column") or item.get("name") or ""
                col_type = item.get("type") or ""
                desc = item.get("columnDesc") or item.get("desc") or ""
                lines.append(f"- `{name}` `{col_type}` {desc}".rstrip())
            else:
                lines.append(f"- `{item}`")
        lines.append("")
        lines.append("## Query Hint")
        lines.append("- 先确认这张表是全量快照还是增量流水。")
        lines.append("- 全量表通常优先查最新分区，日级场景一般用业务时间 T-1，可直接写 `${yyyyMMdd}`。")
        lines.append("- 增量表再按业务时间范围或事件时间范围筛选。")
    return "\n".join(lines).strip() + "\n", len(columns)


def format_stop_output(query_id: int, result: Dict[str, Any]) -> str:
    return (
        "# Berserker Query Stop\n\n"
        f"- queryId: `{query_id}`\n"
        f"- code: {result.get('code')}\n"
        f"- msg: {result.get('msg', '')}\n"
    )


def format_latest_partition_output(
    *, table_ref: str, query_id: int, partition_columns: Sequence[str],
    partition_map: Dict[str, str], where_clause: str,
) -> str:
    lines = ["# Berserker Latest Partition", ""]
    lines.append(f"- table: `{table_ref}`")
    lines.append(f"- queryId: `{query_id}`")
    lines.append(f"- partitionColumns: `{', '.join(partition_columns)}`")
    lines.append("")
    lines.append("| partition | value |")
    lines.append("| --- | --- |")
    for column in partition_columns:
        lines.append(f"| {column} | {partition_map.get(column, '')} |")
    if where_clause:
        lines.append("")
        lines.append("## Suggested Filter")
        lines.append("```sql")
        lines.append(f"where {where_clause}")
        lines.append("```")
    return "\n".join(lines).strip() + "\n"


def format_download_create_output(
    query_id: int, result: Dict[str, Any], *, selected_format: Optional[str] = None,
    selected_link: str = "", saved_path: Optional[Path] = None,
    filename: str = "", content_type: str = "", file_size: int = 0,
) -> str:
    data = result.get("data") or {}
    risk = (data.get("europaRiskCheckResp") or {})
    risk_event = risk.get("riskEvent") or {}
    detail = risk_event.get("detail") or {}
    links = detail.get("links") or []
    lines = ["# Berserker Download", ""]
    lines.append(f"- queryId: `{query_id}`")
    lines.append(f"- downloadId: `{data.get('id', '')}`")
    lines.append(f"- auditStatus: `{data.get('auditStatus', '')}`")
    lines.append(f"- hasRisk: `{risk.get('hasRisk')}`")
    if selected_format:
        lines.append(f"- format: `{selected_format}`")
    if saved_path:
        lines.append(f"- savedTo: `{saved_path}`")
    if filename:
        lines.append(f"- filename: `{filename}`")
    if content_type:
        lines.append(f"- contentType: `{content_type}`")
    if file_size:
        lines.append(f"- fileSize: {file_size}")
    if selected_link:
        lines.append(f"- selectedLink: `{selected_link}`")
    columns = detail.get("columns") or []
    if columns:
        lines.append("")
        lines.append(f"## 列信息（{len(columns)}）")
        for item in columns[:20]:
            lines.append(f"- `{item}`")
    if links:
        lines.append("")
        lines.append("## 可用链接")
        for item in links:
            title = item.get("title") or "link"
            url = item.get("url") or ""
            lines.append(f"- {title}: `{url}`")
    return "\n".join(lines).strip() + "\n"


def resolve_download_format(*, row_count: int, explicit_format: Optional[str], no_download: bool) -> Optional[str]:
    if no_download:
        return None
    if explicit_format:
        return explicit_format
    if row_count > RESULT_FILE_THRESHOLD:
        return "excel-string"
    return None


# ---------------------------------------------------------------------------
# BerserkerClient
# ---------------------------------------------------------------------------

class BerserkerClient:
    """Thin Berserker API client."""

    def __init__(self, workspace: str, user_name: Optional[str] = None):
        self.workspace = workspace
        self.cookie = get_cookie()
        # Try to extract username from cookie; fall back to SKILL_USER_ID
        self.user_name = (
            user_name
            or extract_username_from_cookie(self.cookie)
            or os.environ.get("SKILL_USER_ID", "")
        )
        if not self.user_name:
            raise RuntimeError("无法确定 username，请通过 --user-name 显式传入")

        self.session = requests.Session()
        self.session.headers.update({
            "accept": "application/json, text/plain, */*",
            "accept-language": "zh-CN,zh;q=0.9",
            "bsk-workspace-name": self.workspace,
            "cache-control": "no-cache",
            "content-type": "application/json;charset=UTF-8",
            "origin": BASE_URL,
            "pragma": "no-cache",
            "referer": f"{BASE_URL}/",
            "user-agent": (
                "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) "
                "AppleWebKit/537.36 (KHTML, like Gecko) "
                "Chrome/145.0.0.0 Safari/537.36"
            ),
            "x-requested-with": "XMLHttpRequest",
            "cookie": self.cookie,
        })

    def _request(
        self, method: str, endpoint: str, *,
        params: Optional[Dict[str, Any]] = None,
        payload: Optional[Dict[str, Any]] = None,
        timeout: int = 180,
    ) -> Dict[str, Any]:
        url = f"{BASE_URL}{endpoint}"
        response = self.session.request(method, url, params=params, json=payload, timeout=timeout)
        if response.status_code in (401, 403):
            raise RuntimeError("❌ NEED_LOGIN")
        response.raise_for_status()
        try:
            return response.json()
        except (ValueError, requests.exceptions.JSONDecodeError) as exc:
            error_context = [
                "Berserker returned non-JSON response",
                f"  HTTP Status: {response.status_code}",
                f"  Content-Type: {response.headers.get('content-type', 'unknown')}",
                f"  URL: {method} {endpoint}",
            ]
            preview = response.text[:1000]
            if len(response.text) > 1000:
                preview += f"\n  ... (truncated, total: {len(response.text)} chars)"
            error_context.append(f"  Response Preview:\n    {preview}")
            if "login" in preview.lower() or response.status_code == 302:
                error_context.append(
                    "\n  ⚠️ HINT: 疑似 Cookie 已过期或登录态失效。\n"
                    "  请重新执行 browser(action=save_cookies, cookieName=\"berserker\") 刷新 Cookie。"
                )
            raise RuntimeError("\n".join(error_context)) from exc

    def list_tables_page(self, *, keyword: str = "", current: int = 1, size: int = DEFAULT_PAGE_SIZE,
                         business_tag_first: str = "业务线", business_tag_second: str = "", workspace: str = "") -> Dict[str, Any]:
        payload = {
            "dsType": "Hive", "keyword": keyword, "userName": self.user_name,
            "businessTag": {"tagFirst": business_tag_first, "tagSecond": business_tag_second},
            "tag": [], "workspace": workspace, "current": current, "size": size, "totalSize": 0,
        }
        return self._request("POST", "/keeper/myData/privilegeTab", payload=payload, timeout=120)

    def list_all_tables(self, *, keyword: str = "", business_tag_first: str = "业务线",
                        business_tag_second: str = "", workspace: str = "", size: int = DEFAULT_PAGE_SIZE) -> List[Dict[str, Any]]:
        all_tables: List[Dict[str, Any]] = []
        current = 1
        while True:
            result = self.list_tables_page(keyword="", current=current, size=size,
                                           business_tag_first=business_tag_first,
                                           business_tag_second=business_tag_second, workspace=workspace)
            if result.get("code") != 200:
                raise RuntimeError(f"权限表查询失败: {result.get('msg', '未知错误')}")
            data = result.get("data") or {}
            page = data.get("page") or {}
            tables = data.get("tables") or []
            all_tables.extend(tables)
            total_size = int(page.get("totalSize") or 0)
            current_size = int(page.get("currentSize") or len(tables))
            per_page_size = int(page.get("perPageSize") or size)
            if not tables:
                break
            fetched = len(all_tables)
            if total_size and fetched >= total_size:
                break
            if current_size < per_page_size:
                break
            current += 1
        if keyword:
            kw = keyword.lower()
            all_tables = [
                t for t in all_tables
                if kw in " ".join(str(t.get(f, "")) for f in ("fullTabName", "dbName", "tabName", "tabDesc", "workspaceName")).lower()
            ]
        return all_tables

    def get_table_schema(self, *, database: str, table: str) -> Dict[str, Any]:
        return self._request("GET", f"/keeper/hive/data/{database}/{table}", timeout=180)

    def check_sql(self, *, sql: str, limit_size: int = DEFAULT_LIMIT_SIZE, engine_type: int = DEFAULT_ENGINE_TYPE) -> Dict[str, Any]:
        payload = {
            "checkType": "adhoc:run", "engineType": engine_type, "limitSize": limit_size,
            "requestParam": "", "sqlCommand": sql, "customParams": [],
        }
        return self._request("POST", "/api/adhoc/sql/run/rule/check", payload=payload, timeout=180)

    def execute_sql(self, *, sql: str, trace_id: str, limit_size: int = DEFAULT_LIMIT_SIZE,
                    engine_type: int = DEFAULT_ENGINE_TYPE) -> Dict[str, Any]:
        payload = {
            "checkType": "adhoc:run", "filename": "", "sqlCommand": sql, "originalSqlCommand": sql,
            "engineType": engine_type, "limitSize": limit_size, "traceId": trace_id,
            "extraParam": None, "requestParam": "", "customParams": [], "templateId": 0,
        }
        return self._request("POST", "/api/adhoc/sql/run/execute", payload=payload, timeout=300)

    def fetch_result(self, query_id: int) -> Dict[str, Any]:
        return self._request("GET", "/api/adhoc/sql/run/result", params={"queryId": query_id}, timeout=180)

    def build_socket_headers(self) -> Dict[str, str]:
        return {"Origin": BASE_URL, "Cookie": self.cookie,
                "User-Agent": self.session.headers.get("user-agent", "Mozilla/5.0")}

    def build_socket_url(self, query_id: int) -> str:
        from urllib.parse import urlparse
        parsed = urlparse(BASE_URL)
        scheme = "wss" if parsed.scheme == "https" else "ws"
        return f"{scheme}://{parsed.netloc}/api/adhoc/socket/{query_id}"

    def query_history(self, *, keyword: str = "", username: str = "", start_time: Optional[int] = None,
                      end_time: Optional[int] = None, current: int = 1, size: int = DEFAULT_HISTORY_SIZE) -> Dict[str, Any]:
        payload: Dict[str, Any] = {
            "keyword": keyword, "username": username or self.user_name,
            "startTime": start_time, "endTime": end_time, "current": current, "size": size,
        }
        return self._request("POST", "/api/adhoc/sql/run/history/query", payload=payload, timeout=180)

    def stop_query(self, query_id: int) -> Dict[str, Any]:
        return self._request("POST", "/api/adhoc/sql/run/stop", params={"queryId": query_id}, timeout=120)

    def create_download(self, query_id: int, *, skip_approval: bool = False) -> Dict[str, Any]:
        return self._request("POST", "/api/adhoc/sql/run/download/create",
                             params={"queryId": query_id, "skipApproval": str(skip_approval).lower()}, timeout=180)

    def download_file(self, *, download_id: int, export_type: int, original_type: Optional[bool] = None,
                      order_id: str = "", timeout: int = DOWNLOAD_TIMEOUT) -> "requests.Response":
        params: Dict[str, Any] = {"id": download_id, "exportType": export_type}
        if order_id:
            params["orderId"] = order_id
        if original_type is not None:
            params["originalType"] = str(original_type).lower()
        response = self.session.request("GET", f"{BASE_URL}/api/adhoc/sql/run/download",
                                        params=params, timeout=timeout, stream=True)
        response.raise_for_status()
        content_type = response.headers.get("content-type", "")
        if "application/json" in content_type.lower():
            try:
                result = response.json()
            except ValueError as exc:
                raise RuntimeError("下载接口返回了 JSON，但无法解析内容") from exc
            if result.get("code") == 200:
                raise RuntimeError("下载接口返回了 JSON 响应，未拿到文件流，请改用 --create-only 先检查下载信息")
            raise RuntimeError(f"文件下载失败: code={result.get('code')}, msg={result.get('msg')}")
        return response


# ---------------------------------------------------------------------------
# WebSocket client
# ---------------------------------------------------------------------------

class AdhocQuerySocket:
    """Minimal websocket client for Berserker adhoc query channel."""

    def __init__(self, url: str, headers: Dict[str, str]):
        self.url = url
        self.headers = headers
        self.sock: Optional[socket.socket] = None
        self.default_timeout = DEFAULT_SOCKET_IDLE_TIMEOUT

    def connect(self, timeout: int = DEFAULT_SOCKET_IDLE_TIMEOUT) -> None:
        from urllib.parse import urlparse
        parsed = urlparse(self.url)
        host = parsed.hostname or ""
        if not host:
            raise RuntimeError("adhoc socket URL 缺少 host")
        port = parsed.port or (443 if parsed.scheme == "wss" else 80)
        path = parsed.path or "/"
        if parsed.query:
            path += f"?{parsed.query}"
        raw_sock = socket.create_connection((host, port), timeout=timeout)
        if parsed.scheme == "wss":
            context = ssl.create_default_context()
            self.sock = context.wrap_socket(raw_sock, server_hostname=host)
        else:
            self.sock = raw_sock
        self.sock.settimeout(timeout)
        self.default_timeout = timeout
        key = base64.b64encode(secrets.token_bytes(16)).decode("ascii")
        expected_accept = base64.b64encode(
            hashlib.sha1((key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11").encode("utf-8")).digest()
        ).decode("ascii")
        host_header = host if port in (80, 443) else f"{host}:{port}"
        request_lines = [
            f"GET {path} HTTP/1.1", f"Host: {host_header}",
            "Upgrade: websocket", "Connection: Upgrade",
            f"Sec-WebSocket-Key: {key}", "Sec-WebSocket-Version: 13",
        ]
        for key_name, value in self.headers.items():
            request_lines.append(f"{key_name}: {value}")
        self.sock.sendall(("\r\n".join(request_lines) + "\r\n\r\n").encode("utf-8"))
        response = self._recv_http_headers()
        header_lines = response.split("\r\n")
        status_line = header_lines[0] if header_lines else ""
        if " 101 " not in status_line:
            raise RuntimeError(f"adhoc socket 握手失败: {status_line or '无状态行'}")
        header_map: Dict[str, str] = {}
        for line in header_lines[1:]:
            if ":" not in line:
                continue
            name, value = line.split(":", 1)
            header_map[name.strip().lower()] = value.strip()
        if header_map.get("sec-websocket-accept") != expected_accept:
            raise RuntimeError("adhoc socket 握手失败: Sec-WebSocket-Accept 不匹配")

    def close(self) -> None:
        if self.sock is None:
            return
        try:
            self._send_frame(0x8, b"")
        except Exception:
            pass
        try:
            self.sock.close()
        finally:
            self.sock = None

    def recv_text(self, timeout: Optional[int] = None) -> str:
        if self.sock is None:
            raise RuntimeError("adhoc socket 尚未连接")
        self.sock.settimeout(timeout or self.default_timeout)
        message_parts: List[bytes] = []
        collecting_text = False
        while True:
            header = self._recv_exact(2)
            first_byte, second_byte = header[0], header[1]
            fin = bool(first_byte & 0x80)
            opcode = first_byte & 0x0F
            masked = bool(second_byte & 0x80)
            payload_len = second_byte & 0x7F
            if payload_len == 126:
                payload_len = struct.unpack("!H", self._recv_exact(2))[0]
            elif payload_len == 127:
                payload_len = struct.unpack("!Q", self._recv_exact(8))[0]
            mask_key = self._recv_exact(4) if masked else b""
            payload = self._recv_exact(payload_len) if payload_len else b""
            if masked:
                payload = bytes(byte ^ mask_key[index % 4] for index, byte in enumerate(payload))
            if opcode == 0x8:
                raise ConnectionError("adhoc socket 已关闭")
            if opcode == 0x9:
                self._send_frame(0xA, payload)
                continue
            if opcode == 0xA:
                continue
            if opcode == 0x1:
                message_parts = [payload]
                collecting_text = True
                if fin:
                    return b"".join(message_parts).decode("utf-8", errors="ignore")
                continue
            if opcode == 0x0 and collecting_text:
                message_parts.append(payload)
                if fin:
                    return b"".join(message_parts).decode("utf-8", errors="ignore")

    def _recv_http_headers(self) -> str:
        chunks = bytearray()
        while b"\r\n\r\n" not in chunks:
            piece = self._recv_exact(1)
            chunks.extend(piece)
            if len(chunks) > 65536:
                raise RuntimeError("adhoc socket 握手响应过长")
        return chunks.decode("utf-8", errors="ignore").split("\r\n\r\n", 1)[0]

    def _recv_exact(self, length: int) -> bytes:
        if self.sock is None:
            raise RuntimeError("adhoc socket 尚未连接")
        chunks = bytearray()
        while len(chunks) < length:
            piece = self.sock.recv(length - len(chunks))
            if not piece:
                raise ConnectionError("adhoc socket 连接已断开")
            chunks.extend(piece)
        return bytes(chunks)

    def _send_frame(self, opcode: int, payload: bytes) -> None:
        if self.sock is None:
            return
        first_byte = 0x80 | (opcode & 0x0F)
        payload_len = len(payload)
        mask_key = secrets.token_bytes(4)
        masked_payload = bytes(byte ^ mask_key[index % 4] for index, byte in enumerate(payload))
        if payload_len < 126:
            header = bytes([first_byte, 0x80 | payload_len])
        elif payload_len < 65536:
            header = bytes([first_byte, 0x80 | 126]) + struct.pack("!H", payload_len)
        else:
            header = bytes([first_byte, 0x80 | 127]) + struct.pack("!Q", payload_len)
        self.sock.sendall(header + mask_key + masked_payload)


# ---------------------------------------------------------------------------
# Query flow helpers
# ---------------------------------------------------------------------------

def summarize_socket_message(message: Dict[str, Any]) -> str:
    status = message.get("status")
    logs = message.get("logs")
    if isinstance(logs, list) and logs:
        return f"status={status}, log={truncate_text(logs[-1], 160)}"
    if isinstance(logs, str) and logs:
        return f"status={status}, log={truncate_text(logs, 160)}"
    return f"status={status}"


def activate_query_via_socket(client: BerserkerClient, *, query_id: int, timeout: int,
                               idle_timeout: int = DEFAULT_SOCKET_IDLE_TIMEOUT) -> None:
    socket_client = AdhocQuerySocket(client.build_socket_url(query_id), client.build_socket_headers())
    deadline = time.time() + timeout
    last_status: Optional[int] = None
    last_message: Optional[Dict[str, Any]] = None
    try:
        socket_client.connect(timeout=min(idle_timeout, max(3, timeout)))
        while time.time() < deadline:
            remaining = max(1, int(deadline - time.time()))
            raw = socket_client.recv_text(timeout=min(idle_timeout, remaining))
            try:
                message = json.loads(raw)
            except json.JSONDecodeError:
                continue
            last_message = message
            status = message.get("status")
            if status is None:
                continue
            try:
                status = int(status)
            except (TypeError, ValueError):
                continue
            last_status = status
            if status == 1:
                return
            if status in {2, 5}:
                raise RuntimeError(f"adhoc socket 返回终态失败: {summarize_socket_message(message)}")
            if status in SOCKET_RUNNING_STATUSES:
                continue
        raise TimeoutError(
            f"adhoc socket 等待超时: queryId={query_id}, "
            f"last={summarize_socket_message(last_message or {'status': last_status})}"
        )
    finally:
        socket_client.close()


def wait_for_result(client: BerserkerClient, *, query_id: int, timeout: int = DEFAULT_TIMEOUT,
                    poll_interval: int = DEFAULT_POLL_INTERVAL, activate: bool = False) -> Dict[str, Any]:
    deadline = time.time() + timeout
    last_message = ""
    if activate:
        activate_query_via_socket(client, query_id=query_id, timeout=timeout)
    while True:
        result = client.fetch_result(query_id)
        code = result.get("code")
        message = result.get("msg", "")
        if code == 200:
            return result
        state = parse_running_state(message)
        if any(hint in state for hint in RUNNING_STATE_HINTS) or any(hint in message for hint in RUNNING_STATE_HINTS):
            last_message = message
            if time.time() >= deadline:
                raise TimeoutError(f"等待查询结果超时: queryId={query_id}, last={last_message}")
            time.sleep(poll_interval)
            continue
        raise RuntimeError(f"查询结果获取失败: code={code}, msg={message}")


def submit_checked_query(client: BerserkerClient, *, sql: str, limit_size: int,
                         engine_type: int) -> Tuple[int, str]:
    check_result = client.check_sql(sql=sql, limit_size=limit_size, engine_type=engine_type)
    if check_result.get("code") != 200:
        raise RuntimeError(f"SQL 检查失败: {check_result.get('msg', '未知错误')}")
    check_data = check_result.get("data") or {}
    if not check_data.get("isPass"):
        print(format_check_output(check_result, sql=sql))
        raise RuntimeError("SQL 规则检查未通过，已停止执行")
    trace_id = check_data.get("traceId")
    if not trace_id:
        raise RuntimeError("SQL 检查成功，但未返回 traceId")
    execute_result = client.execute_sql(sql=sql, trace_id=trace_id, limit_size=limit_size, engine_type=engine_type)
    if execute_result.get("code") != 200:
        raise RuntimeError(f"SQL 提交失败: {execute_result.get('msg', '未知错误')}")
    query_id = (execute_result.get("data") or {}).get("queryId")
    if not query_id:
        raise RuntimeError("SQL 提交成功，但未返回 queryId")
    return int(query_id), str(trace_id)


def run_download_flow(client: BerserkerClient, *, query_id: int, format_name: str,
                      skip_approval: bool = False, create_only: bool = False, save_to: Optional[str] = None) -> str:
    create_result = client.create_download(query_id, skip_approval=skip_approval)
    if create_result.get("code") != 200:
        raise RuntimeError(f"创建下载任务失败: {create_result.get('msg', '未知错误')}")
    data = create_result.get("data") or {}
    risk = data.get("europaRiskCheckResp") or {}
    detail = (risk.get("riskEvent") or {}).get("detail") or {}
    links = detail.get("links") or []
    download_id = data.get("id")
    selected = DOWNLOAD_FORMATS[format_name]
    selected_link = select_download_link(links, int(selected["export_type"]))
    if download_id and format_name != "copy-link":
        selected_link = build_download_request_url(
            download_id=int(download_id), export_type=int(selected["export_type"]),
            original_type=selected["original_type"], order_id="",
        )
    if create_only or risk.get("hasRisk") or data.get("auditStatus") not in (0, "0", None):
        return format_download_create_output(query_id, create_result, selected_format=format_name, selected_link=selected_link)
    if format_name == "copy-link":
        return format_download_create_output(query_id, create_result, selected_format=format_name, selected_link=selected_link)
    if not download_id:
        raise RuntimeError("下载创建成功，但未返回 downloadId")
    response = client.download_file(download_id=int(download_id), export_type=int(selected["export_type"]),
                                    original_type=selected["original_type"])
    output_path, filename, content_type, file_size = save_download_response(
        response, export_type=int(selected["export_type"]), save_to=save_to)
    return format_download_create_output(query_id, create_result, selected_format=format_name,
                                         selected_link=selected_link, saved_path=output_path,
                                         filename=filename, content_type=content_type, file_size=file_size)


# ---------------------------------------------------------------------------
# Command handlers
# ---------------------------------------------------------------------------

def handle_tables(args: argparse.Namespace) -> None:
    client = BerserkerClient(workspace=args.workspace, user_name=args.user_name)
    if args.page is not None:
        result = client.list_tables_page(keyword=args.keyword, current=args.page, size=args.size,
                                         business_tag_first=args.business_tag_first,
                                         business_tag_second=args.business_tag_second,
                                         workspace=args.filter_workspace)
        if result.get("code") != 200:
            raise RuntimeError(f"权限表查询失败: {result.get('msg', '未知错误')}")
        tables = (result.get("data") or {}).get("tables") or []
    else:
        tables = client.list_all_tables(keyword=args.keyword, business_tag_first=args.business_tag_first,
                                        business_tag_second=args.business_tag_second,
                                        workspace=args.filter_workspace, size=args.size)
    content = format_table_listing(tables, keyword=args.keyword, workspace=args.workspace)
    print_or_save_output("berserker_tables", content, force_file=len(tables) > TABLE_FILE_THRESHOLD,
                         allow_stdout=args.no_file, threshold_rows=TABLE_FILE_THRESHOLD, row_count=len(tables))


def handle_check(args: argparse.Namespace) -> None:
    sql = load_sql(args.sql, args.sql_file)
    validate_readonly_sql(sql)
    client = BerserkerClient(workspace=args.workspace, user_name=args.user_name)
    result = client.check_sql(sql=sql, limit_size=args.limit_size, engine_type=args.engine_type)
    if result.get("code") != 200:
        raise RuntimeError(f"SQL 检查失败: {result.get('msg', '未知错误')}")
    print(format_check_output(result, sql=sql))


def handle_schema(args: argparse.Namespace) -> None:
    database, table = parse_table_reference(args.table)
    client = BerserkerClient(workspace=args.workspace, user_name=args.user_name)
    result = client.get_table_schema(database=database, table=table)
    if result.get("code") != 200:
        raise RuntimeError(f"表结构查询失败: {result.get('msg', '未知错误')}")
    content, row_count = format_schema_output(result, table_ref=f"{database}.{table}")
    print_or_save_output("berserker_schema", content, force_file=row_count > RESULT_FILE_THRESHOLD,
                         allow_stdout=args.no_file, threshold_rows=RESULT_FILE_THRESHOLD, row_count=row_count)


def handle_latest_partition(args: argparse.Namespace) -> None:
    database, table = parse_table_reference(args.table)
    table_ref = f"{database}.{table}"
    client = BerserkerClient(workspace=args.workspace, user_name=args.user_name)
    schema = client.get_table_schema(database=database, table=table)
    if schema.get("code") != 200:
        raise RuntimeError(f"表结构查询失败: {schema.get('msg', '未知错误')}")
    schema_data = schema.get("data") or {}
    partition_columns = schema_data.get("partitionColumns") or []
    partition_names: List[str] = []
    for item in partition_columns:
        name = str(item.get("column") or item.get("name") or "").strip() if isinstance(item, dict) else str(item).strip()
        if name:
            partition_names.append(name)
    if not partition_names:
        raise RuntimeError(f"`{table_ref}` 没有分区列，不能查询「最新分区」")
    if args.partition_column:
        requested = [item.strip() for item in args.partition_column.split(",") if item.strip()]
        invalid = [item for item in requested if item not in partition_names]
        if invalid:
            raise RuntimeError(f"指定的分区列不存在: {', '.join(invalid)}；可用分区列: {', '.join(partition_names)}")
        partition_names = requested
    sql = f"show partitions {table_ref}"
    query_id, _ = submit_checked_query(client, sql=sql, limit_size=args.limit_size, engine_type=args.engine_type)
    result = wait_for_result(client, query_id=query_id, timeout=args.timeout,
                             poll_interval=args.poll_interval, activate=True)
    _, rows = extract_query_rows(result)
    if not rows:
        raise RuntimeError(f"`{table_ref}` 的 SHOW PARTITIONS 结果为空")
    specs: List[Dict[str, str]] = [parse_partition_spec(row[0]) for row in rows if row and parse_partition_spec(row[0])]
    if not specs:
        raise RuntimeError(f"`{table_ref}` 的分区结果解析失败")
    latest = max(specs, key=lambda item: tuple(item.get(column, "") for column in partition_names))
    where_clause = build_partition_where_clause(latest, partition_names)
    print(format_latest_partition_output(table_ref=table_ref, query_id=query_id,
                                         partition_columns=partition_names, partition_map=latest,
                                         where_clause=where_clause))


def handle_query(args: argparse.Namespace) -> None:
    sql = load_sql(args.sql, args.sql_file)
    validate_readonly_sql(sql)
    client = BerserkerClient(workspace=args.workspace, user_name=args.user_name)
    query_id, trace_id = submit_checked_query(client, sql=sql, limit_size=args.limit_size, engine_type=args.engine_type)
    if args.submit_only:
        print(format_submit_output(query_id, trace_id))
        return
    result = wait_for_result(client, query_id=query_id, timeout=args.timeout,
                             poll_interval=args.poll_interval, activate=True)
    content, row_count, _ = format_query_output(query_id, result)
    download_format = resolve_download_format(row_count=row_count, explicit_format=args.download_format, no_download=args.no_download)
    if download_format:
        print(format_query_summary(query_id, result, note="结果集不短，已直接进入下载链路。"))
        print()
        print(run_download_flow(client, query_id=query_id, format_name=download_format,
                                skip_approval=args.download_skip_approval, create_only=args.download_create_only,
                                save_to=args.download_save_to))
        return
    print_or_save_output("berserker_query", content, row_count=row_count,
                         threshold_rows=RESULT_FILE_THRESHOLD, allow_stdout=args.no_file)


def handle_result(args: argparse.Namespace) -> None:
    client = BerserkerClient(workspace=args.workspace, user_name=args.user_name)
    if args.wait:
        result = wait_for_result(client, query_id=args.query_id, timeout=args.timeout,
                                 poll_interval=args.poll_interval, activate=True)
    else:
        result = client.fetch_result(args.query_id)
        if result.get("code") != 200:
            raise RuntimeError(f"结果尚不可用: {result.get('msg', '未知错误')}，可加 --wait 重试")
    content, row_count, _ = format_query_output(args.query_id, result)
    download_format = resolve_download_format(row_count=row_count, explicit_format=args.download_format, no_download=args.no_download)
    if download_format:
        print(format_query_summary(args.query_id, result, note="结果集不短，已直接进入下载链路。"))
        print()
        print(run_download_flow(client, query_id=args.query_id, format_name=download_format,
                                skip_approval=args.download_skip_approval, create_only=args.download_create_only,
                                save_to=args.download_save_to))
        return
    print_or_save_output("berserker_result", content, row_count=row_count,
                         threshold_rows=RESULT_FILE_THRESHOLD, allow_stdout=args.no_file)


def handle_history(args: argparse.Namespace) -> None:
    start_ms, end_ms = resolve_history_time_range(days=args.days, start_time=args.start_time, end_time=args.end_time)
    client = BerserkerClient(workspace=args.workspace, user_name=args.user_name)
    result = client.query_history(keyword=args.keyword, username=args.history_user or client.user_name,
                                  start_time=start_ms, end_time=end_ms, current=args.page, size=args.size)
    if result.get("code") != 200:
        raise RuntimeError(f"历史查询失败: {result.get('msg', '未知错误')}")
    content, row_count = format_history_output(result, keyword=args.keyword,
                                               username=args.history_user or client.user_name,
                                               start_time=start_ms, end_time=end_ms)
    print_or_save_output("berserker_history", content, force_file=row_count > RESULT_FILE_THRESHOLD,
                         allow_stdout=args.no_file, threshold_rows=RESULT_FILE_THRESHOLD, row_count=row_count)


def handle_stop(args: argparse.Namespace) -> None:
    client = BerserkerClient(workspace=args.workspace, user_name=args.user_name)
    result = client.stop_query(args.query_id)
    if result.get("code") != 200:
        raise RuntimeError(f"停止查询失败: {result.get('msg', '未知错误')}")
    print(format_stop_output(args.query_id, result))


def handle_download(args: argparse.Namespace) -> None:
    client = BerserkerClient(workspace=args.workspace, user_name=args.user_name)
    print(run_download_flow(client, query_id=args.query_id, format_name=args.format,
                            skip_approval=args.skip_approval, create_only=args.create_only, save_to=args.save_to))


# ---------------------------------------------------------------------------
# CLI
# ---------------------------------------------------------------------------

def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Berserker Hive 权限查询与 SQL 执行工具",
                                     formatter_class=argparse.RawDescriptionHelpFormatter)
    subparsers = parser.add_subparsers(dest="command", required=True)

    tables = subparsers.add_parser("tables", help="列出有权限的 Hive 表")
    tables.add_argument("--keyword", default="", help="表名/描述关键词过滤")
    tables.add_argument("--workspace", default=DEFAULT_WORKSPACE)
    tables.add_argument("--filter-workspace", default="")
    tables.add_argument("--user-name")
    tables.add_argument("--size", type=int, default=DEFAULT_PAGE_SIZE)
    tables.add_argument("--page", type=int)
    tables.add_argument("--business-tag-first", default="业务线")
    tables.add_argument("--business-tag-second", default="")
    tables.add_argument("--no-file", action="store_true")
    tables.set_defaults(func=handle_tables)

    schema = subparsers.add_parser("schema", help="查询表结构元数据")
    schema.add_argument("table", help="database.table")
    schema.add_argument("--workspace", default=DEFAULT_WORKSPACE)
    schema.add_argument("--user-name")
    schema.add_argument("--no-file", action="store_true")
    schema.set_defaults(func=handle_schema)

    lp = subparsers.add_parser("latest-partition", help="查询表的最新分区值")
    lp.add_argument("table", help="database.table")
    lp.add_argument("--workspace", default=DEFAULT_WORKSPACE)
    lp.add_argument("--user-name")
    lp.add_argument("--partition-column")
    lp.add_argument("--limit-size", type=int, default=DEFAULT_LIMIT_SIZE)
    lp.add_argument("--engine-type", type=int, default=DEFAULT_ENGINE_TYPE)
    lp.add_argument("--timeout", type=int, default=DEFAULT_TIMEOUT)
    lp.add_argument("--poll-interval", type=int, default=DEFAULT_POLL_INTERVAL)
    lp.set_defaults(func=handle_latest_partition)

    check = subparsers.add_parser("check", help="执行 SQL 规则检查")
    check.add_argument("sql", nargs="?")
    check.add_argument("--sql-file")
    check.add_argument("--workspace", default=DEFAULT_WORKSPACE)
    check.add_argument("--user-name")
    check.add_argument("--limit-size", type=int, default=DEFAULT_LIMIT_SIZE)
    check.add_argument("--engine-type", type=int, default=DEFAULT_ENGINE_TYPE)
    check.set_defaults(func=handle_check)

    query = subparsers.add_parser("query", help="检查并执行 Hive SQL")
    query.add_argument("sql", nargs="?")
    query.add_argument("--sql-file")
    query.add_argument("--workspace", default=DEFAULT_WORKSPACE)
    query.add_argument("--user-name")
    query.add_argument("--limit-size", type=int, default=DEFAULT_LIMIT_SIZE)
    query.add_argument("--engine-type", type=int, default=DEFAULT_ENGINE_TYPE)
    query.add_argument("--timeout", type=int, default=DEFAULT_TIMEOUT)
    query.add_argument("--poll-interval", type=int, default=DEFAULT_POLL_INTERVAL)
    query.add_argument("--submit-only", action="store_true")
    query.add_argument("--no-file", action="store_true")
    query.add_argument("--download-format", choices=sorted(DOWNLOAD_FORMATS.keys()))
    query.add_argument("--no-download", action="store_true")
    query.add_argument("--download-save-to")
    query.add_argument("--download-skip-approval", action="store_true")
    query.add_argument("--download-create-only", action="store_true")
    query.set_defaults(func=handle_query)

    result = subparsers.add_parser("result", help="根据 queryId 拉取结果")
    result.add_argument("query_id", type=int)
    result.add_argument("--workspace", default=DEFAULT_WORKSPACE)
    result.add_argument("--user-name")
    result.add_argument("--wait", action="store_true")
    result.add_argument("--timeout", type=int, default=DEFAULT_TIMEOUT)
    result.add_argument("--poll-interval", type=int, default=DEFAULT_POLL_INTERVAL)
    result.add_argument("--no-file", action="store_true")
    result.add_argument("--download-format", choices=sorted(DOWNLOAD_FORMATS.keys()))
    result.add_argument("--no-download", action="store_true")
    result.add_argument("--download-save-to")
    result.add_argument("--download-skip-approval", action="store_true")
    result.add_argument("--download-create-only", action="store_true")
    result.set_defaults(func=handle_result)

    history = subparsers.add_parser("history", help="查询 SQL 历史记录")
    history.add_argument("--keyword", default="")
    history.add_argument("--workspace", default=DEFAULT_WORKSPACE)
    history.add_argument("--user-name")
    history.add_argument("--history-user")
    history.add_argument("--page", type=int, default=1)
    history.add_argument("--size", type=int, default=DEFAULT_HISTORY_SIZE)
    history.add_argument("--days", type=int)
    history.add_argument("--start-time")
    history.add_argument("--end-time")
    history.add_argument("--no-file", action="store_true")
    history.set_defaults(func=handle_history)

    stop = subparsers.add_parser("stop", help="停止运行中的 SQL 查询")
    stop.add_argument("query_id", type=int)
    stop.add_argument("--workspace", default=DEFAULT_WORKSPACE)
    stop.add_argument("--user-name")
    stop.set_defaults(func=handle_stop)

    download = subparsers.add_parser("download", help="创建并下载查询结果文件")
    download.add_argument("query_id", type=int)
    download.add_argument("--workspace", default=DEFAULT_WORKSPACE)
    download.add_argument("--user-name")
    download.add_argument("--format", choices=sorted(DOWNLOAD_FORMATS.keys()), default="excel-string")
    download.add_argument("--skip-approval", action="store_true")
    download.add_argument("--create-only", action="store_true")
    download.add_argument("--save-to")
    download.set_defaults(func=handle_download)

    return parser


def main() -> None:
    parser = build_parser()
    args = parser.parse_args()
    try:
        args.func(args)
    except KeyboardInterrupt:
        print("\n⚠️ 已中断", file=sys.stderr)
        sys.exit(130)
    except Exception as exc:
        # 业务错误输出到 stdout 并正常退出，让 LLM 能将错误信息展示给用户
        print(f"❌ {exc}")
        sys.exit(0)


if __name__ == "__main__":
    main()
