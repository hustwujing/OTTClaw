/**
 * Author:    Vijay
 * Email:     hustwujing@163.com
 * Date:      2026
 * Copyright: Copyright (c) 2026 Vijay
 *
 * browser-server/server.js
 * Playwright HTTP sidecar server.
 *
 * Design:
 *  - Per-user persistent BrowserContext via chromium.launchPersistentContext():
 *      cookies/localStorage/IndexedDB survive server restarts automatically.
 *      User data stored at BROWSER_USER_DATA_BASE/{safeUserId}/.
 *  - One shared Chromium browser process (launched at startup) for /pdf only.
 *  - Snapshot → ref → act pattern for LLM interaction:
 *      1. GET /snapshot  → aria tree with ref labels (e1, e2, ...)
 *      2. POST /act      → execute action by ref
 *  - Idle contexts are cleaned up after 15 minutes.
 *
 * All requests identify the user via `x-user-id` header.
 * Port defaults to 9222, overridable via PORT env var.
 */

'use strict';

const express = require('express');
const { chromium } = require('playwright');
const path = require('path');
const fs = require('fs');

const PORT = parseInt(process.env.PORT || '9222', 10);
const OUTPUT_DIR = process.env.OUTPUT_DIR || path.join(__dirname, '..', 'output');
const HEADLESS = process.env.BROWSER_HEADLESS !== 'false';
const IDLE_TIMEOUT_MS = 15 * 60 * 1000; // 15 minutes
// Per-user Chrome profile root: actual path is {base}/{safeUserId}
const BROWSER_USER_DATA_BASE = process.env.BROWSER_USER_DATA_BASE
  || path.join(__dirname, '..', 'data', 'browser-profiles');

// Sanitize userId for use as a directory name (max 64 chars)
function safeUserId(userId) {
  return (userId || 'default').replace(/[^a-zA-Z0-9_-]/g, '_').slice(0, 64);
}

// ── Stealth init script ────────────────────────────────────────────────────────
// 注入每个页面，覆盖 headless Chromium 暴露的自动化特征，降低被检测为 bot 的概率。
const STEALTH_SCRIPT = `
(() => {
  // 1. 隐藏 webdriver 自动化标志（最关键，几乎所有检测都查此属性）
  try {
    Object.defineProperty(navigator, 'webdriver', { get: () => undefined });
  } catch (_) {}

  // 2. 伪造插件列表（headless 下 navigator.plugins 为空数组，特征明显）
  try {
    const fakeMime = { type: 'application/pdf', suffixes: 'pdf', description: '' };
    const fakePlugin = Object.create(Plugin.prototype);
    Object.defineProperties(fakePlugin, {
      name:        { get: () => 'Chrome PDF Viewer' },
      filename:    { get: () => 'mhjfbmdgcfjbbpaeojofohoefgiehjai' },
      description: { get: () => 'Portable Document Format' },
      length:      { get: () => 1 },
      0:           { get: () => fakeMime },
    });
    const fakeArr = Object.create(PluginArray.prototype);
    Object.defineProperties(fakeArr, {
      length:            { get: () => 1 },
      0:                 { get: () => fakePlugin },
      item:              { value: (i) => i === 0 ? fakePlugin : null },
      namedItem:         { value: (n) => n === fakePlugin.name ? fakePlugin : null },
      [Symbol.iterator]: { value: function* () { yield fakePlugin; } },
    });
    Object.defineProperty(navigator, 'plugins', { get: () => fakeArr });
  } catch (_) {}

  // 3. 语言 / 时区与 context 保持一致
  try {
    Object.defineProperty(navigator, 'languages', { get: () => ['zh-CN', 'zh', 'en-US', 'en'] });
  } catch (_) {}

  // 4. 补齐 window.chrome.runtime（headless 下此对象缺失，检测时常见特征）
  try {
    if (!window.chrome) window.chrome = {};
    if (!window.chrome.runtime) {
      window.chrome.runtime = {
        connect:    () => {},
        sendMessage: () => {},
        onMessage:  { addListener: () => {}, removeListener: () => {} },
      };
    }
  } catch (_) {}

  // 5. permissions.query：notifications 权限不返回 'denied'（Headless 特有行为）
  try {
    const _q = navigator.permissions.query.bind(navigator.permissions);
    navigator.permissions.query = (p) =>
      p.name === 'notifications'
        ? Promise.resolve({ state: Notification.permission, onchange: null })
        : _q(p);
  } catch (_) {}
})();
`;

// ── Global state ───────────────────────────────────────────────────────────────

/** @type {import('playwright').Browser | null} */
let browser = null;

