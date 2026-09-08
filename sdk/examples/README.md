# examples

Runnable end-to-end examples driving the local `ghostchrome` binary through the
TypeScript and Python SDKs.

Each example **attaches to an already-running Chrome** via `--connect=auto`
(the project's preferred runtime policy: never cold-spawn). Start one first:

```bash
google-chrome --headless=new --remote-debugging-port=9222 \
  --user-data-dir=/tmp/gc-profile about:blank &
```

Build the binary from the repo root:

```bash
go build -o .local/bin/ghostchrome .
```

## TypeScript

```bash
GHOSTCHROME_BIN="$PWD/.local/bin/ghostchrome" \
  bun run sdk/examples/typescript/chronovet.ts
# optional: pass a different URL
GHOSTCHROME_BIN="$PWD/.local/bin/ghostchrome" \
  bun run sdk/examples/typescript/chronovet.ts https://example.com
```

## Python

```bash
GHOSTCHROME_BIN="$PWD/.local/bin/ghostchrome" \
  python3 sdk/examples/python/chronovet.py
GHOSTCHROME_BIN="$PWD/.local/bin/ghostchrome" \
  python3 sdk/examples/python/chronovet.py https://example.com
```

## Environment variables

| Var | Default | Meaning |
|---|---|---|
| `GHOSTCHROME_BIN` | `ghostchrome` | Path to the binary the SDK spawns as the JSONL agent |
| `GHOSTCHROME_CONNECT` | `--connect=auto` | Connection flag forwarded to `ghostchrome agent` |

Both examples navigate to `https://www.chronovet.fr/` by default, extract a
skeleton accessibility tree, and print the first interactive `@refs`.
