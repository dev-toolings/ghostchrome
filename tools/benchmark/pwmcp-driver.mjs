#!/usr/bin/env node
// pwmcp-driver : measure Playwright-MCP snapshot output on one or more URLs.
//
// Spawns `@playwright/mcp` over stdio, performs initialize then, for each
// URL: browser_navigate + browser_snapshot. One JSON line emitted on stdout
// per URL. The MCP server is kept warm across URLs : measures per-call
// latency with a hot session (the real LLM-agent loop), not cold-spawn.
//
// Usage: node pwmcp-driver.mjs <url> [<url2> <url3> ...]
//        echo -e "url1\nurl2" | node pwmcp-driver.mjs   (one URL per line on stdin)
// Env:   PWMCP_TIMEOUT_MS=60000  per-tool-call timeout

import { spawn } from 'node:child_process';
import { performance } from 'node:perf_hooks';

async function collectUrls() {
  const argv = process.argv.slice(2).filter(Boolean);
  if (argv.length > 0) return argv;
  // Read from stdin (one URL per line). Fail fast if stdin is a TTY.
  if (process.stdin.isTTY) {
    console.error('usage: pwmcp-driver.mjs <url> [<url2> ...]  OR pipe URLs on stdin');
    process.exit(2);
  }
  let buf = '';
  for await (const chunk of process.stdin) buf += chunk;
  return buf.split('\n').map((s) => s.trim()).filter(Boolean);
}
const urls = await collectUrls();
if (urls.length === 0) {
  console.error('no URLs provided');
  process.exit(2);
}

const TIMEOUT_MS = Number(process.env.PWMCP_TIMEOUT_MS || 60000);

// Resolve a Chromium executable. Prefer PWMCP_CHROME_PATH; otherwise reuse
// the Chromium binary Rod downloaded for ghostchrome (so both tools run
// the same engine : apples to apples). Fall back to letting Playwright-MCP
// auto-resolve (which works on a normal dev machine with Chrome installed).
import { existsSync, readdirSync, readFileSync } from 'node:fs';
import { homedir } from 'node:os';
import { join } from 'node:path';

function resolveChrome() {
  if (process.env.PWMCP_CHROME_PATH) return process.env.PWMCP_CHROME_PATH;
  const rodRoot = join(homedir(), '.cache', 'rod', 'browser');
  if (existsSync(rodRoot)) {
    const entries = readdirSync(rodRoot).filter((e) => e.startsWith('chromium-'));
    for (const e of entries) {
      const p = join(rodRoot, e, 'chrome');
      if (existsSync(p)) return p;
    }
  }
  return null;
}

const chromePath = resolveChrome();
const args = ['-y', '@playwright/mcp@latest', '--headless', '--isolated', '--no-sandbox'];
if (chromePath) args.push('--executable-path', chromePath);

const child = spawn('npx', args, { stdio: ['pipe', 'pipe', 'pipe'] });

let stderr = '';
child.stderr.on('data', (b) => { stderr += b.toString(); });

// JSON-RPC over LSP-style line framing (Playwright-MCP uses newline-delimited JSON).
let buf = '';
const pending = new Map();
let nextId = 1;

child.stdout.on('data', (chunk) => {
  buf += chunk.toString();
  let nl;
  while ((nl = buf.indexOf('\n')) !== -1) {
    const line = buf.slice(0, nl).trim();
    buf = buf.slice(nl + 1);
    if (!line) continue;
    let msg;
    try { msg = JSON.parse(line); } catch { continue; }
    if (msg.id != null && pending.has(msg.id)) {
      const { resolve, reject } = pending.get(msg.id);
      pending.delete(msg.id);
      if (msg.error) reject(new Error(`rpc ${msg.error.code}: ${msg.error.message}`));
      else resolve(msg.result);
    }
  }
});

function call(method, params) {
  const id = nextId++;
  const payload = JSON.stringify({ jsonrpc: '2.0', id, method, params }) + '\n';
  return new Promise((resolve, reject) => {
    pending.set(id, { resolve, reject });
    child.stdin.write(payload);
    setTimeout(() => {
      if (pending.has(id)) {
        pending.delete(id);
        reject(new Error(`timeout on ${method}`));
      }
    }, TIMEOUT_MS);
  });
}

let peakRssKb = 0;
const rssTimer = setInterval(() => {
  try {
    const status = readFileSync(`/proc/${child.pid}/status`, 'utf8');
    const m = status.match(/VmRSS:\s+(\d+)\s+kB/);
    if (m) peakRssKb = Math.max(peakRssKb, Number(m[1]));
  } catch { /* macOS: skip */ }
}, 100);

let exitCode = 1;
try {
  await call('initialize', {
    protocolVersion: '2024-11-05',
    capabilities: {},
    clientInfo: { name: 'pwmcp-bench', version: '1.0' },
  });
  await call('notifications/initialized', {}).catch(() => {});

  const tools = await call('tools/list', {});
  const names = (tools.tools || []).map((t) => t.name);
  const navTool = names.find((n) => /navigate|browser_navigate/.test(n)) || 'browser_navigate';
  const snapTool = names.find((n) => /snapshot|browser_snapshot/.test(n)) || 'browser_snapshot';

  for (const url of urls) {
    const t0 = performance.now();
    const navRes = await call('tools/call', { name: navTool, arguments: { url } });
    if (navRes && navRes.isError) {
      process.stderr.write(`[pwmcp] navigate ${url} failed: ${JSON.stringify(navRes.content).slice(0, 200)}\n`);
      continue;
    }
    const snap = await call('tools/call', { name: snapTool, arguments: {} });
    const durationMs = Math.round(performance.now() - t0);
    if (snap && snap.isError) {
      process.stderr.write(`[pwmcp] snapshot ${url} failed\n`);
      continue;
    }
    const textParts = (snap.content || []).filter((c) => c.type === 'text').map((c) => c.text);
    const bytes = Buffer.byteLength(textParts.join('\n'), 'utf8');
    process.stdout.write(JSON.stringify({
      tool: 'playwright-mcp',
      url,
      bytes,
      durationMs,
      peakRssKb,
      snapTool,
    }) + '\n');
  }
  exitCode = 0;
} catch (err) {
  process.stderr.write(`[pwmcp] ${err.message}\n`);
  if (stderr) process.stderr.write(`[pwmcp-stderr] ${stderr.slice(-500)}\n`);
} finally {
  clearInterval(rssTimer);
  try { child.stdin.end(); } catch { /* ignore */ }
  try { child.kill('SIGTERM'); } catch { /* ignore */ }
  setTimeout(() => process.exit(exitCode), 200);
}
