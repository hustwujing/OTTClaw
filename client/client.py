#!/usr/bin/env python3
# Author:    维杰（邬晶）
# Email:     wujing03@bilibili.com
# Date:      2026
# Copyright: Copyright (c) 2026 维杰（邬晶）
#
"""
OTTClaw 控制台客户端

支持所有服务端输出类型：
  text        — 流式文字（实时打印）
  progress    — 进度事件（dim 色独占一行）
  interactive — 选项列表 / 确认框 / 文件上传（阻塞等待用户操作）
  end / error — 结束 / 错误

命令：
  /new              新建会话（清空对话历史）
  /upload <路径>    上传本地文件，返回服务端存储路径
  /quit             退出
"""

import json
import pathlib
import sys
import time
import uuid

import jwt
import requests
from prompt_toolkit import prompt as _pt_prompt
from prompt_toolkit.formatted_text import ANSI as _ANSI

# ── ANSI 颜色 ──────────────────────────────────────────────────────────────
RST  = "\033[0m"
BOLD = "\033[1m"
DIM  = "\033[2m"
RED  = "\033[31m"
GRN  = "\033[32m"
YLW  = "\033[33m"
BLU  = "\033[34m"
CYN  = "\033[36m"


def _ask(prompt_str: str) -> str:
    """
    用 prompt_toolkit 替代内置 input()。

    内置 input() 依赖系统 readline，对中文等多字节 UTF-8 字符按字节退格，
    导致删到字节边界后卡住。prompt_toolkit 自行实现行编辑，按字符退格，
    完整支持 CJK 及其他多字节字符。
    """
    return _pt_prompt(_ANSI(prompt_str))


# ── JWT 本地签发 ────────────────────────────────────────────────────────────

def _load_jwt_secret() -> str:
    """
    从项目根目录的 .env 文件读取 JWT_SECRET。
    若找不到，抛出 RuntimeError 提示用户检查配置。
    """
    # client/ 的上级目录即项目根
    env_path = pathlib.Path(__file__).parent.parent / ".env"
    if not env_path.exists():
        raise RuntimeError(f".env 文件不存在：{env_path}")

    for line in env_path.read_text(encoding="utf-8").splitlines():
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        key, sep, val = line.partition("=")
        if sep and key.strip() == "JWT_SECRET":
            return val.strip().strip('"').strip("'")

    raise RuntimeError(".env 中未找到 JWT_SECRET")


def _make_token(user_id: str, secret: str, ttl_hours: int = 24) -> str:
    """用 HS256 签发一个 JWT，payload 只含 sub / iat / exp（与服务端 GenerateToken 一致）。"""
    now = int(time.time())
    payload = {
        "sub": user_id,
        "iat": now,
        "exp": now + ttl_hours * 3600,
    }
    return jwt.encode(payload, secret, algorithm="HS256")


# ── 交互组件 ───────────────────────────────────────────────────────────────

def show_options(data: dict) -> str:
    """展示编号选项列表，阻塞等待用户选择，返回选中的 value。"""
    title   = data.get("title", "请选择")
    options = data.get("options", [])

    print()
    print(f"{BOLD}{title}{RST}")
    for i, opt in enumerate(options, 1):
        print(f"  {CYN}{i}.{RST} {opt['label']}")

    while True:
        try:
            raw = _ask(f"\n{YLW}请输入编号 [1-{len(options)}]: {RST}").strip()
            idx = int(raw) - 1
            if 0 <= idx < len(options):
                chosen = options[idx]
                print(f"{DIM}  ✓ 已选择：{chosen['label']}{RST}")
                return chosen["value"]
        except (ValueError, EOFError):
            pass
        print(f"{RED}  请输入有效编号{RST}")


def show_upload(data: dict) -> str:
    """文件上传交互在终端客户端不支持，直接跳过并提示用户通过 Web 界面操作。"""
    title = data.get("title", "上传图片")
    print()
    print(f"{BOLD}{title}{RST}")
    print(f"{YLW}  ⚠  终端客户端不支持文件上传，已自动跳过。{RST}")
    print(f"{DIM}     如需上传头像，请通过 Web 界面完成。{RST}")
    return "skip"


