#!/usr/bin/env node

// Warm-session CLI measurement. Playwright's snapshot artifact is included in
// effective payload bytes because an agent must read it to inspect the page.

import { spawn } from 'node:child_process';
import { mkdtempSync, readFileSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { performance } from 'node:perf_hooks';

const [provider, ...urls] = process.argv.slice(2);
if (!['ghostchrome', 'playwright'].includes(provider) || urls.length === 0) {
  console.error('usage: cli-measure.mjs ghostchrome|playwright <url> [url...]');
  process.exit(2);
}

const ghostchrome = process.env.GHOSTCHROME_BIN;
const playwrightCli = process.env.PWCLI_BIN;
const chrome = process.env.BENCH_CHROME_PATH;
if (!ghostchrome || !playwrightCli || !chrome)
  throw new Error('GHOSTCHROME_BIN, PWCLI_BIN, and BENCH_CHROME_PATH are required');

const workdir = mkdtempSync(join(tmpdir(), `ghostchrome-${provider}-cli-bench-`));
const session = `real-bench-${provider}-${process.pid}`;
const configPath = join(workdir, 'playwright-config.json');
writeFileSync(configPath, JSON.stringify({
  browser: {
    browserName: 'chromium',
    launchOptions: { headless: true, executablePath: chrome, args: ['--no-sandbox'] },
  },
  outputDir: join(workdir, 'playwright-output'),
  console: { level: 'error' },
}));

function run(command, args, extraEnv = {}) {
  const before = performance.now();
  return new Promise(resolve => {
    const child = spawn(command, args, {
      cwd: workdir,
      env: { ...process.env, ...extraEnv },
      stdio: ['ignore', 'pipe', 'pipe'],
    });
    let stdout = '', stderr = '';
    child.stdout.on('data', chunk => { stdout += chunk; });
    child.stderr.on('data', chunk => { stderr += chunk; });
    child.on('close', code => resolve({
      code, stdout, stderr, durationMs: Math.round(performance.now() - before),
    }));
  });
}

function artifactFrom(output) {
  const match = output.match(/\[Snapshot\]\(([^)]+\.yml)\)/);
  if (!match) return { path: '', text: '', bytes: 0 };
  const path = match[1].startsWith('/') ? match[1] : join(workdir, match[1]);
  try {
    const text = readFileSync(path, 'utf8');
    return { path, text, bytes: Buffer.byteLength(text) };
  } catch {
    return { path, text: '', bytes: 0 };
  }
}

async function invoke(url, opening = false) {
  if (provider === 'ghostchrome') {
    return run(ghostchrome, ['-s', session, 'preview', url, '--wait', 'load'], {
      PLAYWRIGHT_MCP_EXECUTABLE_PATH: chrome,
    });
  }
  const action = opening ? ['open', url, '--config', configPath] : ['goto', url];
  return run('node', [playwrightCli, '-s', session, ...action]);
}

let exitCode = 1;
try {
  await invoke(urls[0], true); // daemon/browser warm-up, intentionally discarded
  for (const url of urls.slice(1)) {
    const result = await invoke(url);
    const response = result.stdout + result.stderr;
    const artifact = artifactFrom(response);
    process.stdout.write(`${JSON.stringify({
      provider, url, durationMs: result.durationMs,
      success: result.code === 0,
      responseBytes: Buffer.byteLength(response),
      artifactBytes: artifact.bytes,
      payloadBytes: Buffer.byteLength(response) + artifact.bytes,
      agentReads: artifact.bytes > 0 ? 2 : 1,
      response,
      artifact: artifact.text,
    })}\n`);
  }
  exitCode = 0;
} finally {
  if (provider === 'ghostchrome')
    await run(ghostchrome, ['sessions', 'stop', session]);
  else
    await run('node', [playwrightCli, '-s', session, 'close']);
  process.exit(exitCode);
}
