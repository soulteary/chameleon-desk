"""Offline USB management, lifecycle and Desk integration tests."""

import asyncio
import contextlib
import json
import os
import re
import sys
import tempfile
import threading
import types
import unittest
import urllib.request
from pathlib import Path
from unittest import mock

import bridge
import test_management
from management import ManagementServer
from serial_backend import USB_RESTRICTED_COMMANDS, transport_info
from serial_management import SerialController
from test_bridge import application_frame, configure_info_device, eventually
from test_serial_backend import Factory


class SerialManagementTests(unittest.IsolatedAsyncioTestCase):
    open_request = test_management.ManagementTests.open_request
    response = test_management.ManagementTests.response
    request = test_management.ManagementTests.request

    async def asyncSetUp(self):
        self.first = types.SimpleNamespace(device="COM7", description="Chameleon application",
            vid=0x6868, pid=0x8686, serial_number="first", location="usb-1", hwid="USB first")
        self.ports = [self.first]
        self.factory = Factory()
        self.owner = SerialController(lambda: self.ports, self.factory, timeout=0.3)
        self.data = await asyncio.start_server(self.owner.bridge.accept, "127.0.0.1", 0)
        self.data_port = self.data.sockets[0].getsockname()[1]
        self.owner.data_address = f"127.0.0.1:{self.data_port}"
        self.token = "a" * 64
        self.api = ManagementServer(self.owner, self.token, "")
        self.control = await asyncio.start_server(self.api.accept, "127.0.0.1", 0, limit=8192)
        self.port = self.control.sockets[0].getsockname()[1]
        self.api.authority = f"127.0.0.1:{self.port}"
        self.writers, self.gates = [], []

    async def asyncTearDown(self):
        for gate in self.gates:
            gate.set()
        for port in self.factory.ports:
            for gate in (port.open_gate, port.write_gate, port.close_gate):
                if gate:
                    gate.set()
        self.control.close()
        self.data.close()
        await self.api.close()
        await self.owner.close()
        await self.control.wait_closed()
        await self.data.wait_closed()
        for writer in self.writers:
            writer.close()
            with contextlib.suppress(Exception):
                await writer.wait_closed()
        for port in self.factory.ports:
            await asyncio.to_thread(port.thread.join, 1)
            self.assertFalse(port.thread.is_alive())
        if self.owner.discovery:
            await asyncio.to_thread(self.owner.discovery.join, 1)
            self.assertFalse(self.owner.discovery.is_alive())

    async def select(self):
        code, result = await self.request("scan")
        self.assertEqual(code, 200, result)
        self.assertEqual((await self.request("select", {"device_id": "COM7"}))[0], 200)
        return result

    async def connect(self):
        await self.select()
        code, status = await self.request("connect")
        self.assertEqual(code, 200, status)
        return status

    async def data_client(self):
        reader, writer = await asyncio.open_connection("127.0.0.1", self.data_port)
        self.writers.append(writer)
        line = await asyncio.wait_for(reader.readline(), 2)
        return reader, writer, line

    async def test_scan_select_connect_transfer_status_disconnect(self):
        scan = await self.select()
        self.assertEqual(scan["devices"][0]["vid"], 0x6868)
        self.assertFalse(self.factory.ports)  # Enumeration never opens a port.
        code, status = await self.request("connect")
        self.assertEqual(code, 200)
        self.assertEqual(status["transport"], transport_info(True))
        self.assertFalse(status["session_active"])
        reader, writer, line = await self.data_client()
        self.assertEqual(json.loads(line[len(bridge.PREAMBLE):])["transport"], transport_info(True))
        frame = application_frame(payload=b"application")
        writer.write(frame[:4]); await writer.drain()
        writer.write(frame[4:]); await writer.drain()
        self.assertEqual(await asyncio.wait_for(reader.readexactly(len(frame)), 2), frame)
        self.assertEqual((await self.request("status", method="GET"))[1]["session_active"], True)
        self.assertEqual((await self.data_client())[2], b"")
        code, status = await self.request("disconnect")
        self.assertEqual(code, 200)
        self.assertEqual(status["state"], "disconnected")
        self.assertEqual(status["error"], "")
        self.assertEqual(await asyncio.wait_for(reader.read(), 2), b"")
        self.assertTrue(self.factory.ports[0].closed.is_set())
        self.assertFalse(self.factory.ports[0].thread.is_alive())

    async def test_selection_and_port_identity_are_checked_before_open(self):
        self.assertEqual((await self.request("connect"))[0], 409)
        self.assertEqual((await self.request("select", {"device_id": "/arbitrary"}))[0], 400)
        await self.select()
        self.first.serial_number = "replacement"
        self.assertEqual((await self.request("connect"))[0], 502)
        self.assertIsNone(self.owner.selected)
        self.assertFalse(self.factory.ports)
        await self.select()
        self.ports.clear()
        await self.request("scan")
        self.assertIsNone(self.owner.selected)

    async def test_active_selection_changes_are_rejected_and_data_close_requires_connect(self):
        await self.connect()
        for action, body in (("scan", {}), ("select", {"device_id": "COM7"}), ("connect", {})):
            self.assertEqual((await self.request(action, body))[0], 409)
        old = self.owner.link
        _, writer, _ = await self.data_client()
        writer.close()
        await eventually(lambda: self.owner.state == "disconnected")
        self.assertEqual((await self.data_client())[2], b"")
        self.assertEqual(len(self.factory.ports), 1)
        self.assertEqual((await self.request("connect"))[0], 200)
        self.assertEqual(len(self.factory.ports), 2)
        await old.watch()
        self.assertEqual(self.owner.state, "connected")
        reader, writer, _ = await self.data_client()
        self.factory.ports[0].emit(b"old bytes")
        frame = application_frame()
        writer.write(frame); await writer.drain()
        self.assertEqual(await asyncio.wait_for(reader.readexactly(len(frame)), 2), frame)

    async def test_idle_unplug_updates_status_without_automatic_reconnect(self):
        await self.connect()
        self.factory.ports[0].reads.put(OSError("unplug"))
        await eventually(lambda: self.owner.state == "disconnected")
        self.assertFalse((await self.request("status", method="GET"))[1]["session_active"])
        self.assertEqual(len(self.factory.ports), 1)
        self.assertTrue(self.factory.ports[0].closed.is_set())

    async def check_unplug_with_stalled_close(self, active):
        gate = threading.Event(); self.gates.append(gate)
        self.factory.configure = lambda port: setattr(port, "close_gate", gate)
        await self.connect()
        if active:
            reader, _, _ = await self.data_client()
        self.factory.ports[0].reads.put(OSError("unplug"))
        await eventually(lambda: self.owner.state == "blocked")
        if active:
            self.assertEqual(await asyncio.wait_for(reader.read(), 2), b"")
        self.assertEqual((await self.data_client())[2], b"")
        self.assertEqual((await self.request("connect"))[0], 409)
        gate.set()
        await eventually(lambda: self.factory.ports[0].closed.is_set())
        self.assertEqual(self.owner.state, "blocked")

    async def test_idle_unplug_does_not_wait_indefinitely_for_driver_close(self):
        await self.check_unplug_with_stalled_close(False)

    async def test_active_unplug_ends_tcp_even_if_driver_close_stalls(self):
        await self.check_unplug_with_stalled_close(True)

    async def test_cleanup_failure_blocks_replacement_clients(self):
        self.factory.configure = lambda port: setattr(port, "close_error", True)
        await self.connect()
        status = (await self.request("disconnect"))[1]
        self.assertEqual(status["state"], "blocked")
        self.assertEqual((await self.request("connect"))[0], 409)
        self.assertEqual((await self.data_client())[2], b"")
        self.assertEqual(len(self.factory.ports), 1)

    async def test_disconnect_cleans_link_when_claimed_tcp_task_has_not_started(self):
        for close_error in (False, True):
            self.factory.configure = lambda port: setattr(port, "close_error", close_error)
            await self.connect()
            port = self.factory.ports[-1]
            writer = mock.Mock()
            writer.wait_closed = mock.AsyncMock()
            self.owner.bridge.accept(mock.Mock(), writer)
            # No event-loop yield between accept and disconnect: handle cannot
            # yet have constructed a SerialSession that owns the open port.
            status = await self.owner.disconnect()
            self.assertTrue(port.closed.is_set())
            self.assertFalse(port.thread.is_alive())
            self.assertEqual(status["state"], "blocked" if close_error else "disconnected")

    async def test_close_timeout_keeps_bridge_blocked_after_late_close(self):
        gate = threading.Event(); self.gates.append(gate)
        self.factory.configure = lambda port: setattr(port, "close_gate", gate)
        await self.connect()
        self.assertEqual((await self.request("disconnect"))[1]["state"], "blocked")
        gate.set()
        await eventually(lambda: self.factory.ports[0].closed.is_set())
        self.assertEqual(self.owner.state, "blocked")
        self.assertEqual((await self.request("connect"))[0], 409)
        self.assertEqual((await self.data_client())[2], b"")

    async def test_abandoned_connect_closes_late_open_before_reuse(self):
        gate = threading.Event(); self.gates.append(gate)
        self.factory.configure = lambda port: setattr(port, "open_gate", gate)
        await self.select()
        _, writer = await self.open_request("connect")
        await eventually(lambda: bool(self.factory.ports))
        writer.close()
        await eventually(lambda: self.owner.link.worker.stop.is_set())
        self.assertEqual(self.owner.state, "disconnecting")
        self.assertEqual((await self.request("connect"))[0], 409)
        gate.set()
        await eventually(lambda: self.owner.state == "disconnected")
        self.assertTrue(self.factory.ports[0].closed.is_set())
        self.assertFalse(self.factory.ports[0].writes)

    async def test_abandoned_failed_setup_finishes_cleanup_and_state_publication(self):
        gate = threading.Event(); self.gates.append(gate)
        def configure(port):
            port.open_error = True
            port.close_gate = gate
        self.factory.configure = configure
        await self.select()
        _, writer = await self.open_request("connect")
        await eventually(lambda: self.owner.state == "disconnecting")
        writer.close()
        await asyncio.sleep(0.02)
        gate.set()
        await eventually(lambda: self.owner.state == "disconnected")
        self.assertIsNone(self.owner.link)
        self.assertFalse(self.owner.bridge.blocked)
        self.assertFalse(self.factory.ports[0].thread.is_alive())

    async def test_disconnect_cancels_setup_and_waits_for_close(self):
        gate = threading.Event(); self.gates.append(gate)
        self.factory.configure = lambda port: setattr(port, "open_gate", gate)
        await self.select()
        connect_reader, _ = await self.open_request("connect")
        await eventually(lambda: bool(self.factory.ports))
        disconnect_reader, _ = await self.open_request("disconnect")
        await eventually(lambda: self.owner.link.worker.stop.is_set())
        self.assertEqual((await self.request("status", method="GET"))[1]["state"], "disconnecting")
        gate.set()
        self.assertEqual((await self.response(connect_reader))[0], 409)
        self.assertEqual((await self.response(disconnect_reader))[1]["state"], "disconnected")
        self.assertTrue(self.factory.ports[0].closed.is_set())

    async def test_abandoned_scan_has_no_late_selection_or_parallel_enumeration(self):
        gate = threading.Event(); self.gates.append(gate)
        self.owner.enumerator = lambda: (gate.wait(), self.ports)[1]
        _, writer = await self.open_request("scan")
        await eventually(lambda: self.owner.state == "scanning")
        writer.close()
        await eventually(lambda: self.owner.state == "disconnected")
        self.assertEqual((await self.request("scan"))[0], 409)
        gate.set()
        await eventually(lambda: not self.owner.discovery.is_alive())
        self.assertFalse(self.owner.devices)
        self.assertEqual((await self.request("scan"))[0], 200)

    async def test_unauthorized_requests_and_invalid_fields_do_not_scan_or_open(self):
        for options, expected in (({"token": "bad"}, 401), ({"extra": "Origin: http://evil.example\r\n"}, 403), ({"host": "evil.example"}, 403), ({"extra": "Content-Length: 2\r\n"}, 400)):
            self.assertEqual((await self.request("scan", **options))[0], expected)
        self.assertEqual((await self.request("scan", {"extra": True}))[0], 400)
        self.assertFalse(self.owner.devices)
        self.assertFalse(self.factory.ports)

    async def test_managed_usb_rejects_pairing_reset_and_bootloader_before_serial_write(self):
        await self.select()
        for command in sorted(USB_RESTRICTED_COMMANDS):
            self.assertEqual((await self.request("connect"))[0], 200)
            reader, writer, _ = await self.data_client()
            writer.write(application_frame(command, b"123456")); await writer.drain()
            self.assertEqual(await asyncio.wait_for(reader.read(), 2), b"")
            await eventually(lambda: self.owner.state == "disconnected")
            self.assertFalse(self.factory.ports[-1].writes)

    @unittest.skipUnless(os.environ.get("CHAMELEON_BRIDGE_TEST_BINARY"), "set path to a no-cgo CLI binary")
    async def test_desk_usb_cli_scans_without_opening_serial(self):
        with tempfile.TemporaryDirectory() as directory:
            key = Path(directory)/"bridge.key"; key.write_text(self.token); key.chmod(0o600)
            process = await asyncio.create_subprocess_exec(os.path.abspath(os.environ["CHAMELEON_BRIDGE_TEST_BINARY"]),
                "usb", "scan", "--usb-bridge", "http://"+self.api.authority, "--bridge-token-file", str(key),
                stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.PIPE)
            output, errors = await asyncio.wait_for(process.communicate(), 10)
            self.assertEqual(process.returncode, 0, errors)
            result = json.loads(output)
            self.assertEqual(result["devices"][0]["id"], "COM7")
            self.assertNotIn(self.token.encode(), output+errors)
            self.assertFalse(self.factory.ports)

    @unittest.skipUnless(os.environ.get("CHAMELEON_BRIDGE_TEST_BINARY"), "set path to a no-cgo CLI binary")
    async def test_desk_routine_usb_addresses_have_explicit_migration_errors(self):
        binary = os.path.abspath(os.environ["CHAMELEON_BRIDGE_TEST_BINARY"])
        for args, message in ((["info"], b"Python bridge"), (["info", "--port", "COM7"], b"Python bridge"),
                              (["devices"], b"usb scan"), (["settings", "set", "--name", "ble-key", "--key", "123456", "--yes", "--native-usb", "--port", "tcp:127.0.0.1:12345"], b"explicit local application"),
                              (["backup", "restore", "--native-usb", "--port", "tcp:127.0.0.1:12345", "--yes"], b"direct serial")):
            process = await asyncio.create_subprocess_exec(binary, *args, stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.PIPE)
            output, errors = await asyncio.wait_for(process.communicate(), 10)
            self.assertNotEqual(process.returncode, 0)
            self.assertIn(message, errors)
        self.assertFalse(self.factory.ports)

    @unittest.skipUnless(os.environ.get("CHAMELEON_BRIDGE_TEST_BINARY"), "set path to a no-cgo CLI binary")
    async def test_web_usb_management_reuses_session_and_tracks_background_task(self):
        self.factory.configure = configure_info_device
        with tempfile.TemporaryDirectory() as directory:
            key = Path(directory)/"bridge.key"; key.write_text(self.token); key.chmod(0o600)
            process = await asyncio.create_subprocess_exec(os.path.abspath(os.environ["CHAMELEON_BRIDGE_TEST_BINARY"]),
                "serve", "--addr", "127.0.0.1:0", "--usb-bridge", "http://"+self.api.authority,
                "--bridge-token-file", str(key), "--timeout", "2s",
                stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.PIPE)
            try:
                line = await asyncio.wait_for(process.stdout.readline(), 5)
                match = re.search(rb"http://([^/]+)/", line); self.assertIsNotNone(match, line)
                base = "http://"+match[1].decode()
                opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
                token, session = "", ""
                def request(path, body=None):
                    data = None if body is None else json.dumps(body).encode()
                    req = urllib.request.Request(base+path, data=data, headers={"Content-Type":"application/json", "X-Chameleon-Token":token, "X-Chameleon-Session":session})
                    with opener.open(req, timeout=5) as response:
                        return json.load(response)
                async def api(*args): return await asyncio.to_thread(request, *args)
                caps = await api("/api/capabilities"); token = caps["csrf_token"]
                self.assertTrue(caps["usb_bridge_enabled"]); self.assertFalse(caps["ble_bridge_enabled"])
                initial = await api("/api/session/status"); session = initial["session_id"]
                self.assertEqual(initial["kind"], "usb-bridge")
                await api("/api/bridge/scan", {})
                await api("/api/bridge/select", {"device_id":"COM7"})
                await api("/api/bridge/connect", {})
                connected = await api("/api/session/status"); session = connected["session_id"]
                self.assertNotEqual(session, initial["session_id"])
                for _ in range(2):
                    info = await api("/api/info")
                    self.assertEqual(info["transport"], transport_info(True))
                task = await api("/api/tasks", {"request_id":"usb-info-background-01", "session_id":session, "path":"/api/info"})
                async with asyncio.timeout(5):
                    while task["state"] not in ("succeeded", "failed", "interrupted"):
                        await asyncio.sleep(0.01)
                        task = await api("/api/tasks/"+task["id"])
                self.assertEqual(task["state"], "succeeded", task)
                self.assertEqual(task["result"]["transport"], transport_info(True))
                self.assertEqual(len(self.factory.ports), 1)
                await api("/api/bridge/disconnect", {})
                final = await api("/api/session/status")
                self.assertFalse(final["connected"]); self.assertNotEqual(session, final["session_id"])
                self.assertTrue(self.factory.ports[0].closed.is_set())
                self.assertNotIn(self.token, json.dumps(caps)+json.dumps(info))
            finally:
                if process.returncode is None: process.terminate()
                await asyncio.wait_for(process.communicate(), 5)


class SerialManagementOptionsTests(unittest.IsolatedAsyncioTestCase):
    async def test_managed_usb_startup_does_not_import_bleak(self):
        args = bridge.parser().parse_args(["--usb", "--control-port", "12346", "--token-file", "key"])
        with mock.patch.dict(sys.modules, {"bleak": None}), mock.patch("serial_management.serve_managed_serial", new_callable=mock.AsyncMock) as serve:
            await bridge.serve(args)
            serve.assert_awaited_once()


    def test_usb_requires_management_and_does_not_accept_ble_pairing(self):
        for options in (["--usb"], ["--usb", "--control-port", "12346"], ["--usb", "--control-port", "12346", "--token-file", "key", "--pair"]):
            with self.assertRaises(ValueError): bridge.validate_modes(bridge.parser().parse_args(options))
        bridge.validate_modes(bridge.parser().parse_args(["--usb", "--control-port", "12346", "--token-file", "key"]))