/**
 * Per-user state.
 * @type {Map<string, {
 *   context: import('playwright').BrowserContext,
 *   pages: import('playwright').Page[],
 *   activePageIdx: number,
 *   refs: Map<string, {role: string, name: string, nth: number}>,
 *   lastActive: number
 * }>}
 */
const users = new Map();

// ── Helpers ────────────────────────────────────────────────────────────────────

function getUser(userId) {
  return users.get(userId) || null;
}

function getActivePage(userId) {
  const u = getUser(userId);
  if (!u || u.pages.length === 0) return null;
  return u.pages[u.activePageIdx] || u.pages[0];
}

function touchUser(userId) {
  const u = getUser(userId);
  if (u) u.lastActive = Date.now();
}

/**
 * Parse Playwright ariaSnapshot YAML-like text and assign ref labels to interactive elements.
 * Returns { text: string, refs: Map<string, {role, name, nth}> }
 */
function parseSnapshot(raw) {
  const interactiveRoles = new Set([
    'button', 'link', 'textbox', 'checkbox', 'radio', 'combobox', 'listbox',
    'option', 'menuitem', 'tab', 'searchbox', 'spinbutton', 'switch',
    'treeitem', 'gridcell', 'columnheader', 'rowheader', 'slider',
  ]);

  const refs = new Map();
  const roleCounters = new Map(); // role+name → count (for nth)
  let refCounter = 1;

  const lines = raw.split('\n');
  const outputLines = [];

  for (const line of lines) {
    // Match lines like:  - button "Submit" or  - link "Home" or  - textbox "Search"
    // Format: optional spaces, "- ", role, optional quoted name, optional attributes
    const m = line.match(/^(\s*-\s+)(\w[\w-]*)(?:\s+"([^"]*)")?(.*)$/);
    if (m) {
      const [, prefix, role, name = '', rest] = m;
      const roleLower = role.toLowerCase();

      if (interactiveRoles.has(roleLower)) {
        const key = `${roleLower}::${name}`;
        const nth = roleCounters.get(key) || 0;
        roleCounters.set(key, nth + 1);

        const ref = `e${refCounter++}`;
        refs.set(ref, { role: roleLower, name, nth });

        // Insert [ref=eN] before trailing attributes
        const nameStr = name ? ` "${name}"` : '';
        outputLines.push(`${prefix}${role}${nameStr} [ref=${ref}]${rest}`);
        continue;
      }
    }
    outputLines.push(line);
  }

  return { text: outputLines.join('\n'), refs };
}

// ── Routes ─────────────────────────────────────────────────────────────────────

const app = express();
app.use(express.json({ limit: '10mb' }));

// Middleware: extract x-user-id
app.use((req, res, next) => {
  req.userId = req.headers['x-user-id'] || 'default';
  next();
});

// GET /status
app.get('/status', (req, res) => {
  const u = getUser(req.userId);
  if (!u) {
    return res.json({ launched: false, pageCount: 0 });
  }
  res.json({
    launched: true,
    pageCount: u.pages.length,
    activePageIdx: u.activePageIdx,
    currentUrl: u.pages[u.activePageIdx]?.url() || '',
    browserRunning: browser !== null,
  });
});

