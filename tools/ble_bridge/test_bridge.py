"""Offline bridge tests. No Bleak import, adapter or RFID card is required."""

import argparse
import asyncio
import contextlib
import os
import struct
import subprocess
import sys
import types
import unittest
from unittest import mock

import bridge


class Characteristic:
    def __init__(self, properties, maximum=20):
        self.properties = properties
        self.max_write_without_response_size = maximum


class Service:
    def __init__(self, response=True, maximum=20):
        self.rx = Characteristic(["write" if response else "write-without-response"], maximum)
        self.tx = Characteristic(["notify"])

    def get_characteristic(self, uuid):
        return {bridge.NUS_RX: self.rx, bridge.NUS_TX: self.tx}.get(uuid)


class FakeClient:
    def __init__(self, device, *, disconnected_callback, pair, timeout):
        self.disconnected_callback = disconnected_callback
        self.pair = pair
        self.timeout = timeout
        self.service = Service()
        self.services = self
        self.connected = asyncio.Event()
        self.subscribed = asyncio.Event()
        self.disconnected = asyncio.Event()
        self.notification = None
        self.writes = []
        self.handler = None
        self.connect_gate = None
        self.write_gate = None
        self.disconnect_error = False
        self.disconnect_gate = None
        self.has_service = True

    def get_service(self, uuid):
        assert uuid == bridge.NUS_SERVICE
        return self.service if self.has_service else None

    async def connect(self):
        if self.connect_gate:
            await self.connect_gate.wait()
        self.connected.set()

    async def start_notify(self, characteristic, callback):
        assert characteristic is self.service.tx
        self.notification = callback
        self.subscribed.set()

    async def write_gatt_char(self, characteristic, data, *, response):
        assert self.subscribed.is_set()
        assert characteristic is self.service.rx
        self.writes.append((bytes(data), response))
        if self.write_gate:
            await self.write_gate.wait()
        if self.handler:
            self.handler(bytes(data))
        else:
            # Notification boundaries intentionally differ from TCP/GATT writes.
            self.emit(data[:1])
            self.emit(data[1:])

    def emit(self, data):
        if self.notification:
            self.notification(self.service.tx, bytearray(data))

    async def disconnect(self):
        self.disconnected.set()
        if self.disconnect_gate:
            await self.disconnect_gate.wait()
        if self.disconnect_error:
            raise RuntimeError("test disconnect failure")
        self.disconnected_callback(self)


class Factory:
    def __init__(self):
        self.clients = []
        self.configure = lambda client: None

    def __call__(self, *args, **kwargs):
        client = FakeClient(*args, **kwargs)
        self.configure(client)
        self.clients.append(client)
        return client


async def eventually(predicate):
    async with asyncio.timeout(2):
        while not predicate():
            await asyncio.sleep(0.001)


