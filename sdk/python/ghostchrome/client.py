"""
Ghostchrome Python client.

Provides a typed ``Ghostchrome`` class wrapping a Transport with one method
per JSONL op from contracts/commands.json.
"""
from __future__ import annotations

from typing import Any, Optional

from .transport import SubprocessTransport, Transport, TransportError
from .types import (
    BackForwardResult,
    ErrorEntry,
    EvalResult,
    ExtractResult,
    ExtractStats,
    FillResult,
    GhostchromeError,
    InitResult,
    NavigateResult,
    Observation,
    Response,
    ScreenshotResult,
    ScrollResult,
    UrlResult,
    _parse_observation,
    SnapshotDiff,
    TabInfo,
    DialogResult,
    TabsActionResult,
    _parse_mutation_result,
)


def _check(raw: dict, op: str = "") -> Response:
    """Wrap raw dict in a Response; raise GhostchromeError on ok=False."""
    obs = _parse_observation(raw.get("observation"))
    result = raw.get("result")
    resp = Response(
        id=raw.get("id", ""),
        ok=raw.get("ok", False),
        result=result,
        observation=obs,
        error=raw.get("error"),
        events=raw.get("events", []),
        protocol=raw.get("protocol"),
        error_code=raw.get("error_code"),
        retryable=raw.get("retryable"),
    )
    if not resp.ok:
        raise GhostchromeError(op or "unknown", resp.error or str(raw))
    return resp


def _result_dict(resp: Response) -> dict[str, Any]:
    """Return the result as a dict, defaulting to {} when omitted."""
    if isinstance(resp.result, dict):
        return resp.result
    return {}