// POST /launch
app.post('/launch', async (req, res) => {
  try {
    if (users.has(req.userId)) {
      return res.json({ status: 'already_launched' });
    }

    // 每位用户对应独立的 Chrome Profile 目录，cookies/localStorage 自动持久化，
    // 重启服务后登录态仍保留，无需手动 save_cookies/load_cookies。
    const userDataDir = path.join(BROWSER_USER_DATA_BASE, safeUserId(req.userId));
    fs.mkdirSync(userDataDir, { recursive: true });

    // visible:true is the preferred alias; headless:false is also accepted
    const headlessOpt = req.body.visible === true ? false
      : typeof req.body.headless === 'boolean' ? req.body.headless
      : HEADLESS;
    const context = await chromium.launchPersistentContext(userDataDir, {
      headless: headlessOpt,
      args: [
        '--no-sandbox',
        '--disable-setuid-sandbox',
        '--disable-dev-shm-usage',
        '--disable-blink-features=AutomationControlled',
      ],
      viewport: { width: 1280, height: 800 },
      deviceScaleFactor: 2, // Retina 2× — 截图像素翻倍，Mermaid 图等内容文字清晰
      userAgent: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36',
      locale: 'zh-CN',
      timezoneId: 'Asia/Shanghai',
      extraHTTPHeaders: { 'Accept-Language': 'zh-CN,zh;q=0.9,en-US;q=0.8,en;q=0.7' },
    });
    // 对此 context 下所有页面（含新开标签页）注入 stealth 脚本
    await context.addInitScript(STEALTH_SCRIPT);
    const page = await context.newPage();

    users.set(req.userId, {
      context,
      pages: [page],
      activePageIdx: 0,
      refs: new Map(),
      lastActive: Date.now(),
    });

    const autoSessionFile = path.join(userDataDir, 'auto-session.json');

    // In visible mode: save cookies on every page load across ALL tabs.
    // Ensures session is persisted even if user closes the window abruptly.
    if (!headlessOpt) {
      const saveSession = async () => {
        try {
          const cookies = await context.cookies();
          if (cookies.length > 0) {
            fs.writeFileSync(autoSessionFile, JSON.stringify(cookies));
            console.log(`[browser] session saved: ${cookies.length} cookies → ${autoSessionFile}`);
          }
        } catch (e) {
          console.log(`[browser] session save failed: ${e.message}`);
        }
      };
      const attachSaveListener = p => p.on('load', saveSession);
      attachSaveListener(page);                   // initial tab
      context.on('page', attachSaveListener);     // any new tab
    }

    // Clean up map when browser window is closed by user
    context.on('close', () => { users.delete(req.userId); });

    // Restore last auto-saved session if exists
    if (fs.existsSync(autoSessionFile)) {
      try {
        const cookies = JSON.parse(fs.readFileSync(autoSessionFile, 'utf-8'));
        if (cookies.length > 0) {
          await context.addCookies(cookies);
          console.log(`[browser] session restored: ${cookies.length} cookies from ${autoSessionFile}`);
        }
      } catch (e) {
        console.log(`[browser] session restore failed: ${e.message}`);
      }
    } else {
      console.log(`[browser] no saved session found at ${autoSessionFile}`);
    }

    res.json({ status: 'ok' });
  } catch (err) {
    res.status(500).json({ error: err.message });
  }
});

// POST /close
app.post('/close', async (req, res) => {
  const u = getUser(req.userId);
  if (!u) {
    return res.json({ status: 'not_launched' });
  }
  // Save cookies BEFORE closing — context is still alive at this point.
  // (If user already closed the window, context.cookies() will throw; we ignore it.)
  const userDataDir = path.join(BROWSER_USER_DATA_BASE, safeUserId(req.userId));
  const autoSessionFile = path.join(userDataDir, 'auto-session.json');
  try {
    const cookies = await u.context.cookies();
    if (cookies.length > 0) fs.writeFileSync(autoSessionFile, JSON.stringify(cookies));
  } catch (_) {}
  // Always remove from map; context may already be closed if user shut the window
  users.delete(req.userId);
  try {
    await u.context.close();
  } catch (_) { /* already closed by user — ignore */ }
  res.json({ status: 'ok' });
});

// POST /navigate  { url: string, targetId?: string }
app.post('/navigate', async (req, res) => {
  try {
    touchUser(req.userId);
    const page = getActivePage(req.userId);
    if (!page) {
      return res.status(400).json({ error: 'No active page. Call /launch first.' });
    }
    const { url, timeoutMs = 30000 } = req.body;
    if (!url) return res.status(400).json({ error: 'url is required' });

    const resp = await page.goto(url, { timeout: timeoutMs, waitUntil: 'domcontentloaded' });
    // Clear refs after navigation
    const u = getUser(req.userId);
    if (u) u.refs = new Map();

    res.json({
      status: 'ok',
      url: page.url(),
      title: await page.title(),
      httpStatus: resp ? resp.status() : null,
    });
  } catch (err) {
    res.status(500).json({ error: err.message });
  }
});

// GET /snapshot?fullPage=false
app.get('/snapshot', async (req, res) => {
  try {
    touchUser(req.userId);
    const page = getActivePage(req.userId);
    if (!page) {
      return res.status(400).json({ error: 'No active page. Call /launch first.' });
    }

    const raw = await page.locator(':root').ariaSnapshot();
    const { text, refs } = parseSnapshot(raw);

    // Store refs per user
    const u = getUser(req.userId);
    if (u) u.refs = refs;

    res.json({
      url: page.url(),
      title: await page.title(),
      snapshot: text,
      refCount: refs.size,
    });
  } catch (err) {
    res.status(500).json({ error: err.message });
  }
});