class BridgeTests(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.factory = Factory()
        self.bridge = bridge.Bridge("test-device", self.factory, timeout=0.2)
        self.server = await asyncio.start_server(self.bridge.accept, "127.0.0.1", 0)
        self.port = self.server.sockets[0].getsockname()[1]
        self.writers = []

    async def asyncTearDown(self):
        self.server.close()
        await self.bridge.close()
        await self.server.wait_closed()
        for writer in self.writers:
            writer.close()
            with contextlib.suppress(Exception):
                await writer.wait_closed()

    async def connect(self):
        reader, writer = await asyncio.open_connection("127.0.0.1", self.port)
        self.writers.append(writer)
        return reader, writer

    async def client(self, count=1):
        await eventually(lambda: len(self.factory.clients) >= count and self.factory.clients[count - 1].subscribed.is_set())
        return self.factory.clients[count - 1]

    async def test_binary_stream_fragmentation_and_coalescing(self):
        reader, writer = await self.connect()
        client = await self.client()
        payload = bytes(range(256)) * 3
        for data in (payload[:3], payload[3:117], payload[117:]):
            writer.write(data)
            await writer.drain()
            await asyncio.sleep(0)
        self.assertEqual(await asyncio.wait_for(reader.readexactly(len(payload)), 2), payload)
        self.assertEqual(b"".join(data for data, _ in client.writes), payload)
        self.assertTrue(all(0 < len(data) <= 20 and response for data, response in client.writes))

    async def test_without_response_uses_characteristic_write_limit(self):
        self.factory.configure = lambda c: setattr(c, "service", Service(response=False, maximum=7))
        reader, writer = await self.connect()
        client = await self.client()
        writer.write(b"abcdefghijklmnopqrstuvw")
        await writer.drain()
        self.assertEqual(await asyncio.wait_for(reader.readexactly(23), 2), b"abcdefghijklmnopqrstuvw")
        self.assertTrue(all(len(data) <= 7 and not response for data, response in client.writes))

    async def test_second_client_is_closed_without_touching_ble(self):
        reader, writer = await self.connect()
        await self.client()
        other, _ = await self.connect()
        self.assertEqual(await asyncio.wait_for(other.read(), 2), b"")
        self.assertEqual(len(self.factory.clients), 1)
        writer.write(b"still active")
        await writer.drain()
        self.assertEqual(await asyncio.wait_for(reader.readexactly(12), 2), b"still active")

    async def test_tcp_eof_disconnects_and_old_notifications_are_ignored(self):
        _, writer = await self.connect()
        old = await self.client()
        writer.close()
        await eventually(lambda: old.disconnected.is_set() and not self.bridge.active)
        reader, current_writer = await self.connect()
        current = await self.client(2)
        self.assertIsNot(old, current)
        old.emit(b"stale data")
        current_writer.write(b"current")
        await current_writer.drain()
        self.assertEqual(await asyncio.wait_for(reader.readexactly(7), 2), b"current")

    async def test_ble_disconnect_closes_tcp(self):
        reader, _ = await self.connect()
        client = await self.client()
        client.disconnected_callback(client)
        self.assertEqual(await asyncio.wait_for(reader.read(), 2), b"")
        await eventually(lambda: not self.bridge.active)

    async def test_notification_overflow_ends_session_instead_of_dropping_bytes(self):
        reader, _ = await self.connect()
        client = await self.client()
        client.emit(b"x" * (bridge.BUFFER_LIMIT + 1))
        self.assertEqual(await asyncio.wait_for(reader.read(), 2), b"")
        await eventually(lambda: not self.bridge.active)

    async def test_accumulated_notifications_are_bounded(self):
        reader, _ = await self.connect()
        client = await self.client()
        # No event-loop yield: the callback queue itself must remain bounded.
        for _ in range(bridge.BUFFER_LIMIT // 20 + 1):
            client.emit(b"x" * 20)
        await asyncio.wait_for(reader.read(), 2)
        await eventually(lambda: not self.bridge.active)
        self.assertTrue(client.disconnected.is_set())

    async def test_write_timeout_does_not_retry(self):
        self.factory.configure = lambda c: setattr(c, "write_gate", asyncio.Event())
        reader, writer = await self.connect()
        client = await self.client()
        writer.write(b"write once")
        await writer.drain()
        self.assertEqual(await asyncio.wait_for(reader.read(), 2), b"")
        self.assertEqual(client.writes, [(b"write once", True)])
        self.assertTrue(client.disconnected.is_set())

    async def test_client_eof_cancels_pending_ble_connection(self):
        self.factory.configure = lambda c: setattr(c, "connect_gate", asyncio.Event())
        _, writer = await self.connect()
        await eventually(lambda: len(self.factory.clients) == 1)
        writer.close()
        await eventually(lambda: not self.bridge.active)
        self.assertTrue(self.factory.clients[0].disconnected.is_set())

    async def test_eof_discards_bytes_queued_during_connection_setup(self):
        gate = asyncio.Event()
        self.factory.configure = lambda c: setattr(c, "connect_gate", gate)
        _, writer = await self.connect()
        await eventually(lambda: len(self.factory.clients) == 1)
        writer.write(b"do not send after the caller has timed out")
        await writer.drain()
        writer.close()
        await eventually(lambda: not self.bridge.active)
        gate.set()
        self.assertFalse(self.factory.clients[0].writes)
        self.assertTrue(self.factory.clients[0].disconnected.is_set())

    async def test_tcp_input_is_bounded_while_connecting(self):
        self.factory.configure = lambda c: setattr(c, "connect_gate", asyncio.Event())
        reader, writer = await self.connect()
        writer.write(b"x" * (bridge.BUFFER_LIMIT + 4096))
        await writer.drain()
        self.assertEqual(await asyncio.wait_for(reader.read(), 2), b"")
        await eventually(lambda: not self.bridge.active)
        self.assertFalse(self.factory.clients[0].writes)

    async def test_connection_timeout_releases_session(self):
        self.factory.configure = lambda c: setattr(c, "connect_gate", asyncio.Event())
        reader, _ = await self.connect()
        self.assertEqual(await asyncio.wait_for(reader.read(), 2), b"")
        await eventually(lambda: not self.bridge.active)
        self.assertFalse(self.bridge.blocked)

    async def test_missing_nus_fails_before_writing(self):
        self.factory.configure = lambda c: setattr(c, "has_service", False)
        reader, writer = await self.connect()
        writer.write(b"not for another service")
        await writer.drain()
        self.assertEqual(await asyncio.wait_for(reader.read(), 2), b"")
        self.assertFalse(self.factory.clients[0].writes)

    async def test_connect_error_closes_tcp_and_disconnects(self):
        def configure(client):
            client.connect = mock.AsyncMock(side_effect=RuntimeError("connection refused"))
        self.factory.configure = configure
        reader, _ = await self.connect()
        self.assertEqual(await asyncio.wait_for(reader.read(), 2), b"")
        await eventually(lambda: not self.bridge.active)
        self.assertTrue(self.factory.clients[0].disconnected.is_set())

    async def test_notification_setup_failure_does_not_write(self):
        def configure(client):
            client.start_notify = mock.AsyncMock(side_effect=RuntimeError("subscribe failed"))
        self.factory.configure = configure
        reader, writer = await self.connect()
        writer.write(b"do not send before subscription")
        await writer.drain()
        self.assertEqual(await asyncio.wait_for(reader.read(), 2), b"")
        self.assertFalse(self.factory.clients[0].writes)

    async def test_write_error_does_not_retry(self):
        def configure(client):
            client.write_gatt_char = mock.AsyncMock(side_effect=RuntimeError("write failed"))
        self.factory.configure = configure
        reader, writer = await self.connect()
        client = await self.client()
        writer.write(b"once")
        await writer.drain()
        self.assertEqual(await asyncio.wait_for(reader.read(), 2), b"")
        self.assertEqual(client.write_gatt_char.await_count, 1)

    async def test_tcp_output_timeout_disconnects(self):
        reader, _ = await self.connect()
        client = await self.client()
        gate = asyncio.Event()
        with mock.patch.object(asyncio.StreamWriter, "drain", side_effect=gate.wait):
            client.emit(b"response")
            self.assertEqual(await asyncio.wait_for(reader.readexactly(8), 2), b"response")
            self.assertEqual(await asyncio.wait_for(reader.read(), 2), b"")
        self.assertTrue(client.disconnected.is_set())

    async def test_cleanup_failure_blocks_new_sessions(self):
        self.factory.configure = lambda c: setattr(c, "disconnect_error", True)
        _, writer = await self.connect()
        await self.client()
        writer.close()
        await eventually(lambda: not self.bridge.active)
        self.assertTrue(self.bridge.blocked)
        reader, _ = await self.connect()
        self.assertEqual(await asyncio.wait_for(reader.read(), 2), b"")
        self.assertEqual(len(self.factory.clients), 1)

    async def test_cleanup_timeout_keeps_other_clients_out(self):
        self.factory.configure = lambda c: setattr(c, "disconnect_gate", asyncio.Event())
        _, writer = await self.connect()
        client = await self.client()
        writer.close()
        await asyncio.wait_for(client.disconnected.wait(), 2)
        reader, _ = await self.connect()
        self.assertEqual(await asyncio.wait_for(reader.read(), 2), b"")
        await eventually(lambda: not self.bridge.active)
        self.assertTrue(self.bridge.blocked)
        self.assertEqual(len(self.factory.clients), 1)

    async def test_shutdown_disconnects_active_session(self):
        reader, _ = await self.connect()
        client = await self.client()
        await self.bridge.close()
        self.assertEqual(await asyncio.wait_for(reader.read(), 2), b"")
        self.assertTrue(client.disconnected.is_set())
        self.assertFalse(self.bridge.tasks)

    async def test_shutdown_before_session_task_starts_closes_socket(self):
        writer = mock.Mock()
        writer.wait_closed = mock.AsyncMock()
        self.bridge.accept(mock.Mock(), writer)
        await self.bridge.close()
        writer.close.assert_called()
        writer.wait_closed.assert_awaited_once()
        self.assertFalse(self.bridge.active)
        self.assertFalse(self.factory.clients)

    async def test_serve_shutdown_closes_clients_before_waiting_for_server(self):
        started = asyncio.Event()
        servers = []
        original_start_server = asyncio.start_server

        async def start_server(*args, **kwargs):
            server = await original_start_server(*args, **kwargs)
            servers.append(server)
            started.set()
            return server

        scanner = types.SimpleNamespace(find_device_by_address=mock.AsyncMock(return_value="device"))
        fake_bleak = types.SimpleNamespace(BleakClient=self.factory, BleakScanner=scanner)
        args = argparse.Namespace(device="device", scan=False, timeout=0.2,
                                  pair=False, listen="127.0.0.1", port=0)
        with mock.patch.dict(sys.modules, {"bleak": fake_bleak}), \
                mock.patch.object(asyncio, "start_server", side_effect=start_server):
            task = asyncio.create_task(bridge.serve(args))
            try:
                await asyncio.wait_for(started.wait(), 2)
                reader, writer = await asyncio.open_connection("127.0.0.1", servers[0].sockets[0].getsockname()[1])
                self.writers.append(writer)
                client = await self.client()
                task.cancel()
                with self.assertRaises(asyncio.CancelledError):
                    await asyncio.wait_for(task, 2)
                self.assertEqual(await asyncio.wait_for(reader.read(), 2), b"")
                self.assertTrue(client.disconnected.is_set())
            finally:
                task.cancel()
                await asyncio.gather(task, return_exceptions=True)

    async def test_scan_lists_advertised_identifier_without_connecting(self):
        device = types.SimpleNamespace(address="device-id", name="fallback")
        advert = types.SimpleNamespace(local_name="ChameleonUltra", rssi=-55)
        scanner = types.SimpleNamespace(discover=mock.AsyncMock(return_value={"device-id": (device, advert)}))
        fake_bleak = types.SimpleNamespace(BleakClient=self.factory, BleakScanner=scanner)
        args = argparse.Namespace(scan=True, timeout=1)
        with mock.patch.dict(sys.modules, {"bleak": fake_bleak}), mock.patch("builtins.print") as output:
            await bridge.serve(args)
        scanner.discover.assert_awaited_once_with(timeout=1, return_adv=True,
                                                 service_uuids=[bridge.NUS_SERVICE])
        output.assert_called_once_with("device-id\tChameleonUltra\t-55 dBm")
        self.assertFalse(self.factory.clients)

    @unittest.skipUnless(os.environ.get("CHAMELEON_BRIDGE_TEST_BINARY"), "set path to a CGO_ENABLED=0 CLI binary")
    async def test_desk_cli_uses_tcp_bridge(self):
        def configure(client):
            incoming = bytearray()

            def respond(data):
                incoming.extend(data)
                while len(incoming) >= 10:
                    length = int.from_bytes(incoming[6:8], "big") + 10
                    if len(incoming) < length:
                        return
                    request = bytes(incoming[:length])
                    del incoming[:length]
                    command = int.from_bytes(request[2:4], "big")
                    payloads = {
                        1000: b"\x02\x00", 1033: b"\x00", 1017: b"test-bridge",
                        1035: b"\x03\xe8", 1002: b"\x00", 1025: b"\x0e\x74\x50",
                        1018: b"\x00", 1019: b"\x03\xe9\x00\x64" * 8,
                        1023: b"\x01\x01" * 8,
                    }
                    payload = payloads[command]
                    header = b"\x11\xef" + struct.pack(">HHH", command, 0x68, len(payload))
                    reply = header + bytes([-sum(header) % 256]) + payload + bytes([-sum(payload) % 256])
                    for start in range(0, len(reply), 3):
                        client.emit(reply[start:start + 3])
            client.handler = respond
        self.factory.configure = configure
        process = await asyncio.create_subprocess_exec(
            os.path.abspath(os.environ["CHAMELEON_BRIDGE_TEST_BINARY"]),
            "info", "--port", f"tcp:127.0.0.1:{self.port}", "--json",
            stdout=subprocess.PIPE, stderr=subprocess.PIPE,
        )
        try:
            stdout, stderr = await asyncio.wait_for(process.communicate(), 10)
        except BaseException:
            if process.returncode is None:
                process.kill()
                await process.wait()
            raise
        self.assertEqual(process.returncode, 0, stderr.decode())
        import json
        info = json.loads(stdout)
        self.assertEqual(info["git_version"], "test-bridge")
        self.assertEqual(len(info["slots"]), 8)


class ValidationTests(unittest.TestCase):
    def test_loopback_only(self):
        for value in ("127.0.0.1", "::1"):
            self.assertEqual(bridge.loopback(value), value)
        for value in ("0.0.0.0", "::", "192.0.2.1", "localhost", "invalid"):
            with self.assertRaises(argparse.ArgumentTypeError):
                bridge.loopback(value)

    def test_timeouts_and_ports(self):
        for value in ("0", "-1", "nan", "inf", "wrong"):
            with self.assertRaises(argparse.ArgumentTypeError):
                bridge.positive_timeout(value)
        for value in ("0", "65536", "-1", "wrong"):
            with self.assertRaises(argparse.ArgumentTypeError):
                bridge.port_number(value)
        self.assertEqual(bridge.positive_timeout("0.5"), 0.5)
        self.assertEqual(bridge.port_number("12345"), 12345)


if __name__ == "__main__":
    unittest.main()
