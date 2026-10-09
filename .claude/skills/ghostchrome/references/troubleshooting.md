# Troubleshooting and runtime hygiene

Read this reference when the selected transport fails. Preserve the selected
mode while diagnosing; a failure in CLI mode is not a reason to register MCP,
and an MCP registration failure is not a reason to invoke the CLI binary.

## First checks

Run the read-only diagnostics:

```bash
ghostchrome setup status
ghostchrome setup doctor --strict
```

Confirm all of the following:

- the manifest parses and names exactly one mode;
- the selected executable exists, is executable, and matches its recorded hash;
- Chrome or Chromium is discoverable;
- the selected executable can launch Chrome and establish a CDP session;
- the configured profile is writable and not locked by another owner;
- MCP clients point to the stable standalone binary in `mcp` mode;
- no duplicate Ghostchrome server or managed session is running.

Do not expose environment variables, credentials, cookies, profile contents, or
full command lines containing secrets in a report.

## CDP and Chrome failures

For `connection refused`, inspect the configured `GHOSTCHROME_CONNECT` endpoint
and local debugging port. For `browser not found`, install or select a supported
Chrome/Chromium executable and rerun the doctor. For Linux startup errors, use
the dependency list reported by the doctor; do not install packages silently.

For a locked profile, identify the owning Ghostchrome process first. Stop only a
session or MCP process recorded as Ghostchrome-managed. Never remove a profile
directory while a browser may still be using it.

## Timeouts and dynamic pages

Use `stable` or a targeted `wait_for` condition instead of increasing every
timeout. Inspect a fresh snapshot and `errors` after a timeout. Retry an
idempotent read once if the page remains reachable; do not repeat a submission,
upload, purchase, deletion, or other external side effect automatically.

When a page maintains analytics or chat connections, avoid an unbounded network
idle wait. Use a known selector, URL transition, or bounded delay after the
primary content is visible.

## Resource cleanup

CLI named sessions intentionally keep Chrome and their profiles alive between
calls. End a completed flow with:

```bash
ghostchrome sessions stop <name>
```

Use `--purge` only for a disposable profile. List and prune dead registry entries
before considering profile removal:

```bash
ghostchrome sessions list
ghostchrome sessions prune
ghostchrome profiles list
```

MCP keeps the stdio process available, blanks its pages after
`GHOSTCHROME_PAGE_IDLE_TIMEOUT` (5 minutes) and releases Chrome after
`GHOSTCHROME_IDLE_TIMEOUT` (15 minutes by default). Closing stdin should close
the browser immediately. If RSS remains elevated, inspect the process tree and
the client-owned MCP process before taking action. Report the process IDs and
measured RSS, not a guess.

## Mode repair

If the manifest is absent or corrupt, stop and report the exact setup state. Do
not create a second mode manually. If the opposite artifact or managed client
entry is present, use the explicit switch command:

```bash
ghostchrome setup switch --to cli --yes
ghostchrome setup switch --to mcp --yes
```

The switch must preserve unmanaged files and user profiles. A conflict with an
unmanaged binary or client entry requires manual resolution; never overwrite it
from an automation flow.