def show_confirm(data: dict) -> str:
    """展示确认框，阻塞等待 Y/N，返回对应按钮文案。"""
    message       = data.get("message", "确认执行此操作？")
    confirm_label = data.get("confirm_label", "确认")
    cancel_label  = data.get("cancel_label", "取消")

    print()
    print(f"{YLW}{BOLD}⚠  {message}{RST}")
    print(f"  {GRN}[Y]{RST} {confirm_label}   {DIM}[N] {cancel_label}{RST}")

    while True:
        try:
            raw = _ask(f"\n{YLW}Y / N: {RST}").strip().lower()
            if raw in ("y", "yes"):
                print(f"{DIM}  ✓ {confirm_label}{RST}")
                return confirm_label
            if raw in ("n", "no"):
                print(f"{DIM}  ✗ {cancel_label}{RST}")
                return cancel_label
        except EOFError:
            return cancel_label
        print(f"{RED}  请输入 Y 或 N{RST}")


# ── 当前活跃技能名字（由 speaker 事件更新）──────────────────────────────────

_current_speaker: str = "AI"


# ── SSE 流解析与渲染 ────────────────────────────────────────────────────────

def chat(base_url: str, token: str, session_id: str, message: str) -> "str | None":
    """
    向服务端发送一条消息，解析 SSE 流并实时渲染到终端。

    返回值：
      str  — 服务端发出了交互事件，用户已操作，返回值为待自动发送的回复内容
      None — 普通文字回复结束，等待用户手动输入下一条消息
    """
    url     = f"{base_url}/sse?session_id={session_id}"
    headers = {
        "Authorization": f"Bearer {token}",
        "Content-Type":  "application/json",
    }

    global _current_speaker
    text_started = False   # 是否已打印过 "AI: " 前缀
    mid_line     = False   # 当前是否处于未换行的文字行
    interactive  = None    # 本轮收到的最后一个 interactive 事件

    try:
        with requests.post(
            url,
            headers=headers,
            data=json.dumps({"message": message}),
            stream=True,
            timeout=120,
        ) as resp:

            if resp.status_code != 200:
                snippet = resp.text[:300]
                print(f"\n{RED}HTTP {resp.status_code}: {snippet}{RST}")
                return None

            for raw in resp.iter_lines():
                if not raw:
                    continue
                line = raw.decode() if isinstance(raw, bytes) else raw
                if not line.startswith("data: "):
                    continue

                try:
                    ev = json.loads(line[6:])
                except json.JSONDecodeError:
                    continue

                t = ev.get("type")

                # ── 文字 chunk ──────────────────────────────────────────
                if t == "text":
                    chunk = ev.get("content", "")
                    if not chunk:
                        continue
                    if not text_started:
                        # 首个文字块：打印 AI 前缀（使用当前活跃技能的优雅名字）
                        print(f"\n{BLU}{BOLD}{_current_speaker}:{RST} ", end="", flush=True)
                        text_started = True
                    print(chunk, end="", flush=True)
                    mid_line = True

                # ── 进度事件 ────────────────────────────────────────────
                elif t == "progress":
                    if mid_line:
                        print()   # 不要把进度消息接在文字末尾
                        mid_line = False
                    step    = ev.get("step", "")
                    detail  = ev.get("detail", "")
                    elapsed = ev.get("elapsed_ms", 0)
                    print(f"  {DIM}[{step}] {detail}  ({elapsed}ms){RST}")

                # ── 技能名字切换 ─────────────────────────────────────────
                elif t == "speaker":
                    name = ev.get("content", "").strip() or "AI"
                    if name != _current_speaker:
                        if mid_line:
                            print()
                            mid_line = False
                        print(f"\n{CYN}✦ {name} 来为您服务~~~{RST}")
                        _current_speaker = name

                # ── 交互事件：收集，等 end 后处理 ──────────────────────
                elif t == "interactive":
                    interactive = ev

                # ── 错误 ────────────────────────────────────────────────
                elif t == "error":
                    if mid_line:
                        print()
                        mid_line = False
                    print(f"\n{RED}{BOLD}[错误] {ev.get('content', '未知错误')}{RST}")
                    return None

                # ── 结束 ────────────────────────────────────────────────
                elif t == "end":
                    if mid_line:
                        print()
                        mid_line = False
                    break

    except requests.exceptions.Timeout:
        print(f"\n{RED}请求超时（120s）{RST}")
        return None
    except requests.exceptions.ConnectionError as e:
        print(f"\n{RED}连接失败: {e}{RST}")
        return None

    # ── 处理交互事件 ────────────────────────────────────────────────────────
    if interactive:
        kind = interactive.get("step")
        data = interactive.get("data") or {}
        # Go 侧 json.RawMessage 嵌入后 Python json.loads 会直接解析为 dict
        # 保险起见也处理字符串形式
        if isinstance(data, (str, bytes)):
            try:
                data = json.loads(data)
            except Exception:
                data = {}

        if kind == "options":
            return show_options(data)
        if kind == "confirm":
            return show_confirm(data)
        if kind == "upload":
            return show_upload(data)

    return None


