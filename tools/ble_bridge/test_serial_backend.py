"""Offline USB backend tests; fake ports never access USB hardware."""

import argparse
import asyncio
import contextlib
import json
import os
import queue
import re
import urllib.request
import subprocess
import sys
import threading
import types
import unittest
from unittest import mock

import bridge
import serial_backend
from test_bridge import application_frame, configure_info_device, eventually


class FakePort:
    def __init__(self, **options):
        self.options = options
        self.thread = threading.current_thread()
        self.port = None
        self.dtr = False
        self.rts = False
        self.write_timeout = options["write_timeout"]
        self.open_gate = self.write_gate = self.close_gate = None
        self.open_error = self.write_error = self.close_error = False
        self.partial = None
        self.opened = threading.Event()
        self.closed = threading.Event()
        self.input_reset = False
        self.writes = []
        self.writing = threading.Event()
        self.reads = queue.Queue()
        self.handler = None
        self.call_threads = set()

    def record(self):
        self.call_threads.add(threading.current_thread().ident)

    def open(self):
        self.record()
        if self.open_gate:
            self.open_gate.wait()
        if self.open_error:
            raise OSError("open failed")
        self.opened.set()

    def reset_input_buffer(self):
        self.record()
        self.input_reset = True

    def emit(self, data):
        if data:
            self.reads.put(bytes(data))

    def read(self, size):
        self.record()
        try:
            value = self.reads.get(timeout=serial_backend.POLL_SECONDS)
        except queue.Empty:
            return b""
        if isinstance(value, Exception):
            raise value
        return value

    def write(self, data):
        self.record()
        self.writing.set()
        if self.write_gate:
            self.write_gate.wait()
        if self.write_error:
            raise OSError("backend exception must not expose secret payload")
        data = bytes(data[:self.partial]) if self.partial else bytes(data)
        self.writes.append(data)
        if self.handler:
            self.handler(data)
        else:
            self.emit(data)
        return len(data)

    def close(self):
        self.record()
        if self.close_gate:
            self.close_gate.wait()
        self.closed.set()
        if self.close_error:
            raise OSError("close failed")


class Factory:
    def __init__(self):
        self.ports = []
        self.configure = lambda port: None

    def __call__(self, **options):
        port = FakePort(**options)
        self.configure(port)
        self.ports.append(port)
        return port


