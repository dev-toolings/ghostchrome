#!/usr/bin/env node
// GitHub launcher: runs ghostchrome straight from the repository, with no npm
// publication and no binary to carry around.
//
//   bunx github:dev-toolings/ghostchrome <command>      # CLI
//   bunx github:dev-toolings/ghostchrome mcp            # MCP server (stdio)
//
// On first use it downloads the prebuilt binary for this platform from the
// matching GitHub Release, verifies it against the release checksums.txt, and
// caches it under ~/.ghostchrome/releases/<version>/. Later runs start the
// cached binary directly. stdout belongs to the child (the MCP channel), so
// every launcher message goes to stderr.
//
// Version: GHOSTCHROME_VERSION (e.g. v0.8.0), else the version of the
// package.json next to this checkout (the ref bunx fetched), else the latest
// release.
import { spawn } from "node:child_process";
import { createHash } from "node:crypto";
import { chmodSync, existsSync, mkdirSync, readFileSync, readdirSync, renameSync, rmSync, writeFileSync } from "node:fs";
import { constants, homedir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const REPO = "dev-toolings/ghostchrome";
const OS = { linux: "linux", darwin: "darwin", win32: "windows" }[process.platform];
const ARCH = { x64: "amd64", arm64: "arm64" }[process.arch];

function fail(msg) {
  process.stderr.write(`ghostchrome launcher: ${msg}\n`);
  process.exit(1);
}

if (!OS || !ARCH) fail(`no prebuilt binary for ${process.platform}-${process.arch}`);
const asset = `ghostchrome-${OS}-${ARCH}${OS === "windows" ? ".exe" : ""}`;
const cacheRoot = join(process.env.GHOSTCHROME_HOME || join(homedir(), ".ghostchrome"), "releases");

function packageVersion() {
  try {
    const pkg = JSON.parse(readFileSync(join(dirname(fileURLToPath(import.meta.url)), "..", "..", "package.json"), "utf8"));
    return pkg.version && pkg.version !== "0.0.0" ? `v${pkg.version}` : "";
  } catch {
    return "";
  }
}

async function latestVersion() {
  // The releases/latest page redirects to /releases/tag/<tag>; unlike the REST
  // API it has no anonymous rate limit.
  try {
    const res = await fetch(`https://github.com/${REPO}/releases/latest`, {
      method: "HEAD",
      redirect: "manual",
      signal: AbortSignal.timeout(5000),
    });
    const tag = (res.headers.get("location") || "").split("/tag/")[1];
    if (tag) return decodeURIComponent(tag);
  } catch {
    // Offline: fall back to the newest cached release below.
  }
  const cached = existsSync(cacheRoot)
    ? readdirSync(cacheRoot).filter((v) => existsSync(join(cacheRoot, v, asset)))
    : [];
  cached.sort((a, b) => a.localeCompare(b, undefined, { numeric: true }));
  if (cached.length) return cached[cached.length - 1];
  fail("cannot resolve the latest release (offline?); set GHOSTCHROME_VERSION=vX.Y.Z");
}

async function download(url) {
  const res = await fetch(url, { signal: AbortSignal.timeout(120000) });
  if (!res.ok) fail(`download failed: ${url} (HTTP ${res.status})`);
  return Buffer.from(await res.arrayBuffer());
}

async function ensureBinary(version) {
  const dir = join(cacheRoot, version);
  const bin = join(dir, asset);
  if (existsSync(bin)) return bin;

  const base = `https://github.com/${REPO}/releases/download/${version}`;
  process.stderr.write(`ghostchrome launcher: downloading ${asset} ${version}\n`);
  const sums = (await download(`${base}/checksums.txt`)).toString("utf8");
  const expected = sums
    .split("\n")
    .map((line) => line.trim().split(/\s+/))
    .find(([, name]) => name === asset)?.[0];
  if (!expected) fail(`checksums.txt of ${version} has no entry for ${asset}`);
  const data = await download(`${base}/${asset}`);
  const actual = createHash("sha256").update(data).digest("hex");
  if (actual !== expected) fail(`checksum mismatch for ${asset} ${version}`);

  // Write then rename, so concurrent first runs never execute a partial file.
  mkdirSync(dir, { recursive: true });
  const tmp = `${bin}.${process.pid}.tmp`;
  writeFileSync(tmp, data);
  chmodSync(tmp, 0o755);
  try {
    renameSync(tmp, bin);
  } catch (err) {
    rmSync(tmp, { force: true });
    if (!existsSync(bin)) throw err;
  }
  return bin;
}

const version = process.env.GHOSTCHROME_VERSION || packageVersion() || (await latestVersion());
const bin = await ensureBinary(version);

const child = spawn(bin, process.argv.slice(2), { stdio: "inherit" });
for (const sig of ["SIGINT", "SIGTERM", "SIGHUP"]) {
  process.on(sig, () => child.kill(sig));
}
child.on("error", (err) => fail(`failed to run ${bin}: ${err.message}`));
child.on("exit", (code, signal) => {
  process.exit(signal ? 128 + (constants.signals[signal] ?? 1) : (code ?? 1));
});
