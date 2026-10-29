# Vehicle lookup caching and iautos-agent SDK integration

## Implemented in Ghostchrome

`sdk/examples/python/vehicle_cache.py` provides a standard-library-only
`VehicleCache(lookup, ttl_seconds=..., max_entries=...)`. It wraps an application
callable taking a normalized French AA123BB plate and returning a validated,
nonempty vehicle dictionary. The adapter must raise on a challenge, missing
vehicle, incomplete response or parsing error; the cache cannot infer business
validity from an arbitrary dictionary.

The cache normalizes spacing, hyphens and case, expires successful records,
evicts least-recently-used entries at its capacity, and shares one in-flight
lookup between callers requesting the same plate. It never caches failures.
Copies isolate caller mutations from both cached and shared results. TTL starts
when retrieval completes. The application explicitly chooses TTL and capacity.

This is a reusable example, not a new Ghostchrome operation or a bundled SIV
provider. It is process-local and resets on restart. Threaded callers for other
plates can run concurrently, so the lookup adapter must serialize work on a
shared browser/session or supply isolated workers. Do not share a profile
between concurrent Chrome processes.

Run the focused hermetic checks:

```sh
python3 -m unittest discover -s sdk/examples/python -p test_vehicle_cache.py -v
```

## SDK boundary

iautos-agent is the independent Python repository under
`others/iautos-agent` in the IAutos workspace. Source inspection found no current
Ghostchrome dependency. Its FastAPI application already manages a Redis client;
this change does not modify that repository or configure infrastructure.

The Python distribution is named `ghostchrome`, under `sdk/python` in this
repository. Its `Ghostchrome` client starts a persistent `ghostchrome agent`
subprocess using JSONL. It does not call `ghostchrome-mcp`. The CLI executable
must contain the frame/actionability fixes; installing only the Python package
will not update the Go engine. Publish or package a release containing those
fixes before pinning that binary and SDK in the iautos-agent image. No version
bump, release, installation switch or production build was done in this change.
The existing global MCP installation remains separate from that service image.

A concrete SDK initialization using the current source API is:

```python
from ghostchrome import Ghostchrome

gc = Ghostchrome(
    command="/usr/local/bin/ghostchrome",
    extra_flags=["--stealth", "--headless=false", "--user-profile=iautos-siv-1"],
    timeout=30,
)
gc.init()
# Reuse gc across requests in one serialized browser worker.
# Call gc.close() at application shutdown.
```

Headed Chrome needs a display; Xvfb was the display in the successful
investigation. Initialize off the ASGI event loop because this SDK is synchronous.
Browser lifecycle and worker limits belong to the application lifespan.

## Application adapter responsibilities

Add a dedicated SIV lookup service in iautos-agent, leaving its existing
auto-data.net crawler separate. The service owns:

1. A response schema with normalized plate, parsed vehicle fields, source and
   retrieval timestamp. Apply application-required field validation before
   returning a record eligible for caching.
2. The cache lookup before acquiring a browser worker. With one process, the
   example can wrap the validated lookup callable. With multiple replicas, use
   the existing Redis service for a versioned cache key, expiration and shared
   in-flight coordination. A Python lock only coordinates one process.
3. A serialized SDK/browser worker: navigate to siv-auto.fr, extract the widget
   subtree, click its current checkbox ref if present, and wait for real
   validation. Submit only the requested, authorized plate to the checked
   same-origin form. The current SDK methods are `navigate`, `extract`, `click`,
   `wait` and `eval_`; no `lookup_vehicle` method exists.
4. An HTTP client with only that site's session cookies, held in memory. Read
   `/result.php`, restrict redirects to the intended origin, parse and validate
   the HTML, then write the complete record into the cache. Keep the browser
   worker reserved through this read because server session state holds the
   current search.
5. Explicit HTTP errors for a challenge, timeout, missing result or parsing
   failure. Do not cache those responses as vehicle records. Do not blindly
   repeat a submission after an ambiguous outcome.

Call the synchronous lookup from an application-managed thread, not directly
inside an async FastAPI handler. Set request timeouts and concurrency limits at
the service boundary. Return cache status and retrieval timestamp for diagnosis;
never return browser cookies, CSRF values or Turnstile tokens.

Store stable technical fields according to the chosen freshness policy. Fields
that can change, such as colour, may need a separate TTL or refresh policy.
Do not cache Turnstile tokens as reusable validation. Preparing a widget during
user input remains an unmeasured optimization, not implemented here.

## Measured performance and remaining work

Warm uncached lookup reached `/infos.php` in 2.57-2.78 seconds across three
measurements. Reading the full result by HTTP with the validated session took
66 ms. A cache hit avoids both steps; no end-to-end iautos-agent cache-hit latency
has been measured. Cache misses have not demonstrated subsecond performance.
See [the investigation](turnstile-frames.md).

The independent iautos-agent checkout now implements the SIV provider in
`app/plates/siv_auto.py`, selected by `PLATE_DECODER_SOURCE=siv_auto` behind the
existing authenticated `/api/v1/plates/decode` route. Its Redis keys use HMACs,
successful records expire, concurrent requests share work, and per-plate Redis
leases coordinate replicas. Each browser worker serializes session use. The
provider uses same-origin HTTP fetch through SDK evaluation so HttpOnly cookies
stay inside Chrome. It validates the returned plate, make and model and does not
expose a VIN through the existing response schema. The original provider remains
the default. Deployment is separate; the CLI binary must contain this fix.

The cache example remains hermetically tested. See the iautos-agent checkout's
`docs/SIV_GHOSTCHROME.md` for integration tests and measured live results.
