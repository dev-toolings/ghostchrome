#!/usr/bin/env python3
"""Latency of ghostchrome per Chrome connection mode.

Each mode runs the same agent loop against tools/benchmark/fixtures/product.html:
navigate + snapshot, eval, click (a non-navigating button). The CLI loop spawns
one process per step, like a shell-driven agent; the MCP loop sends the same
steps to one persistent stdio server. Reported values are medians and p95 over
--runs measured loops, after --warmup discarded loops.

Modes (pick with --modes, comma-separated):
  cli-daemon     implicit background Chrome (the default runtime)
  cli-cold       GHOSTCHROME_NO_DAEMON=1, a Chrome per command
  cli-connect    a Chrome started by this script, --connect http://127.0.0.1:PORT
  cli-attach     the same Chrome registered once with `attach --cdp`
  mcp-local      `ghostchrome mcp`, Chrome owned by the server
  mcp-connect    `ghostchrome mcp --connect http://127.0.0.1:PORT`
  docker-cdp     host CLI, --connect to a container in `cdp` mode (needs --image)
  docker-cdp-hostnet  same, but --network host and Chrome's own 9222: no
                 docker-proxy or socat hop (port 9222 must be free)
  docker-exec    CLI inside a running container via `docker exec` (needs --image)
  docker-mcp     `docker run -i <image> mcp` (needs --image)
  launcher       `--version` overhead: binary vs sdk/launcher vs bunx github:

Everything runs under a temporary HOME, so the user's ~/.ghostchrome default
session is never touched. Stdlib only.
"""
import argparse
import json
import os
import re
import shutil
import signal
import socket
import statistics
import subprocess
import sys
import tempfile
import threading
import time
import urllib.request
from functools import partial
from http.server import SimpleHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
FIXTURES = ROOT / "tools" / "benchmark" / "fixtures"
EVAL = "document.querySelector('#qty').value"
BUTTON = "Add to wishlist"


def free_port():
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def serve_fixtures(bind):
    handler = partial(QuietHandler, directory=str(FIXTURES))
    srv = ThreadingHTTPServer((bind, 0), handler)
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    return srv


class QuietHandler(SimpleHTTPRequestHandler):
    def log_message(self, *args):
        pass


def wait_http(url, timeout=30):
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            with urllib.request.urlopen(url, timeout=1):
                return
        except OSError:
            time.sleep(0.2)
    raise RuntimeError(f"{url} did not answer in {timeout}s")


def timed(cmd, env, check_out=None):
    t0 = time.perf_counter()
    res = subprocess.run(cmd, env=env, capture_output=True, text=True, timeout=120)
    ms = (time.perf_counter() - t0) * 1000
    if res.returncode != 0 or (check_out and check_out not in res.stdout):
        raise RuntimeError(f"{' '.join(cmd)} failed ({res.returncode}): {res.stdout[-300:]} {res.stderr[-300:]}")
    return ms


def summarize(samples):
    xs = sorted(samples)
    p95 = xs[min(len(xs) - 1, round(0.95 * (len(xs) - 1)))]
    return {"median": statistics.median(xs), "p95": p95, "n": len(xs)}


