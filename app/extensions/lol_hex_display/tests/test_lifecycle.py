"""Focused lifecycle regression tests for the LOL Hex runtime."""

import sys
import threading
import unittest
from pathlib import Path
from unittest import mock

EXTENSION_DIR = Path(__file__).resolve().parents[1]
if str(EXTENSION_DIR) not in sys.path:
    sys.path.insert(0, str(EXTENSION_DIR))

import main as hex_main  # noqa: E402


class HexLifecycleTests(unittest.TestCase):
    def setUp(self):
        hex_main.stop_event.set()
        with hex_main.manual_update_lock:
            stale = list(hex_main.manual_update_threads)
        for thread in stale:
            thread.join(timeout=2)
        hex_main.stop_event.clear()
        with hex_main.update_status_lock:
            hex_main.update_status.update({
                "running": False,
                "message": "尚未手动检查",
                "updated_at": 0,
            })
        self.client = hex_main.app.test_client()

    def tearDown(self):
        hex_main.stop_event.set()
        with hex_main.manual_update_lock:
            threads = list(hex_main.manual_update_threads)
        for thread in threads:
            thread.join(timeout=2)
        hex_main.stop_event.clear()

    def test_manual_update_endpoint_rejects_requests_after_shutdown_begins(self):
        hex_main.stop_event.set()
        response = self.client.post("/api/update-assets")
        self.assertEqual(response.status_code, 503)
        self.assertFalse(hex_main.manual_update_threads)

    def test_manual_update_worker_is_tracked_and_exits_on_shutdown(self):
        started = threading.Event()

        def wait_for_shutdown(*_args, **_kwargs):
            started.set()
            hex_main.stop_event.wait(timeout=3)
            return False

        with mock.patch.object(hex_main, "_update_assets_impl", side_effect=wait_for_shutdown):
            response = self.client.post("/api/update-assets")
            self.assertEqual(response.status_code, 200)
            self.assertTrue(started.wait(timeout=2))

            with hex_main.manual_update_lock:
                workers = list(hex_main.manual_update_threads)
            self.assertEqual(len(workers), 1)
            worker = workers[0]
            self.assertEqual(worker.name, "ManualAssetUpdater")

            hex_main.stop_event.set()
            worker.join(timeout=2)

        self.assertFalse(worker.is_alive())
        with hex_main.manual_update_lock:
            self.assertNotIn(worker, hex_main.manual_update_threads)
        self.assertFalse(hex_main._asset_update_mutex.locked())

    def test_asset_update_observes_preexisting_cancellation_before_network_io(self):
        cancel_event = threading.Event()
        cancel_event.set()
        with mock.patch.object(hex_main.requests, "get") as get:
            self.assertFalse(
                hex_main._update_assets_impl(cancel_event=cancel_event)
            )
            get.assert_not_called()

    def test_f1_callback_cannot_start_screenshot_after_shutdown_begins(self):
        hex_main.stop_event.set()
        with mock.patch.object(hex_main.threading, "Thread") as thread_factory:
            hex_main.selector.toggle()
            thread_factory.assert_not_called()
        self.assertIsNone(hex_main.selector.thread)


if __name__ == "__main__":
    unittest.main()