// POST /act  { action, ref?, text?, key?, values?, selector?, targetId?, timeoutMs? }
app.post('/act', async (req, res) => {
  try {
    touchUser(req.userId);
    const page = getActivePage(req.userId);
    if (!page) {
      return res.status(400).json({ error: 'No active page. Call /launch first.' });
    }

    const u = getUser(req.userId);
    const { action, ref, text, key, values, selector, timeoutMs = 10000 } = req.body;

    // Resolve ref to locator
    let locator = null;
    if (ref) {
      if (!u || !u.refs.has(ref)) {
        return res.status(400).json({ error: `Unknown ref: ${ref}. Call /snapshot first.` });
      }
      const { role, name, nth } = u.refs.get(ref);
      locator = page.getByRole(role, name ? { name, exact: false } : {}).nth(nth);
    }

    switch (action) {
      case 'click': {
        if (!locator) return res.status(400).json({ error: 'ref is required for click' });
        await locator.click({ timeout: timeoutMs });
        // Clear refs after navigation might have occurred
        if (u) u.refs = new Map();
        break;
      }
      case 'type': {
        if (!locator) return res.status(400).json({ error: 'ref is required for type' });
        await locator.fill(text || '', { timeout: timeoutMs });
        break;
      }
      case 'select': {
        if (!locator) return res.status(400).json({ error: 'ref is required for select' });
        await locator.selectOption(values || [], { timeout: timeoutMs });
        break;
      }
      case 'hover': {
        if (!locator) return res.status(400).json({ error: 'ref is required for hover' });
        await locator.hover({ timeout: timeoutMs });
        break;
      }
      case 'scroll': {
        const { deltaX = 0, deltaY = 500 } = req.body;
        if (locator) {
          await locator.scrollIntoViewIfNeeded({ timeout: timeoutMs });
        } else {
          await page.mouse.wheel(deltaX, deltaY);
        }
        break;
      }
      case 'drag': {
        // 人类化拖拽：用于滑块验证码等场景。
        // ref 或 selector 指定被拖拽元素，deltaX/deltaY 为拖拽距离（px）。
        let dragLocator = locator;
        if (!dragLocator) {
          if (!selector) return res.status(400).json({ error: 'ref or selector is required for drag' });
          dragLocator = page.locator(selector).first();
        }
        const { deltaX: dx = 0, deltaY: dy = 0 } = req.body;

        const box = await dragLocator.boundingBox({ timeout: timeoutMs });
        if (!box) return res.status(400).json({ error: 'drag: element not visible or not found' });

        const sx = box.x + box.width / 2;
        const sy = box.y + box.height / 2;
        const ex = sx + dx;
        const ey = sy + dy;

        // 移动到起点 → 短暂停顿 → 按下 → ease-in-out 曲线移动 + 随机微抖 → 松开
        await page.mouse.move(sx, sy, { steps: 5 });
        await page.waitForTimeout(60 + Math.random() * 80);
        await page.mouse.down();
        await page.waitForTimeout(40 + Math.random() * 60);

        const steps = 35 + Math.floor(Math.random() * 10);
        for (let i = 1; i <= steps; i++) {
          const t = i / steps;
          const eased = t < 0.5 ? 2 * t * t : -1 + (4 - 2 * t) * t;
          const wx = (Math.random() - 0.5) * 2;  // x 方向微抖 ±1px
          const wy = (Math.random() - 0.5) * 3;  // y 方向微抖 ±1.5px
          await page.mouse.move(sx + (ex - sx) * eased + wx, sy + (ey - sy) * eased + wy);
          await page.waitForTimeout(8 + Math.random() * 16);
        }

        await page.mouse.move(ex, ey); // 精确落点
        await page.waitForTimeout(50 + Math.random() * 80);
        await page.mouse.up();
        break;
      }

      case 'solve_slider_captcha': {
        // 滑块验证码自动求解：B（canvas 亮度+边缘分析）→ C（DOM 探针）→ 截图通知用户手动
        const { sliderSelector: customSliderSel = '' } = req.body;

        // ── Helper: screenshot + manual_required response ──────────────────
        const requireManual = async (reason) => {
          const bucket = req.userId.slice(-1) || '0';
          const dir = path.join(OUTPUT_DIR, bucket);
          fs.mkdirSync(dir, { recursive: true });
          const filename = `captcha_${Date.now()}.png`;
          const filePath = path.join(dir, filename);
          try { await page.screenshot({ path: filePath }); } catch (_) {}
          return {
            status: 'manual_required',
            reason,
            webUrl: '/output/' + path.join(bucket, filename).replace(/\\/g, '/'),
            message: '自动识别验证码失败，已截图，请手动完成验证',
          };
        };

        // ── Method B: Canvas pixel analysis ────────────────────────────────
        let gapX = -1;
        let detectionMethod = null;

        const canvasResult = await page.evaluate(() => {
          try {
            const canvases = Array.from(document.querySelectorAll('canvas'))
              .filter(c => c.width > 80 && c.height > 40)
              .sort((a, b) => (b.width * b.height) - (a.width * a.height));
            if (!canvases.length) return { ok: false, reason: 'no_canvas' };

            const canvas = canvases[0];
            const ctx = canvas.getContext('2d');
            if (!ctx) return { ok: false, reason: 'no_ctx' };

            const w = canvas.width, h = canvas.height;
            let imgData;
            try { imgData = ctx.getImageData(0, 0, w, h); }
            catch (_) { return { ok: false, reason: 'tainted' }; }

            const data = imgData.data;

            // Per-column luminance (weighted RGB → Y)
            const lum = new Float32Array(w);
            for (let x = 0; x < w; x++) {
              let s = 0;
              for (let y = 0; y < h; y++) {
                const i = (y * w + x) * 4;
                s += 0.299 * data[i] + 0.587 * data[i + 1] + 0.114 * data[i + 2];
              }
              lum[x] = s / h;
            }

            // 5-point moving average
            const sm = new Float32Array(w);
            for (let x = 0; x < w; x++) {
              let s = 0, cnt = 0;
              for (let dx = -2; dx <= 2; dx++) {
                const xx = x + dx;
                if (xx >= 0 && xx < w) { s += lum[xx]; cnt++; }
              }
              sm[x] = s / cnt;
            }

            // Global average (skip 10px edges)
            let avg = 0;
            for (let x = 10; x < w - 10; x++) avg += sm[x];
            avg /= (w - 20);

            // Find longest run significantly below average (≥ 8px wide)
            const threshold = avg * 0.83;
            let bestLen = 0, bestMid = -1, runStart = -1;
            for (let x = 10; x < w - 10; x++) {
              if (sm[x] < threshold) {
                if (runStart < 0) runStart = x;
                const len = x - runStart + 1;
                if (len > bestLen) { bestLen = len; bestMid = Math.floor((runStart + x) / 2); }
              } else { runStart = -1; }
            }
            if (bestMid >= 0 && bestLen >= 8) {
              return { ok: true, method: 'brightness', gapX: bestMid };
            }

            // Fallback: edge density peak
            const edge = new Float32Array(w);
            for (let x = 1; x < w - 1; x++) {
              for (let y = 0; y < h; y++) {
                const i = (y * w + x) * 4, iL = (y * w + x - 1) * 4;
                edge[x] += Math.abs(data[i] - data[iL])
                  + Math.abs(data[i + 1] - data[iL + 1])
                  + Math.abs(data[i + 2] - data[iL + 2]);
              }
            }
            const se = new Float32Array(w);
            for (let x = 2; x < w - 2; x++)
              se[x] = (edge[x-2]+edge[x-1]+edge[x]+edge[x+1]+edge[x+2]) / 5;

            let maxE = 0, peakX = -1;
            for (let x = 15; x < w - 15; x++) {
              if (se[x] > maxE) { maxE = se[x]; peakX = x; }
            }
            return peakX >= 0
              ? { ok: true, method: 'edge', gapX: peakX }
              : { ok: false, reason: 'no_gap' };
          } catch (e) {
            return { ok: false, reason: e.message };
          }
        });

        if (canvasResult.ok) {
          gapX = canvasResult.gapX;
          detectionMethod = canvasResult.method;
        }

        // ── Method C: DOM state probes ──────────────────────────────────────
        if (gapX < 0) {
          const domResult = await page.evaluate(() => {
            const probes = [
              // CSS left on gap/cutout element
              () => {
                const el = document.querySelector('[class*="gap"],[class*="cutout"],[class*="hole"],[class*="puzzle"]');
                if (!el) return -1;
                const m = (window.getComputedStyle(el).left || '').match(/(\d+(?:\.\d+)?)/);
                return m ? parseFloat(m[1]) : -1;
              },
              // Inverse of negative background-position on slide image
              () => {
                const el = document.querySelector('[class*="slide"][class*="img"],[class*="vcode"][class*="img"]');
                if (!el) return -1;
                const m = (window.getComputedStyle(el).backgroundPosition || '').match(/(-?\d+(?:\.\d+)?)/);
                return m ? -parseFloat(m[1]) : -1;
              },
              // Common global CAPTCHA state variables
              () => {
                for (const k of ['captchaX','verifyX','slideX','_captchaX','_offsetX']) {
                  if (typeof window[k] === 'number' && window[k] > 0) return window[k];
                }
                return -1;
              },
            ];
            for (const p of probes) {
              try { const v = p(); if (v > 0) return { ok: true, gapX: Math.round(v) }; }
              catch (_) {}
            }
            return { ok: false };
          });

          if (domResult.ok) {
            gapX = domResult.gapX;
            detectionMethod = 'dom';
          }
        }

        // ── Fallback: screenshot ────────────────────────────────────────────
        if (gapX < 0) {
          return res.json(await requireManual('gap_detection_failed'));
        }

        // ── Find slider element ─────────────────────────────────────────────
        const SLIDER_SELS = [
          customSliderSel,
          '.vcode-slide-btn', '.BAIDUID-slide-btn',
          '[class*="slide-btn"]', '[class*="slider-btn"]',
          '[class*="slideBtn"]', '[class*="dragBtn"]',
          '[class*="drag-btn"]', '[class*="slider-handle"]',
        ].filter(Boolean);

        let sliderBox = null;
        for (const sel of SLIDER_SELS) {
          try {
            const box = await page.locator(sel).first().boundingBox({ timeout: 2000 });
            if (box) { sliderBox = box; break; }
          } catch (_) {}
        }

        if (!sliderBox) {
          return res.json(await requireManual('slider_not_found'));
        }

        // ── Convert canvas gapX → page coordinates ──────────────────────────
        const bgRect = await page.evaluate(() => {
          const c = Array.from(document.querySelectorAll('canvas'))
            .filter(c => c.width > 80 && c.height > 40)
            .sort((a, b) => (b.width * b.height) - (a.width * a.height))[0];
          if (!c) return null;
          const r = c.getBoundingClientRect();
          return { left: r.left, width: r.width, canvasW: c.width };
        });

        let dx;
        if (bgRect && bgRect.canvasW > 0) {
          const scale = bgRect.width / bgRect.canvasW;
          const gapPageX = bgRect.left + gapX * scale;
          dx = gapPageX - (sliderBox.x + sliderBox.width / 2);
        } else {
          dx = gapX;
        }

        // ── Human-like drag ─────────────────────────────────────────────────
        const sx = sliderBox.x + sliderBox.width / 2;
        const sy = sliderBox.y + sliderBox.height / 2;
        const ex = sx + dx;

        await page.mouse.move(sx, sy, { steps: 5 });
        await page.waitForTimeout(60 + Math.random() * 80);
        await page.mouse.down();
        await page.waitForTimeout(40 + Math.random() * 60);

        const steps = 35 + Math.floor(Math.random() * 10);
        for (let i = 1; i <= steps; i++) {
          const t = i / steps;
          const eased = t < 0.5 ? 2 * t * t : -1 + (4 - 2 * t) * t;
          await page.mouse.move(
            sx + (ex - sx) * eased + (Math.random() - 0.5) * 2,
            sy + (Math.random() - 0.5) * 2,
          );
          await page.waitForTimeout(8 + Math.random() * 16);
        }
        await page.mouse.move(ex, sy);
        await page.waitForTimeout(50 + Math.random() * 80);
        await page.mouse.up();

        await page.waitForTimeout(1200);

        return res.json({
          status: 'ok',
          method: detectionMethod,
          gapX,
          dragDelta: Math.round(dx),
          url: page.url(),
        });
      }

      case 'press_key': {
        if (!key) return res.status(400).json({ error: 'key is required for press_key' });
        if (locator) {
          await locator.press(key, { timeout: timeoutMs });
        } else {
          await page.keyboard.press(key);
        }
        break;
      }
      case 'wait': {
        if (selector) {
          await page.waitForSelector(selector, { timeout: timeoutMs });
        } else {
          await page.waitForTimeout(timeoutMs > 5000 ? 5000 : timeoutMs);
        }
        break;
      }
      case 'evaluate': {
        const { script } = req.body;
        if (!script) return res.status(400).json({ error: 'script is required for evaluate' });
        const result = await page.evaluate(script);
        return res.json({ status: 'ok', result });
      }
      case 'save_cookies': {
        const { cookieName } = req.body;
        if (!cookieName) return res.status(400).json({ error: 'cookieName is required' });
        const safeName = cookieName.replace(/[^a-zA-Z0-9_-]/g, '_');
        const dir = path.join(OUTPUT_DIR, 'browser-cookies', req.userId);
        fs.mkdirSync(dir, { recursive: true });
        const cookies = await u.context.cookies();
        const filePath = path.join(dir, `${safeName}.json`);
        fs.writeFileSync(filePath, JSON.stringify(cookies, null, 2));
        return res.json({ status: 'ok', profile: safeName, saved: cookies.length });
      }
      case 'load_cookies': {
        const { cookieName } = req.body;
        if (!cookieName) return res.status(400).json({ error: 'cookieName is required' });
        const safeName = cookieName.replace(/[^a-zA-Z0-9_-]/g, '_');
        const filePath = path.join(OUTPUT_DIR, 'browser-cookies', req.userId, `${safeName}.json`);
        if (!fs.existsSync(filePath)) {
          return res.status(404).json({ error: `No saved cookies for profile "${safeName}"` });
        }
        const cookies = JSON.parse(fs.readFileSync(filePath, 'utf-8'));
        await u.context.addCookies(cookies);
        return res.json({ status: 'ok', profile: safeName, loaded: cookies.length });
      }
      case 'list_cookies': {
        const dir = path.join(OUTPUT_DIR, 'browser-cookies', req.userId);
        if (!fs.existsSync(dir)) return res.json({ profiles: [] });
        const profiles = fs.readdirSync(dir)
          .filter(f => f.endsWith('.json'))
          .map(f => f.slice(0, -5));
        return res.json({ profiles });
      }
      default:
        return res.status(400).json({ error: `Unknown action: ${action}` });
    }

    res.json({ status: 'ok', url: page.url() });
  } catch (err) {
    res.status(500).json({ error: err.message });
  }
});

