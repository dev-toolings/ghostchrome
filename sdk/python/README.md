# ghostchrome (Python SDK)

Python SDK for [ghostchrome](../../README.md) browser automation.

Typed client for the ghostchrome JSONL agent protocol. Drives a persistent
`ghostchrome agent` subprocess over stdin/stdout — no HTTP, no RPC overhead.

## Requirements

- Python 3.10+
- `ghostchrome` binary in `$PATH` (or injected via the `command` kwarg)
- No third-party runtime dependencies (stdlib only)

## Installation

```bash
pip install ghostchrome
# or from source:
pip install -e sdk/python/
```

## Quick start

```python
from ghostchrome import Ghostchrome

# Attach to an already-running Chrome on :9222 (preferred mode)
with Ghostchrome(extra_flags=["--connect=auto"]) as gc:
    # Navigate — returns (NavigateResult, Observation)
    nav, obs = gc.navigate("https://example.com")
    print(nav.status)   # 200
    print(nav.title)    # "Example Domain"
    print(nav.time_ms)  # e.g. 42

    # Accessibility tree with @refs
    tree, _ = gc.extract(level="skeleton")
    print(tree.stats.total_nodes)       # e.g. 15
    print(tree.stats.interactive_count) # e.g. 1
    print(list(tree.refs.keys()))       # ["@1", ...]

    # Interact — omitted-result ops return (None, observation)
    gc.click("@1")
    gc.type_("@2", "hello world")
    gc.press("Enter")

    # Fill multiple fields at once
    fill_result, _ = gc.fill({"@1": "Alice", "@2": "pass"})
    print(fill_result.filled)  # 2

    # Scroll
    scroll_result, _ = gc.scroll_by(300)
    print(scroll_result.y)  # new Y position

    # Evaluate JavaScript
    val, _ = gc.eval_("document.title")
    print(val.value)  # "Example Domain"

    # Screenshot — always base64+mime over the wire
    shot, _ = gc.screenshot(full_page=True)
    print(shot.mime)   # "image/png"
    print(shot.base64) # base64 data

    # Current URL
    url, _ = gc.url()
    print(url.url, url.title)

    # Console and network errors — returns list[ErrorEntry], not a wrapper
    errors = gc.errors()
    for e in errors:
        print(e.type, e.level, e.message)
```

## Result shapes (ground truth)

All shapes are measured from the live binary — never guessed.

| Method | Result type | Key fields |
|---|---|---|
| `navigate(url, *, wait?)` | `NavigateResult` | `url`, `title`, `status`, `time_ms` |
| `back()` | `BackForwardResult` | `url?`, `title?` |
| `forward()` | `BackForwardResult` | `url?`, `title?` |
| `extract(*, level?, selector?)` | `ExtractResult` | `nodes`, `refs`, `stats` (→ `ExtractStats`) |
| `click(ref)` | `None` | — |
| `hover(ref)` | `None` | — |
| `type_(ref, text)` | `None` | — |
| `press(key, *, ref?)` | `None` | — |
| `select(ref, values)` | `None` | — |
| `fill(fields)` | `FillResult` | `filled: int` |
| `scroll_by(dy)` | `ScrollResult` | `y: int` |
| `scroll_to(*, y?, bottom?)` | `ScrollResult` | `y: int` |
| `eval_(expr, *, ref?)` | `EvalResult` | `value: str` |
| `screenshot(*, full_page?, ref?, quality?)` | `ScreenshotResult` | `mime`, `base64` |
| `wait(*, selector?, ms?)` | `None` | — |
| `errors()` | `list[ErrorEntry]` | `type`, `level`, `message`, `source`, `time_ms`, `status?`, `method?` |
| `url()` | `UrlResult` | `url`, `title` |
| `init()` | `InitResult` | — |
| `close()` | `None` | — |

`ExtractStats` fields: `total_nodes`, `filtered_nodes`, `interactive_count`.

## Error handling

When `ok=false`, the client raises `GhostchromeError` (a `RuntimeError` subclass):

```python
from ghostchrome import Ghostchrome, GhostchromeError

with Ghostchrome(extra_flags=["--connect=auto"]) as gc:
    gc.navigate("https://example.com")
    try:
        gc.click("@99")
    except GhostchromeError as e:
        print(e.op)      # "click"
        print(e.message) # "ref @99 not found"
```

## Advanced usage

### Custom binary path / flags

```python
gc = Ghostchrome(
    command="/usr/local/bin/ghostchrome",
    extra_flags=["--stealth", "--observe"],
    timeout=60.0,
)
result, _ = gc.navigate("https://app.example.com")
gc.close()
```

### Inject your own transport

```python
from ghostchrome import Ghostchrome
from ghostchrome.transport import SubprocessTransport

transport = SubprocessTransport(
    command="ghostchrome",
    args=["agent"],
    extra_flags=["--connect=auto"],
)
gc = Ghostchrome(transport=transport)
```

## Running tests

```bash
cd sdk/python
python3 -m unittest discover -s tests -q
```

Tests are hermetic — no real browser or ghostchrome binary needed.

## Wire protocol

The SDK implements the JSONL agent protocol documented in
[`docs/recipes/agent-jsonl.md`](../../docs/recipes/agent-jsonl.md).

Each op is a JSON line on stdin:
```json
{"id":"r1","op":"navigate","args":{"url":"https://example.com"}}
```

Each response is a JSON line on stdout:
```json
{"id":"r1","ok":true,"result":{"url":"https://example.com/","title":"Example Domain","status":200,"time_ms":42},"observation":{"url":"https://example.com/"}}
```

The `errors` op returns a JSON array directly (not a dict):
```json
{"id":"r2","ok":true,"result":[{"type":"console","level":"error","message":"...","source":"app.js:1","time_ms":10}],"observation":{...}}
```

Ops that produce no output omit `result` entirely (`init`, `close`, `click`, `hover`,
`type`, `press`, `select`, `wait`).


## Vehicle lookup integration

The [iautos-agent integration guide](../../docs/iautos-agent-sdk.md) explains the
persistent Python client, the Go binary requirement, session isolation and the
hybrid browser/HTTP lookup boundary. A bounded, process-local cache example with
TTL and concurrent-request coalescing lives at
[`sdk/examples/python/vehicle_cache.py`](../examples/python/vehicle_cache.py).
It wraps an application-supplied validated lookup callable; it is not a built-in
vehicle lookup SDK method.
