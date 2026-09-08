#!/usr/bin/env node
// Thin launcher: resolve the platform-specific prebuilt ghostchrome binary
// (shipped as an optional dependency, gated by os/cpu) and exec it. No
// download, no postinstall — works under both node and bun.
import { spawnSync } from "node:child_process";
import { createRequire } from "node:module";

const require = createRequire(import.meta.url);

const platform = process.platform; // "linux" | "darwin" | "win32"
const arch = process.arch; // "x64" | "arm64"
const pkg = `@ghostchrome/cli-${platform}-${arch}`;
const binName = platform === "win32" ? "ghostchrome.exe" : "ghostchrome";

let binPath;
try {
  binPath = require.resolve(`${pkg}/bin/${binName}`);
} catch {
  console.error(
    `ghostchrome: no prebuilt binary for ${platform}-${arch} (missing ${pkg}).\n` +
      `Supported: linux-x64, linux-arm64, darwin-x64, darwin-arm64, win32-x64.\n` +
      `Install another way: https://github.com/dev-toolings/ghostchrome#install`,
  );
  process.exit(1);
}

const res = spawnSync(binPath, process.argv.slice(2), { stdio: "inherit" });
if (res.error) {
  console.error(`ghostchrome: failed to run binary: ${res.error.message}`);
  process.exit(1);
}
process.exit(res.status ?? 1);