// POST /screenshot
app.post('/screenshot', async (req, res) => {
  try {
    touchUser(req.userId);
    const page = getActivePage(req.userId);
    if (!page) {
      return res.status(400).json({ error: 'No active page. Call /launch first.' });
    }

    // Ensure output dir exists
    const bucket = req.userId.slice(-1) || '0';
    const dir = path.join(OUTPUT_DIR, bucket);
    fs.mkdirSync(dir, { recursive: true });

    const selector = req.body.selector;

    // 如果指定了 selector，先尝试提取 SVG（如 Mermaid 图表）。
    // SVG 是矢量格式，无像素分辨率损失；提取失败时降级为 PNG 截图。
    if (selector) {
      // 等待 SVG 子元素出现（Mermaid 等异步渲染库在 JS 执行后才生成 <svg>）
      // 超时则静默继续，下方 evaluate 若仍拿不到会降级到 PNG
      await page.waitForSelector(selector + ' svg', { timeout: 8000 }).catch(() => {});

      const svgHTML = await page.evaluate((sel) => {
        const el = document.querySelector(sel);
        if (!el) return null;
        const svg = el.tagName.toLowerCase() === 'svg' ? el : el.querySelector('svg');
        if (!svg) return null;
        // 将相对尺寸固化为实际像素，避免 <img> 内嵌时尺寸不确定
        const box = svg.getBoundingClientRect();
        if (box.width > 0) {
          svg.setAttribute('width', String(Math.round(box.width)));
          svg.setAttribute('height', String(Math.round(box.height)));
        }
        svg.setAttribute('xmlns', 'http://www.w3.org/2000/svg');
        return svg.outerHTML;
      }, selector).catch(() => null);

      if (svgHTML) {
        const svgFilename = `screenshot_${Date.now()}.svg`;
        const svgFilePath = path.join(dir, svgFilename);
        fs.writeFileSync(svgFilePath, svgHTML, 'utf-8');
        const relativePath = path.join(bucket, svgFilename).replace(/\\/g, '/');
        return res.json({
          status: 'ok',
          path: relativePath,
          absolutePath: svgFilePath,
          webUrl: '/output/' + relativePath,
        });
      }

      // 降级：元素无 SVG，正常 PNG 截图
      const filename = `screenshot_${Date.now()}.png`;
      const filePath = path.join(dir, filename);
      await page.locator(selector).first().screenshot({ path: filePath });
      const relativePath = path.join(bucket, filename).replace(/\\/g, '/');
      return res.json({
        status: 'ok',
        path: relativePath,
        absolutePath: filePath,
        webUrl: '/output/' + relativePath,
      });
    }

    // 无 selector：全页 / 可视区 PNG 截图
    const filename = `screenshot_${Date.now()}.png`;
    const filePath = path.join(dir, filename);
    await page.screenshot({ path: filePath, fullPage: req.body.fullPage === true });

    // Return relative path from output dir + web-accessible URL
    const relativePath = path.join(bucket, filename);
    res.json({
      status: 'ok',
      path: relativePath,
      absolutePath: filePath,
      webUrl: '/output/' + relativePath.replace(/\\/g, '/'),
    });
  } catch (err) {
    res.status(500).json({ error: err.message });
  }
});

