#!/usr/bin/env node

// Measure one long-lived MCP browser server without an MCP host or model in the
// middle. The result is JSONL so benchmark runs retain auditable raw samples.

import { spawn } from 'node:child_process';
import { readFileSync, readdirSync } from 'node:fs';
import { performance } from 'node:perf_hooks';

const [provider, ...urls] = process.argv.slice(2);
if (!['ghostchrome', 'playwright'].includes(provider) || urls.length === 0) {
  console.error('usage: mcp-measure.mjs ghostchrome|playwright <url> [url...]');
  process.exit(2);
}

const ghostchrome = process.env.GHOSTCHROME_BIN;
const chrome = process.env.BENCH_CHROME_PATH;
const playwrightPackage = process.env.PWMCP_PACKAGE || '@playwright/mcp@0.0.79';
if (provider === 'ghostchrome' && !ghostchrome)
  throw new Error('GHOSTCHROME_BIN is required');

const command = provider === 'ghostchrome' ? ghostchrome : 'bunx';
const args = provider === 'ghostchrome'
  ? ['mcp']
  : ['-y', playwrightPackage, '--headless', '--isolated', '--no-sandbox',
      '--snapshot-mode', 'full', ...(chrome ? ['--executable-path', chrome] : [])];

const startedAt = performance.now();
const child = spawn(command, args, { stdio: ['pipe', 'pipe', 'pipe'] });
let stderr = '';
child.stderr.on('data', chunk => { stderr += chunk.toString(); });

let buffer = '';
let nextId = 1;
const pending = new Map();
child.stdout.on('data', chunk => {
  buffer += chunk.toString();
  let newline;
  while ((newline = buffer.indexOf('\n')) !== -1) {
    const line = buffer.slice(0, newline).trim();
    buffer = buffer.slice(newline + 1);
    if (!line) continue;
    let message;
    try { message = JSON.parse(line); } catch { continue; }
    const waiter = pending.get(message.id);
    if (!waiter) continue;
    pending.delete(message.id);
    if (message.error) waiter.reject(new Error(JSON.stringify(message.error)));
    else waiter.resolve(message.result);
  }
});

function request(method, params = {}) {
  const id = nextId++;
  child.stdin.write(`${JSON.stringify({ jsonrpc: '2.0', id, method, params })}\n`);
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      pending.delete(id);
      reject(new Error(`timeout: ${method}`));
    }, 60_000);
    pending.set(id, {
      resolve: value => { clearTimeout(timer); resolve(value); },
      reject: error => { clearTimeout(timer); reject(error); },
    });
  });
}

function notify(method, params = {}) {
  child.stdin.write(`${JSON.stringify({ jsonrpc: '2.0', method, params })}\n`);
}

function processTree(rootPid) {
  const rows = [];
  for (const name of readdirSync('/proc')) {
    if (!/^\d+$/.test(name)) continue;
    try {
      const status = readFileSync(`/proc/${name}/status`, 'utf8');
      const ppid = Number(status.match(/^PPid:\s+(\d+)/m)?.[1] || 0);
      const rss = Number(status.match(/^VmRSS:\s+(\d+)/m)?.[1] || 0);
      rows.push({ pid: Number(name), ppid, rss });
    } catch { /* process exited while sampling */ }
  }
  const pids = new Set([rootPid]);
  let changed = true;
  while (changed) {
    changed = false;
    for (const row of rows) {
      if (pids.has(row.ppid) && !pids.has(row.pid)) {
        pids.add(row.pid);
        changed = true;
      }
    }
  }
  return {
    processCount: pids.size,
    rssKb: rows.filter(row => pids.has(row.pid)).reduce((sum, row) => sum + row.rss, 0),
  };
}

function textPayload(result) {
  return (result?.content || [])
    .filter(block => block.type === 'text')
    .map(block => block.text)
    .join('\n');
}