class Ghostchrome:
    """
    Typed client for the ghostchrome JSONL agent protocol.

    Each method corresponds to a JSONL-surface op from contracts/commands.json.

    Usage::

        with Ghostchrome(extra_flags=["--connect=auto"]) as gc:
            nav, obs = gc.navigate("https://example.com")
            print(nav.status, nav.title)       # 200, "Example Domain"
            tree, _ = gc.extract(level="skeleton")
            print(tree.stats.interactive_count)
            gc.click("@1")
            errors = gc.errors()               # list[ErrorEntry]
            url, _ = gc.url()
            print(url.title)

    Args:
        transport:    A :class:`~ghostchrome.transport.Transport` instance.
                      Defaults to ``SubprocessTransport()`` which spawns
                      ``ghostchrome agent``.
        command:      Binary path (used only when transport is None).
        args:         Args after command (default ``["agent"]``).
        extra_flags:  Extra CLI flags (e.g. ``["--connect=auto", "--stealth"]``).
        timeout:      Per-op read timeout in seconds (default 30).
    """

    def __init__(
        self,
        transport: Transport | None = None,
        *,
        command: str = "ghostchrome",
        args: list[str] | None = None,
        extra_flags: list[str] | None = None,
        timeout: float = 30.0,
    ) -> None:
        if transport is not None:
            self._transport = transport
        else:
            self._transport = SubprocessTransport(
                command=command,
                args=args,
                extra_flags=extra_flags,
                timeout=timeout,
            )

    # ------------------------------------------------------------------
    # Context manager
    # ------------------------------------------------------------------

    def __enter__(self) -> "Ghostchrome":
        return self

    def __exit__(self, *_: Any) -> None:
        self.close()

    # ------------------------------------------------------------------
    # Internal helper
    # ------------------------------------------------------------------

    def _call(self, op: str, args: dict | None = None) -> Response:
        raw = self._transport.send(op, args)
        return _check(raw, op)

    # ------------------------------------------------------------------
    # Lifecycle ops
    # ------------------------------------------------------------------

    def init(self) -> tuple[InitResult, Optional[Observation]]:
        """Open the browser (no-op if already open).

        Newer agents include protocol/version in the result.
        """
        resp = self._call("init")
        d = _result_dict(resp)
        return InitResult(protocol=d.get("protocol"), version=d.get("version")), resp.observation

    def close(self) -> None:
        """Close the browser session and shut down the agent."""
        try:
            self._call("close")
        except (GhostchromeError, TransportError, OSError):
            pass
        self._transport.close()

    # ------------------------------------------------------------------
    # Navigation ops
    # ------------------------------------------------------------------

    def navigate(
        self,
        url: str,
        *,
        wait: Optional[str] = None,
    ) -> tuple[NavigateResult, Optional[Observation]]:
        """Load a URL in the current tab.

        Args:
            url:  Absolute URL.
            wait: Wait strategy: ``load | stable | idle | none | domcontentloaded``.

        Returns:
            ``(NavigateResult, observation)``
            ``NavigateResult`` has: ``url``, ``title``, ``status``, ``time_ms``.
        """
        a: dict[str, Any] = {"url": url}
        if wait is not None:
            a["wait"] = wait
        resp = self._call("navigate", a)
        d = _result_dict(resp)
        result = NavigateResult(
            url=d.get("url", ""),
            title=d.get("title", ""),
            status=d.get("status", 0),
            time_ms=d.get("time_ms", 0),
        )
        return result, resp.observation

    def back(self) -> tuple[BackForwardResult, Optional[Observation]]:
        """Navigate back in browser history."""
        resp = self._call("back")
        d = _result_dict(resp)
        return BackForwardResult(url=d.get("url"), title=d.get("title")), resp.observation

    def forward(self) -> tuple[BackForwardResult, Optional[Observation]]:
        """Navigate forward in browser history."""
        resp = self._call("forward")
        d = _result_dict(resp)
        return BackForwardResult(url=d.get("url"), title=d.get("title")), resp.observation

    def reload(self) -> tuple[BackForwardResult, Optional[Observation]]:
        """Reload (refresh) the current page."""
        resp = self._call("reload")
        d = _result_dict(resp)
        return BackForwardResult(url=d.get("url"), title=d.get("title")), resp.observation

    # ------------------------------------------------------------------
    # Extraction
    # ------------------------------------------------------------------

    def extract(
        self,
        *,
        level: Optional[str] = None,
        selector: Optional[str] = None,
    ) -> tuple[ExtractResult, Optional[Observation]]:
        """Return a compact accessibility tree of the current page with @refs.

        Args:
            level:    ``skeleton | content | full`` (default: ``content``).
            selector: Optional CSS selector to scope extraction.

        Returns:
            ``(ExtractResult, observation)``
            ``ExtractResult.stats`` has fields:
            ``total_nodes``, ``filtered_nodes``, ``interactive_count``.
            ``ExtractResult.warnings`` is non-empty when a selector could not
            be honored; the nodes are then the full page.
        """
        a: dict[str, Any] = {}
        if level is not None:
            a["level"] = level
        if selector is not None:
            a["selector"] = selector
        resp = self._call("extract", a)
        d = _result_dict(resp)
        raw_stats = d.get("stats") or {}
        stats = ExtractStats(
            total_nodes=raw_stats.get("total_nodes", 0),
            filtered_nodes=raw_stats.get("filtered_nodes", 0),
            interactive_count=raw_stats.get("interactive_count", 0),
        )
        result = ExtractResult(
            nodes=d.get("nodes", []),
            refs=d.get("refs", {}),
            stats=stats,
            warnings=d.get("warnings", []),
        )
        return result, resp.observation

    # ------------------------------------------------------------------
    # Interaction ops
    # ------------------------------------------------------------------

    def click(self, ref: str, *, snapshot: Optional[str] = None, button: Optional[str] = None) -> tuple[SnapshotDiff | ExtractResult, Optional[Observation]]:
        """Click an element by its ``@ref`` from the last extract/snapshot."""
        args: dict[str, Any] = {"ref": ref}
        if snapshot is not None:
            args["snapshot"] = snapshot
        if button is not None:
            args["button"] = button
        resp = self._call("click", args)
        return _parse_mutation_result(resp.result), resp.observation

    def dblclick(self, ref: str, *, snapshot: Optional[str] = None, button: Optional[str] = None) -> tuple[SnapshotDiff | ExtractResult, Optional[Observation]]:
        """Double-click an element by its ``@ref`` from the last extract/snapshot."""
        args: dict[str, Any] = {"ref": ref}
        if snapshot is not None:
            args["snapshot"] = snapshot
        if button is not None:
            args["button"] = button
        resp = self._call("dblclick", args)
        return _parse_mutation_result(resp.result), resp.observation

    def check(self, ref: str) -> tuple[SnapshotDiff | ExtractResult, Optional[Observation]]:
        """Tick a checkbox/radio by ``@ref`` (idempotent — no-op if already checked)."""
        resp = self._call("check", {"ref": ref})
        return _parse_mutation_result(resp.result), resp.observation

    def uncheck(self, ref: str) -> tuple[SnapshotDiff | ExtractResult, Optional[Observation]]:
        """Untick a checkbox by ``@ref`` (idempotent — no-op if already unchecked)."""
        resp = self._call("uncheck", {"ref": ref})
        return _parse_mutation_result(resp.result), resp.observation

    def hover(self, ref: str, *, snapshot: Optional[str] = None) -> tuple[SnapshotDiff | ExtractResult, Optional[Observation]]:
        """Hover over an element by ``@ref`` (reveals dropdowns, tooltips)."""
        args: dict[str, Any] = {"ref": ref}
        if snapshot is not None:
            args["snapshot"] = snapshot
        resp = self._call("hover", args)
        return _parse_mutation_result(resp.result), resp.observation

    def type_(
        self,
        ref: str,
        text: str,
        *,
        submit: bool = False,
        snapshot: Optional[str] = None,
    ) -> tuple[SnapshotDiff | ExtractResult, Optional[Observation]]:
        """Type text into an input/textarea identified by ``@ref``.

        The field is cleared before typing.

        Args:
            ref:    ``@ref`` of the input element.
            text:   Text to type.
            submit: If True, press Enter after typing (submit the form).
        """
        args: dict[str, Any] = {"ref": ref, "text": text}
        if submit:
            args["submit"] = True
        if snapshot is not None:
            args["snapshot"] = snapshot
        resp = self._call("type", args)
        return _parse_mutation_result(resp.result), resp.observation

    def press(
        self,
        key: str,
        *,
        ref: Optional[str] = None,
        snapshot: Optional[str] = None,
    ) -> tuple[SnapshotDiff | ExtractResult, Optional[Observation]]:
        """Press a keyboard key; optionally focus an element by ``@ref`` first.

        Args:
            key: Key name, e.g. ``Enter``, ``Escape``, ``ArrowDown``.
            ref: Optional ``@ref`` to focus before pressing.
            snapshot: ``none | diff | full`` (default: diff).
        """
        a: dict[str, Any] = {"key": key}
        if ref is not None:
            a["ref"] = ref
        if snapshot is not None:
            a["snapshot"] = snapshot
        resp = self._call("press", a)
        return _parse_mutation_result(resp.result), resp.observation

    def select(
        self,
        ref: str,
        values: list[str],
        *,
        snapshot: Optional[str] = None,
    ) -> tuple[SnapshotDiff | ExtractResult, Optional[Observation]]:
        """Pick one or more options in a ``<select>`` element by ``@ref``.

        Args:
            ref:    ``@ref`` of the ``<select>`` element.
            values: Option values to select.
        """
        args: dict[str, Any] = {"ref": ref, "values": values}
        if snapshot is not None:
            args["snapshot"] = snapshot
        resp = self._call("select", args)
        return _parse_mutation_result(resp.result), resp.observation

    def fill(
        self,
        fields: dict[str, str],
    ) -> tuple[FillResult, Optional[Observation]]:
        """Fill multiple form fields in one call (convenience wrapper over type).

        Args:
            fields: Map of ``@ref`` → value strings.

        Returns:
            ``(FillResult, observation)``
            ``FillResult.filled`` is the number of fields filled.
        """
        resp = self._call("fill", {"fields": fields})
        d = _result_dict(resp)
        return FillResult(filled=d.get("filled", 0)), resp.observation

    # ------------------------------------------------------------------
    # Scroll ops
    # ------------------------------------------------------------------

    def scroll_by(self, dy: int) -> tuple[ScrollResult, Optional[Observation]]:
        """Scroll the viewport vertically by ``dy`` pixels (positive = down).

        Returns:
            ``(ScrollResult, observation)``
            ``ScrollResult.y`` is the new vertical scroll position.
        """
        resp = self._call("scroll_by", {"dy": dy})
        d = _result_dict(resp)
        return ScrollResult(y=d.get("y", 0)), resp.observation

    def scroll_to(
        self,
        *,
        y: Optional[int] = None,
        bottom: Optional[bool] = None,
    ) -> tuple[ScrollResult, Optional[Observation]]:
        """Scroll to an absolute Y position or to the bottom of the page.

        Args:
            y:      Absolute Y coordinate.
            bottom: If ``True``, scroll to the page bottom.

        Returns:
            ``(ScrollResult, observation)``
            ``ScrollResult.y`` is the new vertical scroll position.
        """
        a: dict[str, Any] = {}
        if y is not None:
            a["y"] = y
        if bottom is not None:
            a["bottom"] = bottom
        resp = self._call("scroll_to", a)
        d = _result_dict(resp)
        return ScrollResult(y=d.get("y", 0)), resp.observation

    # ------------------------------------------------------------------
    # Eval / Screenshot / Wait
    # ------------------------------------------------------------------

    def eval_(
        self,
        expr: str,
        *,
        ref: Optional[str] = None,
    ) -> tuple[EvalResult, Optional[Observation]]:
        """Evaluate a JS expression on the page and return the stringified result.

        Args:
            expr: JS expression.
            ref:  Optional ``@ref`` to bind as ``this``.
        """
        a: dict[str, Any] = {"expr": expr}
        if ref is not None:
            a["ref"] = ref
        resp = self._call("eval", a)
        d = _result_dict(resp)
        return EvalResult(value=d.get("value")), resp.observation

    def screenshot(
        self,
        *,
        full_page: Optional[bool] = None,
        ref: Optional[str] = None,
        quality: Optional[int] = None,
    ) -> tuple[ScreenshotResult, Optional[Observation]]:
        """Capture the current viewport (or element) as a PNG/JPEG image.

        Args:
            full_page: Capture full scrollable page (default ``False``).
            ref:       Capture only this element by ``@ref``.
            quality:   JPEG quality 1-100.

        Returns:
            ``(ScreenshotResult, observation)``
            ``ScreenshotResult.base64`` contains the image data.
            ``ScreenshotResult.mime`` is ``"image/png"`` or ``"image/jpeg"``.
        """
        a: dict[str, Any] = {}
        if full_page is not None:
            a["full_page"] = full_page
        if ref is not None:
            a["ref"] = ref
        if quality is not None:
            a["quality"] = quality
        resp = self._call("screenshot", a)
        d = _result_dict(resp)
        result = ScreenshotResult(
            mime=d.get("mime", ""),
            base64=d.get("base64"),
        )
        return result, resp.observation

    def wait(
        self,
        *,
        selector: Optional[str] = None,
        ref: Optional[str] = None,
        text: Optional[str] = None,
        url: Optional[str] = None,
        load: Optional[str] = None,
        state: Optional[str] = None,
        ms: Optional[int] = None,
        timeout_ms: Optional[int] = None,
    ) -> tuple[None, Optional[Observation]]:
        """Wait for a selector/@ref, visible text, URL substring, load state, or delay."""
        a: dict[str, Any] = {}
        if selector is not None:
            a["selector"] = selector
        if ref is not None:
            a["ref"] = ref
        if text is not None:
            a["text"] = text
        if url is not None:
            a["url"] = url
        if load is not None:
            a["load"] = load
        if state is not None:
            a["state"] = state
        if ms is not None:
            a["ms"] = ms
        if timeout_ms is not None:
            a["timeout_ms"] = timeout_ms
        resp = self._call("wait", a)
        return None, resp.observation

    # ------------------------------------------------------------------
    # Introspection ops
    # ------------------------------------------------------------------

    def errors(self) -> list[ErrorEntry]:
        """Return console and network errors observed on the current page.

        Returns:
            A list of :class:`~ghostchrome.types.ErrorEntry` objects.
            Returns an empty list when there are no errors.

        Note:
            The ``errors`` op result is a JSON array (not a dict).
            Each entry has: ``type``, ``level``, ``message``, ``source``,
            ``time_ms``, and optionally ``status`` / ``method``.
        """
        resp = self._call("errors")
        raw_list = resp.result if isinstance(resp.result, list) else []
        return [
            ErrorEntry(
                type=e.get("type", ""),
                level=e.get("level", ""),
                message=e.get("message", ""),
                source=e.get("source", ""),
                time_ms=e.get("time_ms", 0),
                status=e.get("status"),
                method=e.get("method"),
            )
            for e in raw_list
        ]

    def dialog(
        self,
        *,
        action: Optional[str] = None,
        text: Optional[str] = None,
    ) -> tuple[DialogResult, Optional[Observation]]:
        """Set how JavaScript dialogs are auto-handled (accept or dismiss)."""
        args: dict[str, Any] = {}
        if action is not None:
            args["action"] = action
        if text is not None:
            args["text"] = text
        resp = self._call("dialog", args)
        d = _result_dict(resp)
        return DialogResult(action=d.get("action", ""), text=d.get("text", "")), resp.observation

    def tabs(
        self,
        *,
        action: Optional[str] = None,
        index: Optional[int] = None,
        url: Optional[str] = None,
    ) -> tuple[list[TabInfo] | TabsActionResult, Optional[Observation]]:
        """List, switch, close, or open browser tabs."""
        args: dict[str, Any] = {}
        if action is not None:
            args["action"] = action
        if index is not None:
            args["index"] = index
        if url is not None:
            args["url"] = url
        resp = self._call("tabs", args)
        if isinstance(resp.result, list):
            tabs = [
                TabInfo(
                    index=item.get("index", 0),
                    url=item.get("url", ""),
                    title=item.get("title", ""),
                    target_id=item.get("target_id", ""),
                    active=bool(item.get("active", False)),
                )
                for item in resp.result
                if isinstance(item, dict)
            ]
            return tabs, resp.observation
        d = _result_dict(resp)
        return TabsActionResult(
            action=d.get("action", ""),
            index=d.get("index"),
            url=d.get("url", ""),
            title=d.get("title", ""),
        ), resp.observation

    def url(self) -> tuple[UrlResult, Optional[Observation]]:
        """Return the current page URL and title."""
        resp = self._call("url")
        d = _result_dict(resp)
        result = UrlResult(
            url=d.get("url", ""),
            title=d.get("title", ""),
        )
        return result, resp.observation
