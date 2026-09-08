#!/usr/bin/env python3
"""E2E example: drive ghostchrome from the Python SDK against a live site.

Attaches to an already-running Chrome via --connect=auto (project policy).

    GHOSTCHROME_BIN="$PWD/ghostchrome" python3 sdk/examples/python/chronovet.py [url]
"""
import os
import sys

# Make the in-repo SDK importable without installing it.
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "..", "python"))

from ghostchrome import Ghostchrome  # noqa: E402

url = sys.argv[1] if len(sys.argv) > 1 else "https://www.chronovet.fr/"
binary = os.environ.get("GHOSTCHROME_BIN", "ghostchrome")
connect = os.environ.get("GHOSTCHROME_CONNECT", "--connect=auto")

with Ghostchrome(command=binary, extra_flags=[connect, "--stealth"]) as gc:
    nav, _ = gc.navigate(url)
    print(f"[{nav.status}] {nav.title}")
    print(f"url: {nav.url}")

    tree, _ = gc.extract(level="skeleton")
    refs = tree.refs or {}
    print(f"refs: {len(refs)}")
    for ref, node in list(refs.items())[:10]:
        role = node.get("role", "")
        name = node.get("name", "")
        print(f"  {ref} {role} {name}".rstrip())