function snapshotArtifact(payload) {
  const match = payload.match(/\[Snapshot\]\(([^)]+\.yml)\)/);
  if (!match) return { path: '', bytes: 0, text: '' };
  try {
    const text = readFileSync(match[1], 'utf8');
    return { path: match[1], bytes: Buffer.byteLength(text), text };
  } catch {
    return { path: match[1], bytes: 0, text: '' };
  }
}

let exitCode = 1;
try {
  await request('initialize', {
    protocolVersion: '2025-11-25',
    capabilities: {},
    clientInfo: { name: 'ghostchrome-real-benchmark', version: '1.0.0' },
  });
  notify('notifications/initialized');
  const initializedMs = Math.round(performance.now() - startedAt);
  const listed = await request('tools/list');
  const tools = listed.tools || [];
  process.stdout.write(`${JSON.stringify({
    type: 'metadata', provider, initializedMs,
    toolCount: tools.length,
    schemaBytes: Buffer.byteLength(JSON.stringify(tools)),
  })}\n`);

  for (const url of urls) {
    const before = performance.now();
    const result = provider === 'ghostchrome'
      ? await request('tools/call', {
          name: 'snapshot', arguments: { url, wait: 'load', level: 'content' },
        })
      : await request('tools/call', {
          name: 'browser_navigate', arguments: { url },
        });
    const durationMs = Math.round(performance.now() - before);
    const payload = textPayload(result);
    // Current Playwright MCP returns a short link to a YAML snapshot. A coding
    // agent must read that file to inspect the page, so retain both the wire
    // response and the effective agent-consumed payload.
    const artifact = snapshotArtifact(payload);
    const responseBytes = Buffer.byteLength(payload);
    let interaction = null;
    if (process.env.MCP_INTERACTION === '1') {
      const snapshotText = payload + artifact.text;
      const ref = provider === 'ghostchrome'
        ? snapshotText.match(/"ref":"(@\d+)","role":"combobox","name":"Quantity"/)?.[1]
        : snapshotText.match(/combobox "Quantity" \[ref=([^\]]+)\]/)?.[1];
      const actionStarted = performance.now();
      const selected = provider === 'ghostchrome'
        ? await request('tools/call', { name: 'select', arguments: { ref, value: '3' } })
        : await request('tools/call', { name: 'browser_select_option', arguments: { element: 'Quantity', target: ref, values: ['3'] } });
      const selectMs = Math.round(performance.now() - actionStarted);
      const evalStarted = performance.now();
      const evaluated = provider === 'ghostchrome'
        ? await request('tools/call', { name: 'eval', arguments: { expression: 'document.querySelector("#qty").value' } })
        : await request('tools/call', { name: 'browser_evaluate', arguments: { function: '() => document.querySelector("#qty").value' } });
      const evalMs = Math.round(performance.now() - evalStarted);
      const actionPayload = textPayload(selected) + textPayload(evaluated);
      interaction = {
        ref, selectMs, evalMs,
        success: !selected?.isError && !evaluated?.isError && /\b3\b/.test(textPayload(evaluated)),
        payloadBytes: responseBytes + artifact.bytes + Buffer.byteLength(actionPayload),
        calls: provider === 'playwright' && artifact.bytes > 0 ? 4 : 3,
      };
    }
    process.stdout.write(`${JSON.stringify({
      type: 'sample', provider, url, durationMs,
      responseBytes,
      artifactBytes: artifact.bytes,
      payloadBytes: responseBytes + artifact.bytes,
      artifactPath: artifact.path,
      success: !result?.isError,
      memory: processTree(child.pid),
      payload,
      artifact: artifact.text,
      interaction,
    })}\n`);
  }
  exitCode = 0;
} catch (error) {
  console.error(error.message);
  if (stderr) console.error(stderr.slice(-2000));
} finally {
  try { child.stdin.end(); } catch { /* ignore */ }
  try { child.kill('SIGTERM'); } catch { /* ignore */ }
  setTimeout(() => process.exit(exitCode), 200);
}
