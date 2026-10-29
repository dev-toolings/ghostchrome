"""Hermetic checks, with no browser or external requests."""
import threading
import unittest
from concurrent.futures import ThreadPoolExecutor

from vehicle_cache import VehicleCache


class VehicleCacheTests(unittest.TestCase):
    def test_normalization_expiry_and_caller_mutation(self):
        now = [0.0]
        calls = []
        cache = VehicleCache(
            lambda plate: calls.append(plate) or {"vehicle": {"make": "fixture"}},
            ttl_seconds=10, max_entries=2, clock=lambda: now[0],
        )
        record = cache.get("aa-123-bb")
        record["vehicle"]["make"] = "modified"
        self.assertEqual(cache.get("AA 123 BB")["vehicle"]["make"], "fixture")
        self.assertEqual(calls, ["AA123BB"])
        now[0] = 10.0
        cache.get("AA123BB")
        self.assertEqual(len(calls), 2)

    def test_concurrent_requests_share_one_lookup(self):
        entered = threading.Event()
        release = threading.Event()
        calls = []

        def lookup(plate):
            calls.append(plate)
            entered.set()
            self.assertTrue(release.wait(2))
            return {"make": "fixture"}

        cache = VehicleCache(lookup, ttl_seconds=10, max_entries=2)
        with ThreadPoolExecutor(max_workers=4) as executor:
            first = executor.submit(cache.get, "AA123BB")
            self.assertTrue(entered.wait(2))
            others = [executor.submit(cache.get, "aa-123-bb") for _ in range(3)]
            release.set()
            self.assertEqual(first.result(), {"make": "fixture"})
            self.assertTrue(all(f.result() == first.result() for f in others))
        self.assertEqual(calls, ["AA123BB"])

    def test_failures_are_not_cached_and_capacity_is_bounded(self):
        calls = []

        def lookup(plate):
            calls.append(plate)
            if len(calls) == 1:
                raise RuntimeError("challenge rejected")
            return {"plate": plate}

        cache = VehicleCache(lookup, ttl_seconds=10, max_entries=1)
        with self.assertRaises(RuntimeError):
            cache.get("AA123BB")
        cache.get("AA123BB")
        cache.get("CC456DD")
        cache.get("AA123BB")
        self.assertEqual(calls, ["AA123BB", "AA123BB", "CC456DD", "AA123BB"])


if __name__ == "__main__":
    unittest.main()
