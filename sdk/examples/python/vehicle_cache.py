"""Bounded, process-local vehicle cache for a validated lookup callable.

The callable must return a complete, successful vehicle record or raise.
Cache neither Turnstile tokens nor browser/session credentials in that record.
"""
from __future__ import annotations

import copy
import re
import threading
import time
from collections import OrderedDict
from concurrent.futures import Future
from typing import Callable


class VehicleCache:
    """Cache successful records and coalesce concurrent calls for one plate.

    TTL and capacity are explicit application policy. This cache is local to
    one process; multiple agent replicas require shared storage and coordination.
    The supplied lookup must isolate browser sessions for different plates, or
    serialize all browser work with its own lock.
    """

    def __init__(
        self,
        lookup: Callable[[str], dict],
        *,
        ttl_seconds: float,
        max_entries: int,
        clock: Callable[[], float] = time.monotonic,
    ) -> None:
        if ttl_seconds <= 0 or max_entries <= 0:
            raise ValueError("TTL and capacity must be positive")
        self._lookup = lookup
        self._ttl = ttl_seconds
        self._capacity = max_entries
        self._clock = clock
        self._lock = threading.Lock()
        self._entries: OrderedDict[str, tuple[float, dict]] = OrderedDict()
        self._pending: dict[str, Future] = {}

    def get(self, plate: str) -> dict:
        key = re.sub(r"[\s-]", "", plate).upper()
        if re.fullmatch(r"[A-Z]{2}[0-9]{3}[A-Z]{2}", key) is None:
            raise ValueError("Expected a French AA-123-BB plate")
        with self._lock:
            entry = self._entries.get(key)
            if entry is not None:
                expires, record = entry
                if self._clock() < expires:
                    self._entries.move_to_end(key)
                    return copy.deepcopy(record)
                del self._entries[key]
            future = self._pending.get(key)
            owner = future is None
            if owner:
                future = Future()
                self._pending[key] = future
        if not owner:
            return copy.deepcopy(future.result())
        try:
            record = copy.deepcopy(self._lookup(key))
            if not isinstance(record, dict) or not record:
                raise ValueError("Lookup must return a nonempty validated vehicle record")
            with self._lock:
                self._entries[key] = (self._clock() + self._ttl, record)
                self._entries.move_to_end(key)
                while len(self._entries) > self._capacity:
                    self._entries.popitem(last=False)
            future.set_result(record)
            return copy.deepcopy(record)
        except BaseException as error:
            future.set_exception(error)
            raise
        finally:
            with self._lock:
                self._pending.pop(key, None)
