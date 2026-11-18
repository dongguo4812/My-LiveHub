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

    def test_health_endpoint_reports_ready_and_allows_composite_display_origin(self):
        with mock.patch.object(hex_main, 'HTTP_SERVER', object()):
            response = self.client.get('/api/health')
        self.assertEqual(response.status_code, 200)
        self.assertTrue(response.get_json()['ready'])
        self.assertEqual(response.headers.get('Access-Control-Allow-Origin'), '*')
        self.assertEqual(response.headers.get('Cache-Control'), 'no-store')

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


    def test_shutdown_wait_list_excludes_asset_updater_but_keeps_critical_workers(self):
        screenshot = threading.Thread(name="ScreenshotWatcher")
        assets = threading.Thread(name="AssetUpdater")
        lcu = threading.Thread(name="LcuRecognition")
        gameflow = threading.Thread(name="GameflowWatcher")
        selector = threading.Thread(name="ScreenshotSelector")

        waiting = hex_main._shutdown_threads_to_join(
            [screenshot, assets, lcu, gameflow],
            selector,
        )

        self.assertEqual(
            waiting,
            [screenshot, lcu, gameflow, selector],
        )
        self.assertNotIn(assets, waiting)

    def test_shutdown_does_not_wait_for_manual_asset_update(self):
        started = threading.Event()
        release = threading.Event()

        def slow_update(*_args, **_kwargs):
            started.set()
            release.wait(timeout=5)
            return False

        with mock.patch.object(hex_main, "_update_assets_impl", side_effect=slow_update):
            response = self.client.post("/api/update-assets")
            self.assertEqual(response.status_code, 200)
            self.assertTrue(started.wait(timeout=2))
            with hex_main.manual_update_lock:
                workers = list(hex_main.manual_update_threads)
            self.assertEqual(len(workers), 1)
            worker = workers[0]

            # 关闭流程不 join 资源更新线程；stop_event 使其在请求返回后取消。
            hex_main.stop_event.set()
            self.assertTrue(worker.is_alive())
            release.set()
            worker.join(timeout=2)
            self.assertFalse(worker.is_alive())

    def test_reset_update_endpoint_rejects_requests_after_shutdown_begins(self):
        hex_main.stop_event.set()
        response = self.client.post("/api/reset-update-assets")
        self.assertEqual(response.status_code, 503)

    def test_asset_update_observes_preexisting_cancellation_before_network_io(self):
        cancel_event = threading.Event()
        cancel_event.set()
        with mock.patch.object(hex_main.requests, "get") as get:
            self.assertFalse(
                hex_main._update_assets_impl(cancel_event=cancel_event)
            )
            get.assert_not_called()

    def test_f1_callback_waiting_on_selector_lock_cannot_start_after_shutdown(self):
        callback_attempting = threading.Event()
        hex_main.selector._lock.acquire()

        def invoke_f1():
            callback_attempting.set()
            hex_main.selector.toggle()

        callback = threading.Thread(target=invoke_f1, name="F1CallbackRaceTest")
        callback.start()
        self.assertTrue(callback_attempting.wait(timeout=1))

        # 模拟停止流程先设置 stop_event，而已排队的 F1 回调仍在等待选择器锁。
        hex_main.stop_event.set()
        hex_main.selector._lock.release()
        callback.join(timeout=2)

        self.assertFalse(callback.is_alive())
        self.assertIsNone(hex_main.selector.thread)


if __name__ == "__main__":
    unittest.main()