# ── 文件上传 ───────────────────────────────────────────────────────────────

def upload_file(base_url: str, token: str, file_path: str) -> None:
    """上传本地文件到服务端，打印存储结果。"""
    p = pathlib.Path(file_path).expanduser().resolve()
    if not p.exists():
        print(f"{RED}文件不存在: {p}{RST}")
        return
    if not p.is_file():
        print(f"{RED}路径不是文件: {p}{RST}")
        return

    print(f"{DIM}正在上传 {p.name} ({p.stat().st_size:,} 字节)…{RST}")
    try:
        with p.open("rb") as f:
            resp = requests.post(
                f"{base_url}/api/upload",
                headers={"Authorization": f"Bearer {token}"},
                files={"file": (p.name, f)},
                timeout=60,
            )
    except requests.exceptions.ConnectionError as e:
        print(f"{RED}连接失败: {e}{RST}")
        return
    except requests.exceptions.Timeout:
        print(f"{RED}上传超时（60s）{RST}")
        return

    if resp.status_code != 200:
        print(f"{RED}上传失败 HTTP {resp.status_code}: {resp.text[:200]}{RST}")
        return

    r = resp.json()
    print(f"{GRN}✓ 上传成功{RST}")
    print(f"  {DIM}路径  : {r['path']}{RST}")
    print(f"  {DIM}MD5   : {r['md5']}{RST}")
    print(f"  {DIM}大小  : {r['size']:,} 字节{RST}")
    print(f"  {DIM}目录  : uploads/{r['dir']}/{RST}")


# ── 会话管理 ───────────────────────────────────────────────────────────────

def create_session(base_url: str, token: str) -> str:
    resp = requests.post(
        f"{base_url}/api/session/create",
        headers={"Authorization": f"Bearer {token}"},
        timeout=10,
    )
    resp.raise_for_status()
    return resp.json()["session_id"]


# ── 主循环 ─────────────────────────────────────────────────────────────────

def main() -> None:
    print(f"\n{CYN}{BOLD}═══ OTTClaw 控制台客户端 ═══{RST}\n")

    # ── 连接配置 ────────────────────────────────────────────────────────────
    raw_url  = _ask(f"服务器地址 [{DIM}默认 http://localhost:8080{RST}]: ").strip()
    base_url = (raw_url or "http://localhost:8080").rstrip("/")

    # ── 自动生成身份 ─────────────────────────────────────────────────────────
    user_id = f"user-{uuid.uuid4().hex[:8]}"
    try:
        secret = _load_jwt_secret()
        token  = _make_token(user_id, secret)
    except RuntimeError as e:
        print(f"{RED}无法生成 Token：{e}{RST}")
        sys.exit(1)

    print(f"{DIM}用户 ID : {user_id}{RST}")
    print(f"{DIM}Token   : {token[:32]}…（24h 有效）{RST}")

    # 建立会话
    print(f"\n{DIM}正在创建会话…{RST}")
    try:
        session_id = create_session(base_url, token)
    except Exception as e:
        print(f"{RED}创建会话失败: {e}{RST}")
        sys.exit(1)
    print(f"{DIM}会话 ID: {session_id}{RST}")

    print(f"\n{DIM}输入 /new 新建会话，/upload <路径> 上传文件，/quit 退出{RST}")
    print(f"{DIM}{'─' * 44}{RST}")

    auto_reply: "str | None" = None   # 交互组件产生的待发回复

    while True:
        try:
            if auto_reply is not None:
                # 自动发送交互回复（不再显示 "你:" 提示，安静地发出）
                user_input = auto_reply
                auto_reply = None
            else:
                user_input = _ask(f"\n{GRN}{BOLD}你:{RST} ").strip()

            if not user_input:
                continue

            if user_input == "/quit":
                print(f"\n{DIM}再见！{RST}")
                break

            if user_input == "/new":
                try:
                    session_id = create_session(base_url, token)
                    print(f"{DIM}新会话已创建: {session_id}{RST}")
                except Exception as e:
                    print(f"{RED}创建会话失败: {e}{RST}")
                continue

            if user_input.startswith("/upload"):
                parts = user_input.split(maxsplit=1)
                if len(parts) < 2 or not parts[1].strip():
                    file_path = _ask(f"{YLW}请输入文件路径: {RST}").strip()
                else:
                    file_path = parts[1].strip()
                if file_path:
                    upload_file(base_url, token, file_path)
                continue

            auto_reply = chat(base_url, token, session_id, user_input)

        except KeyboardInterrupt:
            print(f"\n\n{DIM}再见！{RST}")
            break
        except EOFError:
            break


if __name__ == "__main__":
    main()