// GET /tabs
app.get('/tabs', async (req, res) => {
  try {
    const u = getUser(req.userId);
    if (!u) return res.json({ tabs: [] });

    const tabs = await Promise.all(u.pages.map(async (p, i) => ({
      index: i,
      url: p.url(),
      title: await p.title(),
      active: i === u.activePageIdx,
    })));
    res.json({ tabs });
  } catch (err) {
    res.status(500).json({ error: err.message });
  }
});

// POST /tab/open  { url?: string }
app.post('/tab/open', async (req, res) => {
  try {
    const u = getUser(req.userId);
    if (!u) return res.status(400).json({ error: 'Not launched' });

    const page = await u.context.newPage();
    u.pages.push(page);
    u.activePageIdx = u.pages.length - 1;
    u.refs = new Map();

    if (req.body.url) {
      await page.goto(req.body.url, { waitUntil: 'domcontentloaded' });
    }

    res.json({ status: 'ok', index: u.activePageIdx, url: page.url() });
  } catch (err) {
    res.status(500).json({ error: err.message });
  }
});

// POST /tab/close  { index?: number }
app.post('/tab/close', async (req, res) => {
  try {
    const u = getUser(req.userId);
    if (!u) return res.status(400).json({ error: 'Not launched' });

    const idx = typeof req.body.index === 'number' ? req.body.index : u.activePageIdx;
    if (idx < 0 || idx >= u.pages.length) {
      return res.status(400).json({ error: `Invalid tab index: ${idx}` });
    }

    await u.pages[idx].close();
    u.pages.splice(idx, 1);
    if (u.pages.length === 0) {
      // Re-open a blank page so context stays alive
      const p = await u.context.newPage();
      u.pages.push(p);
    }
    u.activePageIdx = Math.min(u.activePageIdx, u.pages.length - 1);
    u.refs = new Map();

    res.json({ status: 'ok', pageCount: u.pages.length, activePageIdx: u.activePageIdx });
  } catch (err) {
    res.status(500).json({ error: err.message });
  }
});

