"""
ghostchrome — Python SDK for ghostchrome browser automation.

Drives the ghostchrome JSONL agent protocol via a subprocess transport.

Quick start::

    from ghostchrome import Ghostchrome

    with Ghostchrome(extra_flags=["--connect=auto"]) as gc:
        nav, obs = gc.navigate("https://example.com")
        print(nav.status, nav.title)         # 200, "Example Domain"
        tree, _ = gc.extract(level="skeleton")
        print(tree.stats.interactive_count)  # e.g. 1
        gc.click("@1")
        errors = gc.errors()                 # list[ErrorEntry]
        url, _ = gc.url()
        print(url.title)
"""
from .client import Ghostchrome
from .transport import SubprocessTransport, Transport, TransportError
from .types import (
    BackForwardResult,
    ConsoleError,
    ErrorEntry,
    EvalResult,
    ExtractResult,
    ExtractStats,
    FillResult,
    GhostchromeError,
    InitResult,
    SnapshotDiff,
    TabInfo,
    DialogResult,
    TabsActionResult,
    DiffNode,
    DiffEntry,
    DiffStats,
    NavigateResult,
    NetworkError,
    Observation,
    Request,
    Response,
    ScreenshotResult,
    ScrollResult,
    UrlResult,
    parse_response,
)

__all__ = [
    # Client
    "Ghostchrome",
    # Transport
    "Transport",
    "SubprocessTransport",
    "TransportError",
    # Error
    "GhostchromeError",
    # Types
    "Request",
    "Response",
    "Observation",
    "ConsoleError",
    "NetworkError",
    "NavigateResult",
    "BackForwardResult",
    "ExtractResult",
    "ExtractStats",
    "FillResult",
    "ScrollResult",
    "UrlResult",
    "ErrorEntry",
    "EvalResult",
    "ScreenshotResult",
    "InitResult",
    "SnapshotDiff",
    "TabInfo",
    "DialogResult",
    "TabsActionResult",
    "DiffNode",
    "DiffEntry",
    "DiffStats",
    "parse_response",
]

__version__ = "0.7.1"
