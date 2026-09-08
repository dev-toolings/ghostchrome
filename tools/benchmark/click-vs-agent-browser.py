#!/usr/bin/env python3
"""Warm click microbench: ghostchrome JSONL vs agent-browser on a shared CDP Chrome."""
import json, os, statistics, subprocess, sys, time, urllib.request
from pathlib import Path

N = int(sys.argv[1]) if len(sys.argv) > 1 else 30
PORT = int(os.environ.get("PORT", "9333"))
FIX = int(os.environ.get("FIX", "8765"))
URL = f"http://127.0.0.1:{FIX}/index.html"
BIN = os.environ.get("GHOSTCHROME_BIN", "/tmp/ghostchrome-benchbin")
AB = os.environ.get("AGENT_BROWSER_BIN", "agent-browser")

PW = os.environ.get("PLAYWRIGHT_CLI_BIN", "")
PW_CFG = os.environ.get("PLAYWRIGHT_CLI_CONFIG", "")


def percentile(xs, p):
    if not xs:
        return 0.0
    ys = sorted(xs)
    k = (len(ys) - 1) * p / 100.0
    f = int(k)
    c = min(f + 1, len(ys) - 1)
    if f == c:
        return ys[f]
    return ys[f] + (ys[c] - ys[f]) * (k - f)


def cdp_ws():
    with urllib.request.urlopen(f"http://127.0.0.1:{PORT}/json/version", timeout=2) as r:
        return json.load(r)["webSocketDebuggerUrl"]


def summarize(name, samples):
    return {
        "name": name,
        "n": len(samples),
        "mean": statistics.fmean(samples),
        "p50": percentile(samples, 50),
        "p95": percentile(samples, 95),
        "max": max(samples),
    }


def fmt(row):
    return f"{row['name']:28} n={row['n']:3}  mean={row['mean']:6.1f}  p50={row['p50']:6.1f}  p95={row['p95']:6.1f}  max={row['max']:6.1f}"


def bench_ghostchrome(ws, n):
    env = os.environ.copy()
    env["GHOSTCHROME_NO_DAEMON"] = "1"
    proc = subprocess.Popen(
        [BIN, "agent", f"--connect={ws}"],
        stdin=subprocess.PIPE,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        env=env,
    )
    assert proc.stdin and proc.stdout

    def rpc(op, args=None):
        req = {"id": op + str(time.time_ns()), "op": op}
        if args:
            req["args"] = args
        proc.stdin.write(json.dumps(req) + "\n")
        proc.stdin.flush()
        line = proc.stdout.readline()
        if not line:
            err = proc.stderr.read() if proc.stderr else ""
            raise RuntimeError(f"ghostchrome agent closed: {err}")
        resp = json.loads(line)
        if not resp.get("ok"):
            raise RuntimeError(f"{op}: {resp.get('error')}")
        return resp

    rpc("init")
    rpc("navigate", {"url": URL, "wait": "domcontentloaded"})
    rpc("extract", {"level": "skeleton"})
    samples = []
    for _ in range(n):
        t0 = time.perf_counter()
        rpc("click", {"ref": "@1", "snapshot": "none"})
        samples.append((time.perf_counter() - t0) * 1000)
    try:
        rpc("close")
    except Exception:
        pass
    proc.terminate()
    try:
        proc.wait(timeout=2)
    except subprocess.TimeoutExpired:
        proc.kill()
    return samples


def bench_agent_browser(n):
    subprocess.check_call([AB, "--cdp", str(PORT), "open", URL], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    subprocess.check_call([AB, "--cdp", str(PORT), "snapshot", "-i"], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    samples = []
    for _ in range(n):
        t0 = time.perf_counter()
        subprocess.check_call([AB, "--cdp", str(PORT), "click", "@e1"], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        samples.append((time.perf_counter() - t0) * 1000)
    return samples


def pw_cmd(*args):
    cmd = [PW] if PW else ["bunx", "--bun", "@playwright/cli"]
    if PW_CFG and args and args[0] == "open":
        cmd += ["--config", PW_CFG]
    cmd += list(args)
    env = os.environ.copy()
    lib = os.environ.get("LD_LIBRARY_PATH", "")
    extra = "/tmp/gc-libcups/extracted/usr/lib/x86_64-linux-gnu"
    env["LD_LIBRARY_PATH"] = extra + (":" + lib if lib else "")
    subprocess.run(cmd, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, env=env, check=True, timeout=20)


def bench_playwright_cli(n):
    pw_cmd("open", URL)
    pw_cmd("snapshot")
    samples = []
    for _ in range(n):
        t0 = time.perf_counter()
        pw_cmd("click", "e2")
        samples.append((time.perf_counter() - t0) * 1000)
    try:
        pw_cmd("close")
    except Exception:
        pass
    return samples


def main():
    ws = cdp_ws()
    print(f"cdp={ws} fixture={URL} n={N}", flush=True)
    ab = bench_agent_browser(N)
    gc = bench_ghostchrome(ws, N)
    rows = [summarize("agent-browser", ab), summarize("ghostchrome JSONL none", gc)]
    try:
        pw = bench_playwright_cli(N)
        rows.append(summarize("playwright-cli", pw))
    except Exception as err:
        print(f"playwright-cli skipped: {err}")
    for row in rows:
        print(fmt(row))
    baseline = rows[0]["p95"] * 1.10
    print(f"gate p95 <= {baseline:.1f} ms: {'PASS' if rows[1]['p95'] <= baseline else 'FAIL'}")


if __name__ == "__main__":
    main()