// POST /pdf  { html: string }
// 将 HTML 内容渲染为 PDF 字节流（base64 编码后返回）。
// 使用共享 browser 开临时页面，渲染完成后立即关闭，无需 /launch。
app.post('/pdf', async (req, res) => {
  try {
    if (!browser) {
      return res.status(503).json({ error: 'Browser not started' });
    }
    const { html } = req.body;
    if (!html) return res.status(400).json({ error: 'html is required' });

    const context = await browser.newContext();
    const page = await context.newPage();
    try {
      await page.setContent(html, { waitUntil: 'networkidle' });
      const pdfBytes = await page.pdf({
        format: 'A4',
        printBackground: true,
        margin: { top: '20mm', bottom: '20mm', left: '20mm', right: '20mm' },
      });
      res.json({ pdf: Buffer.from(pdfBytes).toString('base64') });
    } finally {
      await context.close();
    }
  } catch (err) {
    res.status(500).json({ error: err.message });
  }
});

// ── Idle cleanup ───────────────────────────────────────────────────────────────

setInterval(async () => {
  const now = Date.now();
  for (const [userId, u] of users.entries()) {
    if (now - u.lastActive > IDLE_TIMEOUT_MS) {
      console.log(`[browser-server] Cleaning up idle context for user: ${userId}`);
      try {
        await u.context.close();
      } catch (_) { /* ignore */ }
      users.delete(userId);
    }
  }
}, 60_000); // Check every minute

// ── Startup ────────────────────────────────────────────────────────────────────

async function main() {
  console.log(`[browser-server] Launching Chromium (headless=${HEADLESS})...`);
  browser = await chromium.launch({
    headless: HEADLESS,
    args: [
      '--no-sandbox',
      '--disable-setuid-sandbox',
      '--disable-dev-shm-usage',
      '--disable-blink-features=AutomationControlled', // 隐藏自动化标志
    ],
  });
  console.log('[browser-server] Chromium launched.');

  app.listen(PORT, '127.0.0.1', () => {
    console.log(`[browser-server] Listening on http://127.0.0.1:${PORT}`);
  });
}

// ── Graceful shutdown ──────────────────────────────────────────────────────────

async function shutdown() {
  console.log('[browser-server] Shutting down...');
  for (const u of users.values()) {
    try { await u.context.close(); } catch (_) { /* ignore */ }
  }
  users.clear();
  if (browser) {
    try { await browser.close(); } catch (_) { /* ignore */ }
    browser = null;
  }
  process.exit(0);
}

process.on('SIGTERM', shutdown);
process.on('SIGINT', shutdown);

main().catch(err => {
  console.error('[browser-server] Fatal error:', err);
  process.exit(1);
});
