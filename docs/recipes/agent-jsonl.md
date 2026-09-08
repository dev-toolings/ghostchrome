# Recipe — driving ghostchrome from an LLM (JSONL agent loop)

The `agent` subcommand turns ghostchrome into a long-lived process that reads JSONL ops on stdin and writes JSONL responses on stdout. The LLM doesn't need to learn flag grammar — just emit op dicts.

## Wire protocol

```
stdin →  {"id":"r1","op":"navigate","args":{"url":"https://example.com"}}
stdout ← {"id":"r1","ok":true,"result":{"status":200,"url":"...","title":"..."},"observation":{...}}

stdin →  {"id":"r2","op":"extract","args":{"level":"skeleton"}}
stdout ← {"id":"r2","ok":true,"result":{"refs":{"@1":{...}},"nodes":[...]},"observation":{...}}

stdin →  {"id":"r3","op":"click","args":{"ref":"@1"}}
stdout ← {"id":"r3","ok":true,"result":{},"observation":{"a11y_diff":"...","url":"..."}}

stdin →  {"id":"r4","op":"close"}
stdout ← {"id":"r4","ok":true}
```

One JSON object per line, both directions. No prompts, no preamble.

## Supported ops

| Op | Args | Returns |
|---|---|---|
| `init` | none | session metadata |
| `navigate` | `{url, wait?}` | `{status, url, title}` |
| `back` / `forward` | none | navigation result |
| `extract` | `{level?, selector?}` | `{nodes, refs, stats}` |
| `click` / `hover` | `{ref}` or `{by_role, by_name, ...}` | `{}` |
| `type` | `{ref, text, clear?}` | `{}` |
| `press` | `{key, on?}` | `{}` |
| `select` | `{ref, value}` | `{}` |
| `fill` | `{form: [{ref, value}, ...]}` | `{}` |
| `scroll_by` | `{dx?, dy?}` | `{}` |
| `scroll_to` | `{ref}` | `{}` |
| `eval` | `{expr, on?}` | `{value}` |
| `screenshot` | `{full?, ref?, output?}` | `{path or base64}` |
| `wait` | `{strategy: load|stable|idle|none, ms?}` | `{}` |
| `errors` | none | `{errors: [...]}` |
| `url` | none | `{url}` |
| `close` | none | `{}` then exit |

## Observation packet

Every response includes an `observation` field describing what changed during the op:

```json
{
  "url": "https://app.com/dashboard",
  "console_errors": [
    {"level":"error","text":"TypeError: Cannot read properties of undefined","source":"app.js:142:11"}
  ],
  "network_failed": [
    {"url":"https://api.x.com/me","status":401,"failed":""}
  ],
  "a11y_diff": "added 3 nodes (1 button, 2 links), removed 1 (form), changed 0",
  "dialog": "Are you sure you want to delete this item?",
  "captcha_hint": "DataDome interstitial detected"
}
```

Empty fields are omitted. With `--observe`, an `events: []` list of raw CDP events captured during the op is also embedded.

## Recovery hooks

When an op fails with a recoverable error, the loop runs the hook chain:

| Hook | Trigger | Action |
|---|---|---|
| `RecoverStaleRef` | `ErrStaleRef` | returns explicit error — caller must re-extract (no silent remap) |
| `RecoverDialogAccept` | dialog open detected during op | accept dialog, retry op once |
| `RecoverBotChallenge` | DataDome 403 / Cloudflare 503 | wait for challenge resolution (10 s), retry once |
| `RecoverNetworkSettle` | timeout-class errors | wait stable 2 s, retry once |

Add custom hooks programmatically by extending `agentSession.recoveryHooks`.

## Minimal driver (Python)

```python
import json
import subprocess

proc = subprocess.Popen(
    ["ghostchrome", "agent", "--stealth", "--observe"],
    stdin=subprocess.PIPE, stdout=subprocess.PIPE,
    text=True, bufsize=1,
)

def call(op, args=None, rid=None):
    rid = rid or f"r{id(args or op)}"
    proc.stdin.write(json.dumps({"id": rid, "op": op, "args": args or {}}) + "\n")
    proc.stdin.flush()
    return json.loads(proc.stdout.readline())

# Workflow
print(call("navigate", {"url": "https://example.com"}))
snap = call("extract", {"level": "skeleton"})
target = next(r for r, n in snap["result"]["refs"].items() if n.get("name") == "Learn more")
print(call("click", {"ref": target}))
print(call("url"))
call("close")
```

## Minimal driver (Bash + jq)

```bash
mkfifo /tmp/agent_in
ghostchrome agent --observe < /tmp/agent_in &
exec 3> /tmp/agent_in

send() {
  echo "$1" >&3
}

send '{"id":"1","op":"navigate","args":{"url":"https://example.com"}}'
send '{"id":"2","op":"extract","args":{"level":"skeleton"}}'
send '{"id":"3","op":"close"}'

# Read responses on stdout (separate consumer). For a one-shot pipeline:
echo '{"id":"1","op":"navigate","args":{"url":"https://example.com"}}
{"id":"2","op":"extract","args":{"level":"skeleton"}}
{"id":"3","op":"close"}' | ghostchrome agent | jq -c '{id, ok, has_obs:(.observation!=null)}'
```

## Driving from Claude Code / Cursor

The flow is similar to any LLM-driven loop:

1. The LLM reads the system prompt: "You drive ghostchrome via JSONL. Emit one op per turn. Read observation to plan the next."
2. Send the LLM the snapshot returned by `extract`.
3. The LLM responds with a single op. The harness writes it to stdin.
4. Read stdout, append observation to the LLM's context.
5. Loop until the LLM emits `close` or the task is done.

## Tips

- **Always `extract` before `click` / `type`** — refs come from the latest snapshot. After a navigation, refs are invalid; re-extract.
- **Use observation, not full extraction, for feedback** — `a11y_diff` is much smaller than a full extract response.
- **`--observe` is cheap** — a few KB per op. Worth it.
- **Set `--timeout` carefully** — agents that retry on every failed op without a timeout will hang on dead pages. Default 30 s is fine for most.
- **`close` cleanly** — otherwise Chrome (when auto-launched) is left running until the parent exits.

## Combining with fast-path

For data-extraction work, **don't** drive the agent loop — use `fastfetch` / `fetchapi` directly. The agent loop is for **interactive** workflows: form-filling, multi-step navigation, post-login pages, captcha-prone flows.

A typical end-to-end pipeline:

```
LLM plans  →  ghostchrome fastfetch (cheap probe to find URL of interest)
           →  ghostchrome agent      (interact with that page, navigate, fill form)
           →  ghostchrome fastfetch  (re-fetch the resulting page after action for verification)
```

This minimizes Chrome time to just the steps that need a real browser.