class SerialTests(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.factory = Factory()
        self.bridge = serial_backend.SerialBridge("explicit-port", self.factory, timeout=0.25)
        self.server = await asyncio.start_server(self.bridge.accept, "127.0.0.1", 0)
        self.port = self.server.sockets[0].getsockname()[1]
        self.writers = []

    async def asyncTearDown(self):
        for port in self.factory.ports:
            for gate in (port.open_gate, port.write_gate, port.close_gate):
                if gate:
                    gate.set()
        self.server.close()
        await self.bridge.close()
        await self.server.wait_closed()
        for writer in self.writers:
            writer.close()
            with contextlib.suppress(Exception):
                await writer.wait_closed()
        for port in self.factory.ports:
            await asyncio.to_thread(port.thread.join, 1)
            self.assertFalse(port.thread.is_alive())

    async def connect(self):
        reader, writer = await asyncio.open_connection("127.0.0.1", self.port)
        self.writers.append(writer)
        line = await asyncio.wait_for(reader.readline(), 2)
        return reader, writer, line

    async def opened(self, count=1):
        await eventually(lambda: len(self.factory.ports) >= count and self.bridge.session.ready.is_set())
        return self.factory.ports[count - 1]

    async def test_setup_fragmented_frames_partial_writes_and_thread_ownership(self):
        self.factory.configure = lambda port: setattr(port, "partial", 5)
        reader, writer, line = await self.connect()
        info = json.loads(line[len(bridge.PREAMBLE):])
        self.assertEqual(info["transport"], serial_backend.transport_info())
        port = await self.opened()
        self.assertEqual(port.port, "explicit-port")
        self.assertTrue(port.dtr and port.rts and port.input_reset)
        self.assertEqual(port.options["baudrate"], 115200)
        self.assertFalse(port.options["rtscts"])
        frames = application_frame(payload=bytes(range(256))) + application_frame(1002)
        for part in (frames[:2], frames[2:7], frames[7:]):
            writer.write(part)
            await writer.drain()
        self.assertEqual(await asyncio.wait_for(reader.readexactly(len(frames)), 2), frames)
        self.assertEqual(b"".join(port.writes), frames)
        writer.close()
        await eventually(lambda: not self.bridge.active)
        self.assertTrue(port.closed.is_set())
        self.assertEqual(port.call_threads, {port.thread.ident})

    async def test_restricted_commands_and_nonapplication_frames_never_reach_serial(self):
        for command in serial_backend.USB_RESTRICTED_COMMANDS:
            count = len(self.factory.ports) + 1
            reader, writer, _ = await self.connect()
            port = await self.opened(count)
            frame = application_frame(command, b"123456")
            writer.write(frame[:9])
            await writer.drain()
            await asyncio.sleep(0.005)
            self.assertFalse(port.writes)
            writer.write(frame[9:])
            await writer.drain()
            self.assertEqual(await asyncio.wait_for(reader.read(), 2), b"")
            await eventually(lambda: not self.bridge.active)
            self.assertFalse(port.writes)
        for frame in (b"\xc0" * 16, application_frame()[:-1] + b"\xff",
                      application_frame(payload=b"x" * 4097)):
            count = len(self.factory.ports) + 1
            reader, writer, _ = await self.connect()
            port = await self.opened(count)
            writer.write(frame)
            await writer.drain()
            self.assertEqual(await asyncio.wait_for(reader.read(), 2), b"")
            await eventually(lambda: not self.bridge.active)
            self.assertFalse(port.writes)

    async def test_exclusive_connection_and_reconnect_do_not_reuse_old_bytes(self):
        reader, writer, _ = await self.connect()
        first = await self.opened()
        other, _, line = await self.connect()
        self.assertEqual(line, b"")
        self.assertEqual(await other.read(), b"")
        self.assertEqual(len(self.factory.ports), 1)
        writer.write(application_frame()[:7])
        await writer.drain()
        writer.close()
        await eventually(lambda: not self.bridge.active)
        reader, writer, _ = await self.connect()
        second = await self.opened(2)
        first.emit(b"old-response")
        frame = application_frame(1002)
        writer.write(frame)
        await writer.drain()
        self.assertEqual(await asyncio.wait_for(reader.readexactly(len(frame)), 2), frame)
        self.assertEqual(b"".join(second.writes), frame)
        self.assertFalse(first.writes)

    async def test_write_failure_is_not_retried(self):
        self.factory.configure = lambda port: setattr(port, "write_error", True)
        reader, writer, _ = await self.connect()
        port = await self.opened()
        writer.write(application_frame(payload=b"secret"))
        await writer.drain()
        with self.assertLogs("usb_serial_bridge", level="WARNING") as logs:
            self.assertEqual(await asyncio.wait_for(reader.read(), 2), b"")
            await eventually(lambda: not self.bridge.active)
        self.assertNotIn("secret", " ".join(logs.output))
        self.assertFalse(port.writes)
        self.assertTrue(port.closed.is_set())

    async def test_error_after_short_write_does_not_replay_accepted_prefix(self):
        def configure(port):
            port.partial = 4
            port.handler = lambda _: setattr(port, "write_error", True)
        self.factory.configure = configure
        reader, writer, _ = await self.connect()
        port = await self.opened()
        frame = application_frame(payload=b"payload")
        writer.write(frame)
        await writer.drain()
        self.assertEqual(await asyncio.wait_for(reader.read(), 2), b"")
        await eventually(lambda: not self.bridge.active)
        self.assertEqual(port.writes, [frame[:4]])
        self.assertTrue(port.closed.is_set())

    async def test_unplug_closes_tcp_without_reconnecting(self):
        reader, _, _ = await self.connect()
        port = await self.opened()
        port.reads.put(OSError("unplug"))
        self.assertEqual(await asyncio.wait_for(reader.read(), 2), b"")
        await eventually(lambda: not self.bridge.active)
        self.assertEqual(len(self.factory.ports), 1)
        self.assertTrue(port.closed.is_set())

    async def test_open_error_closes_port_and_allows_explicit_retry(self):
        self.factory.configure = lambda port: setattr(port, "open_error", True)
        reader, _, _ = await self.connect()
        self.assertEqual(await asyncio.wait_for(reader.read(), 2), b"")
        await eventually(lambda: not self.bridge.active)
        self.assertTrue(self.factory.ports[0].closed.is_set())
        self.assertFalse(self.factory.ports[0].writes)
        self.assertFalse(self.bridge.blocked)
        self.factory.configure = lambda port: None
        reader, writer, _ = await self.connect()
        await self.opened(2)
        frame = application_frame()
        writer.write(frame)
        await writer.drain()
        self.assertEqual(await asyncio.wait_for(reader.readexactly(len(frame)), 2), frame)

    async def test_tcp_input_overflow_during_setup_never_reaches_serial(self):
        gate = threading.Event()
        self.factory.configure = lambda port: setattr(port, "open_gate", gate)
        reader, writer, _ = await self.connect()
        await eventually(lambda: bool(self.factory.ports))
        writer.write(b"x" * (bridge.BUFFER_LIMIT + 4096))
        await writer.drain()
        self.assertEqual(await asyncio.wait_for(reader.read(), 2), b"")
        await eventually(lambda: self.bridge.session.worker.stop.is_set())
        gate.set()
        await eventually(lambda: not self.bridge.active)
        self.assertFalse(self.factory.ports[0].writes)

    async def test_cleanup_failure_blocks_further_sessions(self):
        self.factory.configure = lambda port: setattr(port, "close_error", True)
        _, writer, _ = await self.connect()
        await self.opened()
        writer.close()
        await eventually(lambda: not self.bridge.active)
        self.assertTrue(self.bridge.blocked)
        _, _, line = await self.connect()
        self.assertEqual(line, b"")
        self.assertEqual(len(self.factory.ports), 1)

    async def test_late_open_is_closed_without_forwarding_after_timeout(self):
        gate = threading.Event()
        self.factory.configure = lambda port: setattr(port, "open_gate", gate)
        reader, writer, _ = await self.connect()
        writer.write(application_frame())
        await writer.drain()
        self.assertEqual(await asyncio.wait_for(reader.read(), 2), b"")
        await eventually(lambda: not self.bridge.active)
        self.assertTrue(self.bridge.blocked)
        _, _, line = await self.connect()
        self.assertEqual(line, b"")
        gate.set()
        await eventually(lambda: self.factory.ports[0].closed.is_set())
        self.assertFalse(self.factory.ports[0].writes)

    async def test_shutdown_waits_for_serial_close(self):
        reader, _, _ = await self.connect()
        port = await self.opened()
        await self.bridge.close()
        self.assertEqual(await asyncio.wait_for(reader.read(), 2), b"")
        self.assertTrue(port.closed.is_set())
        self.assertFalse(port.thread.is_alive())

    async def test_disconnect_waits_for_inflight_write_before_replacement(self):
        gate = threading.Event()
        self.factory.configure = lambda port: setattr(port, "write_gate", gate)
        _, writer, _ = await self.connect()
        first = await self.opened()
        writer.write(application_frame())
        await writer.drain()
        await eventually(first.writing.is_set)
        writer.close()
        await eventually(lambda: self.bridge.session.worker.stop.is_set())
        _, _, line = await self.connect()
        self.assertEqual(line, b"")
        gate.set()
        await eventually(lambda: not self.bridge.active)
        self.factory.configure = lambda port: None
        reader, writer, _ = await self.connect()
        second = await self.opened(2)
        self.assertFalse(second.writes)
        frame = application_frame(1002)
        writer.write(frame)
        await writer.drain()
        self.assertEqual(await asyncio.wait_for(reader.readexactly(len(frame)), 2), frame)
        self.assertEqual(second.writes, [frame])

    async def test_serial_output_is_byte_bounded_when_tcp_stalls(self):
        reader, _, _ = await self.connect()
        port = await self.opened()
        stalled = asyncio.Event()
        self.bridge.session.writer.drain = mock.AsyncMock(side_effect=stalled.wait)
        for _ in range(20):
            port.emit(b"x" * 4096)
        await eventually(lambda: not self.bridge.active)
        self.assertLessEqual(self.bridge.session.worker.received_bytes, bridge.BUFFER_LIMIT)
        self.assertTrue(port.closed.is_set())
        self.assertLessEqual(len(await reader.read()), 4096)

    async def test_usb_serve_is_independent_of_bleak(self):
        started = asyncio.Event()
        servers = []
        original = asyncio.start_server
        async def start(*args, **kwargs):
            server = await original(*args, **kwargs)
            servers.append(server)
            started.set()
            return server
        args = argparse.Namespace(serial_port="COM7", device=None, scan=False, pair=False,
                                  control_port=None, token_file=None, timeout=0.25,
                                  listen="127.0.0.1", port=0)
        with mock.patch.dict(sys.modules, {"bleak": None, "serial": types.SimpleNamespace(Serial=self.factory)}), mock.patch.object(asyncio, "start_server", side_effect=start):
            task = asyncio.create_task(bridge.serve(args))
            try:
                await asyncio.wait_for(started.wait(), 2)
                reader, writer = await asyncio.open_connection("127.0.0.1", servers[0].sockets[0].getsockname()[1])
                self.writers.append(writer)
                self.assertIn(b"usb-serial", await reader.readline())
                frame = application_frame()
                writer.write(frame)
                await writer.drain()
                self.assertEqual(await asyncio.wait_for(reader.readexactly(len(frame)), 2), frame)
            finally:
                task.cancel()
                await asyncio.gather(task, return_exceptions=True)
            self.assertTrue(self.factory.ports[0].closed.is_set())

    @unittest.skipUnless(os.name == "posix", "PTY serial fixture requires POSIX")
    async def test_real_pyserial_backend_over_virtual_tty(self):
        import serial
        master, slave = os.openpty()
        os.set_blocking(master, False)
        backend = serial_backend.SerialBridge(os.ttyname(slave), serial.Serial, timeout=0.5)
        server = await asyncio.start_server(backend.accept, "127.0.0.1", 0)
        try:
            reader, writer = await asyncio.open_connection("127.0.0.1", server.sockets[0].getsockname()[1])
            self.writers.append(writer)
            await reader.readline()
            frame = application_frame()
            writer.write(frame)
            await writer.drain()
            data = bytearray()
            async with asyncio.timeout(2):
                while len(data) < len(frame):
                    try:
                        data.extend(os.read(master, 4096))
                    except BlockingIOError:
                        await asyncio.sleep(0.005)
            self.assertEqual(bytes(data), frame)
            os.write(master, frame)
            self.assertEqual(await asyncio.wait_for(reader.readexactly(len(frame)), 2), frame)
            writer.close()
            await eventually(lambda: not backend.active)
            self.assertFalse(backend.session.worker.thread.is_alive())
        finally:
            server.close()
            await backend.close()
            await server.wait_closed()
            os.close(master)
            os.close(slave)

    @unittest.skipUnless(os.environ.get("CHAMELEON_BRIDGE_TEST_BINARY"), "set path to a no-cgo CLI binary")
    async def test_web_reuses_usb_tcp_session_and_disconnects(self):
        self.factory.configure = configure_info_device
        process = await asyncio.create_subprocess_exec(
            os.path.abspath(os.environ["CHAMELEON_BRIDGE_TEST_BINARY"]), "serve", "--addr", "127.0.0.1:0",
            "--port", f"tcp:127.0.0.1:{self.port}", "--timeout", "2s",
            stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        try:
            line = await asyncio.wait_for(process.stdout.readline(), 5)
            match = re.search(rb"http://([^/]+)/", line)
            self.assertIsNotNone(match, line)
            base = "http://" + match[1].decode()
            opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
            def request(path, body=None, token=""):
                data = None if body is None else json.dumps(body).encode()
                req = urllib.request.Request(base + path, data=data,
                    headers={"Content-Type":"application/json", "X-Chameleon-Token":token})
                with opener.open(req, timeout=5) as response:
                    return json.load(response)
            async def api(*args):
                return await asyncio.to_thread(request, *args)
            caps = await api("/api/capabilities")
            self.assertFalse(caps["ble_bridge_enabled"])
            for _ in range(2):
                info = await api("/api/info")
                self.assertEqual(info["transport"], serial_backend.transport_info())
                self.assertEqual(info["git_version"], "test-bridge")
            self.assertEqual(len(self.factory.ports), 1)
            status = await api("/api/session/status")
            result = await api("/api/session/disconnect", {"session_id":status["session_id"]}, caps["csrf_token"])
            self.assertFalse(result["connected"])
            await eventually(lambda: not self.bridge.active)
            self.assertTrue(self.factory.ports[0].closed.is_set())
        finally:
            if process.returncode is None:
                process.terminate()
            await asyncio.wait_for(process.communicate(), 5)

    @unittest.skipUnless(os.environ.get("CHAMELEON_BRIDGE_TEST_BINARY"), "set path to a no-cgo CLI binary")
    async def test_desk_cli_uses_usb_bridge_and_refuses_bootloader(self):
        self.factory.configure = configure_info_device
        binary = os.path.abspath(os.environ["CHAMELEON_BRIDGE_TEST_BINARY"])
        async def run(*args):
            process = await asyncio.create_subprocess_exec(binary, *args, "--port", f"tcp:127.0.0.1:{self.port}", stdout=subprocess.PIPE, stderr=subprocess.PIPE)
            try:
                stdout, stderr = await asyncio.wait_for(process.communicate(), 10)
            finally:
                if process.returncode is None:
                    process.kill()
                    await process.wait()
            return process.returncode, stdout, stderr
        code, stdout, stderr = await run("info", "--json")
        self.assertEqual(code, 0, stderr.decode())
        info = json.loads(stdout)
        self.assertEqual(info["transport"], serial_backend.transport_info())
        self.assertEqual(len(info["slots"]), 8)
        await eventually(lambda: not self.bridge.active)
        code, _, stderr = await run("firmware", "enter", "--yes")
        self.assertNotEqual(code, 0)
        self.assertIn(b"USB serial TCP bridge", stderr)
        await eventually(lambda: not self.bridge.active)
        self.assertFalse(self.factory.ports[-1].writes)


class SerialOptionsTests(unittest.TestCase):
    def test_modes_and_local_paths(self):
        args = bridge.parser().parse_args(["--serial-port", "COM7"])
        bridge.validate_modes(args)
        for extra in (["--pair"], ["--control-port", "12346", "--token-file", "key"]):
            args = bridge.parser().parse_args(["--serial-port", "COM7", *extra])
            with self.assertRaises(ValueError):
                bridge.validate_modes(args)
        for path in ("", " ", "socket://localhost:1", "tcp:localhost:1", "ble:device"):
            with self.assertRaises(argparse.ArgumentTypeError):
                bridge.serial_port(path)

    def test_help_does_not_import_optional_backends(self):
        result = subprocess.run([sys.executable, "-S", bridge.__file__, "--help"], capture_output=True, timeout=5)
        self.assertEqual(result.returncode, 0)
        self.assertIn(b"--serial-port", result.stdout)