class Bench:
    def __init__(self, args):
        self.args = args
        self.bin = str(Path(args.bin).resolve())
        self.tmp = Path(tempfile.mkdtemp(prefix="gc-cdp-bench-"))
        self.env = dict(os.environ, HOME=str(self.tmp / "home"))
        Path(self.env["HOME"]).mkdir()
        rod = Path.home() / ".cache" / "rod"
        if rod.exists():  # reuse Rod's downloaded Chromium if any
            (Path(self.env["HOME"]) / ".cache").mkdir()
            (Path(self.env["HOME"]) / ".cache" / "rod").symlink_to(rod)
        self.srv = serve_fixtures("0.0.0.0" if args.image else "127.0.0.1")
        self.port = self.srv.server_address[1]
        self.url = f"http://127.0.0.1:{self.port}/product.html"
        self.procs = []
        self.containers = []

    # ----- CLI loop -------------------------------------------------------
    def cli_loop(self, prefix, env, url=None):
        url = url or self.url
        # Without a daemon every command starts a blank Chrome, so each step
        # must navigate again: that cost is what cli-cold measures.
        again = [url] if env.get("GHOSTCHROME_NO_DAEMON") == "1" else []
        return {
            "navigate+snapshot": timed(prefix + ["preview", url], env, "Add to wishlist"),
            "eval": timed(prefix + ["eval", EVAL] + again, env, "1"),
            "click": timed(prefix + ["click", "--by-role", "button", "--by-name", BUTTON] + again, env),
        }

    def run_cli(self, name, prefix, env, url=None, setup=None):
        if setup:
            setup()
        first = self.cli_loop(prefix, env, url)  # includes the cold start
        for _ in range(self.args.warmup):
            self.cli_loop(prefix, env, url)
        loops = [self.cli_loop(prefix, env, url) for _ in range(self.args.runs)]
        return self.report(name, first, loops)

    # ----- MCP loop -------------------------------------------------------
    def run_mcp(self, name, cmd, env, url=None):
        url = url or self.url
        proc = subprocess.Popen(cmd, env=env, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                stderr=subprocess.DEVNULL, text=True, bufsize=1)
        self.procs.append(proc)
        ids = iter(range(1, 10**6))

        def call(method, params, notify=False):
            msg = {"jsonrpc": "2.0", "method": method, "params": params}
            if not notify:
                msg["id"] = next(ids)
            t0 = time.perf_counter()
            proc.stdin.write(json.dumps(msg) + "\n")
            proc.stdin.flush()
            if notify:
                return None, 0
            while True:
                line = proc.stdout.readline()
                if not line:
                    raise RuntimeError(f"{name}: server closed stdout")
                resp = json.loads(line)
                if resp.get("id") == msg["id"]:
                    ms = (time.perf_counter() - t0) * 1000
                    if "error" in resp or resp.get("result", {}).get("isError"):
                        raise RuntimeError(f"{name}: {method} failed: {line[:300]}")
                    return resp, ms

        def tool(tname, targs):
            resp, ms = call("tools/call", {"name": tname, "arguments": targs})
            text = "".join(c.get("text", "") for c in resp["result"].get("content", []))
            return text, ms

        t0 = time.perf_counter()
        call("initialize", {"protocolVersion": "2025-06-18", "capabilities": {},
                            "clientInfo": {"name": "cdp-bench", "version": "1"}})
        call("notifications/initialized", {}, notify=True)
        init_ms = (time.perf_counter() - t0) * 1000

        def loop():
            _, nav = tool("navigate", {"url": url})
            snap, snap_ms = tool("snapshot", {})
            ref = re.search(r'"(@?\d+)"[^\n]{0,200}' + BUTTON, snap) or re.search(r"(@\d+)[^@\n]*" + BUTTON, snap)
            if not ref:
                raise RuntimeError(f"{name}: no ref for {BUTTON!r} in snapshot")
            _, ev = tool("eval", {"expression": EVAL})
            _, cl = tool("click", {"ref": ref.group(1).lstrip("@")})
            return {"navigate+snapshot": nav + snap_ms, "eval": ev, "click": cl}

        first = loop()
        first["navigate+snapshot"] += init_ms
        for _ in range(self.args.warmup):
            loop()
        loops = [loop() for _ in range(self.args.runs)]
        proc.stdin.close()
        proc.wait(timeout=30)
        return self.report(name, first, loops)

    # ----- helpers --------------------------------------------------------
    def report(self, name, first, loops):
        steps = {k: summarize([l[k] for l in loops]) for k in first}
        total = summarize([sum(l.values()) for l in loops])
        return {"mode": name, "first_loop_ms": sum(first.values()), "loop": total, "steps": steps}

    def chrome(self):
        if hasattr(self, "cdp_url"):
            return self.cdp_url
        exe = self.args.chrome or shutil.which("google-chrome") or shutil.which("chromium")
        if not exe:
            raise RuntimeError("no Chrome found; pass --chrome")
        port = free_port()
        proc = subprocess.Popen([exe, "--headless=new", "--no-sandbox", f"--remote-debugging-port={port}",
                                 f"--user-data-dir={self.tmp / 'chrome-profile'}", "--no-first-run",
                                 "about:blank"], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        self.procs.append(proc)
        self.cdp_url = f"http://127.0.0.1:{port}"
        wait_http(self.cdp_url + "/json/version")
        return self.cdp_url

    def container(self, *args):
        name = f"gc-bench-{os.getpid()}-{len(self.containers)}"
        subprocess.run(["docker", "run", "-d", "--name", name, "--shm-size=1g",
                        "--add-host=host.docker.internal:host-gateway", *args],
                       check=True, capture_output=True)
        self.containers.append(name)
        return name

    def docker_url(self):
        return f"http://host.docker.internal:{self.port}/product.html"

    def close(self):
        for p in self.procs:
            if p.poll() is None:
                p.send_signal(signal.SIGTERM)
                try:
                    p.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    p.kill()
        for c in self.containers:
            subprocess.run(["docker", "rm", "-f", c], capture_output=True)
        subprocess.run([self.bin, "sessions", "stop", "default"], env=self.env, capture_output=True)
        self.srv.shutdown()
        shutil.rmtree(self.tmp, ignore_errors=True)

    # ----- modes ----------------------------------------------------------
    def mode(self, name):
        b = [self.bin]
        if name == "cli-daemon":
            return self.run_cli(name, b, self.env)
        if name == "cli-cold":
            return self.run_cli(name, b, dict(self.env, GHOSTCHROME_NO_DAEMON="1"))
        if name == "cli-connect":
            return self.run_cli(name, b + ["--connect", self.chrome()], self.env)
        if name == "cli-attach":
            env = dict(self.env, HOME=str(self.tmp / "home-attach"))
            Path(env["HOME"]).mkdir(exist_ok=True)
            return self.run_cli(name, b + ["-s", "bench"], env,
                                setup=lambda: timed(b + ["-s", "bench", "attach", "--cdp=" + self.chrome()], env))
        if name == "mcp-local":
            return self.run_mcp(name, b + ["mcp"], self.env)
        if name == "mcp-connect":
            return self.run_mcp(name, b + ["mcp", "--connect", self.chrome()], self.env)
        image = self.args.image
        if not image:
            raise RuntimeError(f"{name} needs --image")
        if name == "docker-cdp":
            port = free_port()
            self.container("-p", f"127.0.0.1:{port}:9223", image, "cdp")
            wait_http(f"http://127.0.0.1:{port}/json/version")
            return self.run_cli(name, b + ["--connect", f"http://127.0.0.1:{port}"], self.env, self.docker_url())
        if name == "docker-cdp-hostnet":
            name_ = f"gc-bench-{os.getpid()}-{len(self.containers)}"
            subprocess.run(["docker", "run", "-d", "--name", name_, "--shm-size=1g", "--network", "host",
                            "-e", "CDP_RELAY=0", image, "cdp"], check=True, capture_output=True)
            self.containers.append(name_)
            wait_http("http://127.0.0.1:9222/json/version")
            return self.run_cli(name, b + ["--connect", "http://127.0.0.1:9222"], self.env)
        if name == "docker-exec":
            c = self.container("--entrypoint", "sleep", image, "infinity")
            return self.run_cli(name, ["docker", "exec", c, "ghostchrome"], self.env, self.docker_url())
        if name == "docker-mcp":
            return self.run_mcp(name, ["docker", "run", "-i", "--rm", "--shm-size=1g",
                                       "--add-host=host.docker.internal:host-gateway", image, "mcp"],
                                self.env, self.docker_url())
        raise RuntimeError(f"unknown mode {name}")

    def launcher(self):
        out = {}
        env = dict(self.env, GHOSTCHROME_HOME=str(self.tmp / "launcher-home"))
        cmds = {"binary": [self.bin, "--version"],
                "sdk/launcher (bun)": ["bun", str(ROOT / "sdk/launcher/ghostchrome.mjs"), "--version"]}
        if self.args.bunx_ref:
            cmds["bunx github:"] = ["bunx", f"github:dev-toolings/ghostchrome#{self.args.bunx_ref}", "--version"]
        for label, cmd in cmds.items():
            timed(cmd, env)  # first run downloads and caches
            out[label] = summarize([timed(cmd, env) for _ in range(self.args.runs)])
        return out


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--bin", required=True, help="ghostchrome binary to measure")
    ap.add_argument("--modes", default="cli-daemon,cli-connect,cli-attach,mcp-local,mcp-connect,launcher")
    ap.add_argument("--runs", type=int, default=15)
    ap.add_argument("--warmup", type=int, default=2)
    ap.add_argument("--chrome", help="Chrome executable for the attach modes")
    ap.add_argument("--image", help="Docker image for the docker-* modes")
    ap.add_argument("--bunx-ref", help="git ref for the bunx github: launcher timing")
    ap.add_argument("--json", help="also write raw results to this file")
    args = ap.parse_args()

    bench = Bench(args)
    results, launcher, failures = [], None, {}
    try:
        for m in args.modes.split(","):
            print(f"[bench] {m}", file=sys.stderr, flush=True)
            try:
                if m == "launcher":
                    launcher = bench.launcher()
                else:
                    results.append(bench.mode(m))
            except Exception as e:  # keep measuring the other modes
                failures[m] = str(e)
                print(f"[bench] {m} failed: {e}", file=sys.stderr)
    finally:
        bench.close()

    print(f"\nghostchrome CDP modes: {args.runs} loops after {args.warmup} warm-up, median / p95 in ms\n")
    print("| Mode | First loop | Loop median | Loop p95 | navigate+snapshot | eval | click |")
    print("|---|---:|---:|---:|---:|---:|---:|")
    for r in results:
        s = r["steps"]
        print(f"| {r['mode']} | {r['first_loop_ms']:.0f} | {r['loop']['median']:.0f} | {r['loop']['p95']:.0f} | "
              f"{s['navigate+snapshot']['median']:.0f} | {s['eval']['median']:.0f} | {s['click']['median']:.0f} |")
    if launcher:
        print("\n| Launcher (`--version`) | median | p95 |\n|---|---:|---:|")
        for k, v in launcher.items():
            print(f"| {k} | {v['median']:.0f} | {v['p95']:.0f} |")
    for m, e in failures.items():
        print(f"\nFAILED {m}: {e}")
    if args.json:
        Path(args.json).write_text(json.dumps({"results": results, "launcher": launcher, "failures": failures}, indent=2))
    sys.exit(1 if failures else 0)


if __name__ == "__main__":
    main()
