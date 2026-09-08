# npm distribution — `@ghostchrome/cli`

bun/npm-installable wrapper around the prebuilt ghostchrome Go binary, using the
per-platform `optionalDependencies` pattern (esbuild/Biome-style) so there is
**no postinstall** — robust under bun, which blocks dependency postinstall
scripts by default.

```
cli/                  → @ghostchrome/cli         (the package users install)
  bin/cli.mjs         launcher: resolves the platform pkg and execs its binary
cli-linux-x64/        → @ghostchrome/cli-linux-x64   (os/cpu-gated, ships 1 binary)
cli-linux-arm64/      → @ghostchrome/cli-linux-arm64
cli-darwin-x64/       → @ghostchrome/cli-darwin-x64
cli-darwin-arm64/     → @ghostchrome/cli-darwin-arm64
cli-win32-x64/        → @ghostchrome/cli-win32-x64
```

Install (end users):

```bash
bun install -g @ghostchrome/cli      # or: bunx @ghostchrome/cli <cmd>
# npm install -g @ghostchrome/cli    # also works
```

bun/npm resolve only the one platform package matching the host `os`+`cpu`, then
`bin/cli.mjs` execs `@ghostchrome/cli-<platform>-<arch>/bin/ghostchrome`.

## Publishing

Binaries are **not committed** — `.github/workflows/release.yml` cross-compiles
the Go binaries on a `v*` tag, copies each into the matching `cli-*/bin/`, stamps
every `package.json` with the tag version, and runs `bun publish --access public`
for the 6 packages. Requires the `NPM_TOKEN` repository secret.

## Local validation (no publish)

```bash
# from repo root, with a freshly built ./ghostchrome
mkdir -p dist/npm/cli-linux-x64/bin
cp ghostchrome dist/npm/cli-linux-x64/bin/ghostchrome
cd dist/npm/cli
mkdir -p node_modules/@ghostchrome
ln -s ../../../cli-linux-x64 node_modules/@ghostchrome/cli-linux-x64
bun bin/cli.mjs --version     # should print the binary's version
rm -rf node_modules
```
