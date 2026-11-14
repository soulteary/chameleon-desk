import asyncio
import contextlib
import json
import os
import re
import tempfile
import types
import unittest
from pathlib import Path
from unittest import mock
import urllib.request
import urllib.error

import bridge
from management import Controller, ManagementServer, load_token
from test_bridge import Factory, application_frame, eventually, configure_info_device


class ManagementTests(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.first = types.SimpleNamespace(address="first-id", name="ChameleonUltra")
        self.second = types.SimpleNamespace(address="second-id", name="ChameleonLite")
        advert = types.SimpleNamespace(local_name="ChameleonUltra", rssi=-42)
        self.scanner = types.SimpleNamespace(discover=mock.AsyncMock(return_value={
            "first-id": (self.first, advert), "second-id": (self.second, advert)}))
        self.factory = Factory()
        self.owner = Controller(self.scanner, self.factory, timeout=0.2)
        self.data = await asyncio.start_server(self.owner.bridge.accept, "127.0.0.1", 0)
        self.data_port = self.data.sockets[0].getsockname()[1]
        self.owner.data_address = f"127.0.0.1:{self.data_port}"
        self.token = "a" * 64
        self.api = ManagementServer(self.owner, self.token, "")
        self.control = await asyncio.start_server(self.api.accept, "127.0.0.1", 0, limit=8192)
        self.port = self.control.sockets[0].getsockname()[1]
        self.api.authority = f"127.0.0.1:{self.port}"
        self.writers = []

    async def asyncTearDown(self):
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

    async def open_request(self, path, body=None, method="POST", token=None, extra="", host=None):
        reader, writer = await asyncio.open_connection("127.0.0.1", self.port)
        self.writers.append(writer)
        payload = json.dumps(body if body is not None else {}).encode() if method == "POST" else b""
        token = self.token if token is None else token
        request = (f"{method} /v1/{path} HTTP/1.1\r\nHost: {host or self.api.authority}\r\n"
                   f"Authorization: Bearer {token}\r\nContent-Type: application/json\r\n"
                   f"Content-Length: {len(payload)}\r\n{extra}\r\n").encode() + payload
        writer.write(request)
        await writer.drain()
        return reader, writer

    async def response(self, reader):
        raw = await asyncio.wait_for(reader.read(), 3)
        header, body = raw.split(b"\r\n\r\n", 1)
        return int(header.split(b" ")[1]), json.loads(body)

    async def request(self, path, body=None, **kwargs):
        reader, _ = await self.open_request(path, body, **kwargs)
        return await self.response(reader)

    async def select(self):
        self.assertEqual((await self.request("scan"))[0], 200)
        self.assertEqual((await self.request("select", {"device_id": "first-id"}))[0], 200)

    async def connect(self):
        await self.select()
        status, body = await self.request("connect")
        self.assertEqual(status, 200, body)
        return body

    async def data_client(self):
        reader, writer = await asyncio.open_connection("127.0.0.1", self.data_port)
        self.writers.append(writer)
        line = await asyncio.wait_for(reader.readline(), 2)
        self.assertTrue(line.startswith(bridge.PREAMBLE))
        metadata = json.loads(line[len(bridge.PREAMBLE):])
        self.assertEqual(metadata["device_id"], "first-id")
        self.assertEqual(metadata["transport"], bridge.transport_info(True))
        return reader, writer

    async def test_scan_select_connect_transfer_status_disconnect(self):
        status = await self.connect()
        self.assertEqual(status["state"], "connected")
        self.assertFalse(status["transport"]["allows_device_restore"])
        reader, writer = await self.data_client()
        frame = application_frame(payload=b"query")
        writer.write(frame)
        await writer.drain()
        self.assertEqual(await asyncio.wait_for(reader.readexactly(len(frame)), 2), frame)
        code, status = await self.request("status", method="GET")
        self.assertEqual(code, 200)
        self.assertTrue(status["session_active"])
        code, status = await self.request("disconnect")
        self.assertEqual(code, 200)
        self.assertEqual(status["state"], "disconnected")
        self.assertEqual(await asyncio.wait_for(reader.read(), 2), b"")
        self.assertEqual(len(self.factory.clients), 1)
        self.assertTrue(self.factory.clients[0].disconnected.is_set())

    async def test_unselected_and_unknown_devices_are_rejected(self):
        self.assertEqual((await self.request("connect"))[0], 409)
        self.assertEqual((await self.request("select", {"device_id": "not-scanned"}))[0], 400)
        self.assertFalse(self.factory.clients)

    async def test_scan_and_selection_do_not_change_an_active_connection(self):
        await self.connect()
        self.assertEqual((await self.request("scan"))[0], 409)
        self.assertEqual((await self.request("select", {"device_id": "second-id"}))[0], 409)
        self.assertEqual((await self.request("connect"))[0], 409)
        self.assertEqual(self.owner.selected.address, "first-id")
        self.assertEqual(len(self.factory.clients), 1)

    async def test_disconnect_cancels_connection_setup(self):
        await self.select()
        gate = asyncio.Event()
        self.factory.configure = lambda c: setattr(c, "connect_gate", gate)
        connect = asyncio.create_task(self.request("connect"))
        await eventually(lambda: self.owner.state == "connecting")
        code, status = await self.request("disconnect")
        self.assertEqual(code, 200)
        self.assertEqual(status["state"], "disconnected")
        self.assertEqual((await connect)[0], 409)
        gate.set()
        self.assertFalse(self.factory.clients[0].writes)

    async def test_abandoned_connect_request_cleans_up(self):
        await self.select()
        self.factory.configure = lambda c: setattr(c, "connect_gate", asyncio.Event())
        _, writer = await self.open_request("connect")
        await eventually(lambda: self.owner.state == "connecting")
        writer.close()
        await eventually(lambda: self.owner.state == "disconnected")
        self.assertTrue(self.factory.clients[0].disconnected.is_set())

    async def test_abandoned_disconnect_still_finishes_ble_cleanup(self):
        await self.connect()
        reader, _ = await self.data_client()
        gate = asyncio.Event()
        client = self.factory.clients[0]
        client.disconnect_gate = gate
        _, writer = await self.open_request("disconnect")
        await asyncio.wait_for(client.disconnected.wait(), 2)
        writer.close()
        await writer.wait_closed()
        await asyncio.sleep(0.01)
        gate.set()
        await eventually(lambda: self.owner.state == "disconnected" and not self.owner.bridge.active)
        self.assertFalse(self.owner.bridge.blocked)
        self.assertEqual(await asyncio.wait_for(reader.read(), 2), b"")

    async def test_client_construction_failure_does_not_leave_connecting_state(self):
        await self.select()
        self.owner.factory = mock.Mock(side_effect=RuntimeError("backend unavailable"))
        self.assertEqual((await self.request("connect"))[0], 502)
        self.assertEqual(self.owner.state, "disconnected")
        self.assertIsNone(self.owner.link)

    async def test_data_close_requires_explicit_reconnect(self):
        await self.connect()
        _, writer = await self.data_client()
        writer.close()
        await eventually(lambda: self.owner.state == "disconnected" and not self.owner.bridge.active)
        reader, writer = await asyncio.open_connection("127.0.0.1", self.data_port)
        self.writers.append(writer)
        self.assertEqual(await asyncio.wait_for(reader.read(), 2), b"")
        self.assertEqual(len(self.factory.clients), 1)

    async def test_old_callbacks_do_not_affect_a_new_connection(self):
        await self.connect()
        old = self.factory.clients[0]
        await self.request("disconnect")
        self.assertEqual((await self.request("connect"))[0], 200)
        old.disconnected_callback(old)
        old.emit(b"stale")
        self.assertEqual(self.owner.state, "connected")

    async def test_disconnect_failure_blocks_further_connections(self):
        await self.connect()
        self.factory.clients[0].disconnect_error = True
        _, status = await self.request("disconnect")
        self.assertEqual(status["state"], "blocked")
        self.assertEqual((await self.request("connect"))[0], 409)

    async def test_auth_origin_host_and_ambiguous_framing_are_rejected(self):
        for options, expected in [
            ({"token": "wrong"}, 401),
            ({"extra": "Origin: http://evil.example\r\n"}, 403),
            ({"host": "evil.example"}, 403),
            ({"extra": "Content-Length: 2\r\n"}, 400),
            ({"extra": "Transfer-Encoding: chunked\r\n"}, 400),
        ]:
            with self.subTest(options=options):
                self.assertEqual((await self.request("scan", **options))[0], expected)
        self.scanner.discover.assert_not_awaited()

    async def test_methods_fields_and_paths_are_checked(self):
        self.assertEqual((await self.request("scan", method="GET"))[0], 405)
        self.assertEqual((await self.request("scan", {"unexpected": True}))[0], 400)
        self.assertEqual((await self.request("unknown"))[0], 404)
        self.assertEqual((await self.request("select", {"device_id": []}))[0], 400)

    async def test_raw_pairing_commands_are_blocked_before_gatt_write(self):
        for command in sorted(bridge.RESTRICTED_COMMANDS):
            with self.subTest(command=command):
                await self.connect()
                reader, writer = await self.data_client()
                client = self.factory.clients[-1]
                frame = application_frame(command, b"123456")
                writer.write(frame[:6])
                await writer.drain()
                await asyncio.sleep(0.01)
                self.assertFalse(client.writes)
                writer.write(frame[6:])
                await writer.drain()
                self.assertEqual(await asyncio.wait_for(reader.read(), 2), b"")
                await eventually(lambda: not self.owner.bridge.active)
                self.assertFalse(client.writes)

    @unittest.skipUnless(os.environ.get("CHAMELEON_BRIDGE_TEST_BINARY"), "set path to a CLI binary")
    async def test_desk_cli_scans_through_managed_bridge(self):
        with tempfile.TemporaryDirectory() as directory:
            key = Path(directory)/"bridge.key"
            key.write_text(self.token)
            key.chmod(0o600)
            process = await asyncio.create_subprocess_exec(
                os.path.abspath(os.environ["CHAMELEON_BRIDGE_TEST_BINARY"]),
                "ble", "scan", "--ble-bridge", "http://" + self.api.authority,
                "--bridge-token-file", str(key),
                stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.PIPE)
            output, errors = await asyncio.wait_for(process.communicate(), 10)
            self.assertEqual(process.returncode, 0, errors)
            scan = json.loads(output)
            self.assertEqual(scan["api_version"], 1)
            self.assertEqual(scan["devices"][0]["id"], "first-id")
            self.assertNotIn(self.token.encode(), output + errors)
            self.assertFalse(self.factory.clients)
            self.assertEqual(self.owner.state, "disconnected")

    @unittest.skipUnless(os.environ.get("CHAMELEON_BRIDGE_TEST_BINARY"), "set path to a CLI binary")
    async def test_desk_native_addresses_have_migration_errors(self):
        binary = os.path.abspath(os.environ["CHAMELEON_BRIDGE_TEST_BINARY"])
        for args, message in [
            (["info", "--port", "ble:old-peripheral"], b"native BLE has been removed"),
            (["ble", "scan"], b"--bridge-token-file"),
            (["firmware", "flash", "--port", "ble:old-peripheral", "--input", "missing.zip", "--yes"], b"USB DFU serial"),
        ]:
            with self.subTest(args=args):
                process = await asyncio.create_subprocess_exec(
                    binary, *args, stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.PIPE)
                output, errors = await asyncio.wait_for(process.communicate(), 10)
                self.assertNotEqual(process.returncode, 0)
                self.assertIn(message, errors)
        self.assertFalse(self.factory.clients)

    @unittest.skipUnless(os.environ.get("CHAMELEON_BRIDGE_TEST_BINARY"), "set path to a cgo-disabled CLI binary")
    async def test_desk_web_manages_and_reuses_a_ble_session(self):
        self.factory.configure = configure_info_device
        with tempfile.TemporaryDirectory() as directory:
            key = Path(directory)/"bridge.key"
            key.write_text(self.token)
            key.chmod(0o600)
            process = await asyncio.create_subprocess_exec(
                os.path.abspath(os.environ["CHAMELEON_BRIDGE_TEST_BINARY"]),
                "serve", "--addr", "127.0.0.1:0", "--ble-bridge", "http://" + self.api.authority,
                "--bridge-token-file", str(key), "--timeout", "2s",
                stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.PIPE)
            try:
                line = await asyncio.wait_for(process.stdout.readline(), 5)
                match = re.search(rb"http://([^/]+)/", line)
                self.assertIsNotNone(match, line)
                base = "http://" + match[1].decode()
                opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))

                def request(path, body=None, token=""):
                    data = None if body is None else json.dumps(body).encode()
                    headers = {"Content-Type": "application/json", "X-Chameleon-Token": token}
                    req = urllib.request.Request(base + path, data=data, headers=headers)
                    try:
                        with opener.open(req, timeout=5) as response:
                            return response.status, json.load(response)
                    except urllib.error.HTTPError as error:
                        content = error.read().decode()
                        return error.code, json.loads(content) if "application/json" in error.headers.get("Content-Type", "") else content

                async def api(path, body=None, token=""):
                    return await asyncio.to_thread(request, path, body, token)

                _, caps = await api("/api/capabilities")
                self.assertTrue(caps["ble_bridge_enabled"])
                self.assertFalse(caps["writes_enabled"])
                self.assertNotIn(self.token, json.dumps(caps))
                token = caps["csrf_token"]
                self.assertEqual((await api("/api/bridge/scan", {}))[0], 403)
                self.assertEqual((await api("/api/bridge/scan", {}, token))[0], 200)
                self.assertEqual((await api("/api/bridge/select", {"device_id": "first-id"}, token))[0], 200)
                code, connected = await api("/api/bridge/connect", {}, token)
                self.assertEqual(code, 200, connected)
                self.assertTrue(connected["session_active"])
                for _ in range(2):
                    code, info = await api("/api/info")
                    self.assertEqual(code, 200, info)
                    self.assertEqual(info["git_version"], "test-bridge")
                    self.assertEqual(info["transport"]["medium"], "ble")
                    self.assertFalse(info["transport"]["allows_device_restore"])
                self.assertEqual(len(self.factory.clients), 1)
                self.assertEqual(len(self.factory.clients[0].writes), 18)
                self.assertEqual((await api("/api/bridge/disconnect", {}, token))[0], 200)
                self.assertEqual((await api("/api/info"))[0], 502)
                self.assertTrue(self.factory.clients[0].disconnected.is_set())
            finally:
                if process.returncode is None:
                    process.terminate()
                await asyncio.wait_for(process.communicate(), 5)


class TokenTests(unittest.TestCase):
    def test_token_is_created_once_and_remains_private(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory)/"bridge.key"
            token = load_token(path)
            self.assertEqual(len(token), 64)
            self.assertEqual(load_token(path), token)
            if os.name == "posix":
                self.assertEqual(path.stat().st_mode & 0o777, 0o600)
                path.chmod(0o644)
                with self.assertRaises(ValueError):
                    load_token(path)

    def test_invalid_token_is_not_overwritten(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory)/"bridge.key"
            path.write_text("not-a-token")
            path.chmod(0o600)
            with self.assertRaises(ValueError):
                load_token(path)
            self.assertEqual(path.read_text(), "not-a-token")
