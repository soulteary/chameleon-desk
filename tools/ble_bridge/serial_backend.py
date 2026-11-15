"""Fixed-port USB serial backend. pySerial is imported only when selected."""

import asyncio
import contextlib
import logging
import os
import queue
import threading
import time

from bridge import ApplicationFrames, Bridge, BUFFER_LIMIT, RESTRICTED_COMMANDS, StreamSession

LOG = logging.getLogger("usb_serial_bridge")
USB_RESTRICTED_COMMANDS = RESTRICTED_COMMANDS | {1010}
POLL_SECONDS = 0.02


def transport_info(managed=False):
    # Descriptive metadata, never permission to use wired-only workflows.
    return {"connection": "tcp", "medium": "usb-serial", "managed": managed,
            "allows_pairing_changes": False, "allows_device_restore": False,
            "supports_dfu": False}


class SerialWorker:
    """One thread owns open/configure/read/write/close for one TCP session.

    Canceling an asyncio waiter cannot stop a blocking OS write. Keep the owner
    alive until cleanup completes, and never admit a replacement while it runs.
    """
    def __init__(self, port, factory, timeout):
        self.port_name, self.factory, self.timeout = port, factory, timeout
        self.stop = threading.Event()
        self.incoming = queue.Queue(maxsize=BUFFER_LIMIT)
        self.received_bytes = 0
        self.received_lock = threading.Lock()
        self.outgoing = queue.Queue(maxsize=1)
        self.ready = asyncio.Event()
        self.finished = asyncio.Event()
        self.ended = asyncio.Event()
        self.wake = asyncio.Event()
        self.loop = asyncio.get_running_loop()
        self.opened = False
        self.clean = False
        self.error = None
        self.thread = threading.Thread(target=self.run, name="chameleon-usb-serial", daemon=True)

    def notify(self, callback, *args):
        with contextlib.suppress(RuntimeError):
            self.loop.call_soon_threadsafe(callback, *args)

    @staticmethod
    def acknowledge(future, success):
        if not future.done():
            future.set_result(success)

    def write_frame(self, port, data):
        deadline = time.monotonic() + self.timeout
        offset = 0
        while offset < len(data):
            remaining = deadline - time.monotonic()
            if self.stop.is_set() or remaining <= 0:
                raise TimeoutError("serial write interrupted")
            port.write_timeout = remaining
            count = port.write(data[offset:])
            if not isinstance(count, int) or count <= 0 or count > len(data) - offset:
                raise RuntimeError("serial write made invalid progress")
            offset += count

    def run(self):
        port = None
        current = None
        try:
            options = dict(port=None, baudrate=115200, bytesize=8, parity="N", stopbits=1,
                           timeout=POLL_SECONDS, write_timeout=self.timeout,
                           xonxoff=False, rtscts=False, dsrdtr=False)
            if os.name == "posix":
                options["exclusive"] = True
            port = self.factory(**options)
            port.port = self.port_name
            port.dtr = True
            port.rts = True
            if self.stop.is_set():
                return
            port.open()
            if self.stop.is_set():
                return
            port.reset_input_buffer()
            self.opened = True
            self.notify(self.ready.set)
            while not self.stop.is_set():
                try:
                    current = self.outgoing.get_nowait()
                except queue.Empty:
                    current = None
                if current is not None:
                    data, future = current
                    self.write_frame(port, data)
                    self.notify(self.acknowledge, future, True)
                    current = None
                if self.stop.is_set():
                    break
                data = port.read(4096)
                if data:
                    if len(data) > 4096:
                        raise RuntimeError("serial read exceeds buffer limit")
                    with self.received_lock:
                        if self.received_bytes + len(data) > BUFFER_LIMIT:
                            raise RuntimeError("serial output buffer overflow")
                        self.received_bytes += len(data)
                    self.incoming.put_nowait(bytes(data))
                    self.notify(self.wake.set)
        except Exception as error:
            # Do not expose protocol payloads or exception text from a backend.
            self.error = type(error).__name__
        finally:
            # Publish I/O termination before close: a driver can block in close,
            # and callers still need to end the TCP session and bound cleanup.
            self.notify(self.ended.set)
            self.notify(self.ready.set)
            if current is not None:
                self.notify(self.acknowledge, current[1], False)
            while True:
                try:
                    _, future = self.outgoing.get_nowait()
                    self.notify(self.acknowledge, future, False)
                except queue.Empty:
                    break
            try:
                if port is not None:
                    port.close()
                self.clean = True
            except Exception as error:
                self.error = type(error).__name__
            self.notify(self.ready.set)
            self.notify(self.finished.set)
            self.notify(self.wake.set)

    def take(self):
        data = self.incoming.get_nowait()
        with self.received_lock:
            self.received_bytes -= len(data)
        return data

    async def connect(self):
        self.thread.start()
        async with asyncio.timeout(self.timeout):
            await self.ready.wait()
        if not self.opened or self.ended.is_set():
            raise RuntimeError("serial setup failed")

    async def disconnect(self):
        self.stop.set()
        async with asyncio.timeout(self.timeout):
            await self.finished.wait()
            await asyncio.to_thread(self.thread.join, self.timeout)
        if not self.clean or self.thread.is_alive():
            raise RuntimeError("serial cleanup failed; restart the bridge")

    async def send(self, frame):
        if self.stop.is_set() or self.ended.is_set():
            raise RuntimeError("serial session ended")
        future = self.loop.create_future()
        self.outgoing.put_nowait((frame, future))
        if not await future:
            raise RuntimeError("serial write failed")


class SerialSession(StreamSession):
    def __init__(self, reader, writer, device, factory, timeout, prepared=None):
        super().__init__(reader, writer, timeout)
        self.prepared = prepared
        self.worker = prepared.worker if prepared else SerialWorker(device, factory, timeout)
        self.clean = False

    async def serial_to_tcp(self):
        async with asyncio.timeout(self.timeout):
            await self.worker.ready.wait()
        if not self.worker.opened:
            raise RuntimeError("serial setup failed")
        self.ready.set()
        while True:
            self.worker.wake.clear()
            try:
                data = self.worker.take()
            except queue.Empty:
                if self.worker.finished.is_set():
                    return
                await self.worker.wake.wait()
                continue
            self.writer.write(data)
            async with asyncio.timeout(self.timeout):
                await self.writer.drain()

    async def tcp_to_serial(self):
        await self.ready.wait()
        decoder = ApplicationFrames(USB_RESTRICTED_COMMANDS)
        while True:
            data = await self.incoming.get()
            for frame in decoder.feed(data):
                await self.worker.send(frame)
            self.incoming_buffered -= len(data)

    async def cleanup(self, tasks):
        self.closed = True
        self.worker.stop.set()
        self.writer.close()
        for task in tasks:
            task.cancel()
        await asyncio.gather(*tasks, return_exceptions=True)
        clean = False
        try:
            await self.worker.disconnect()
            clean = True
        except (TimeoutError, RuntimeError):
            LOG.error("serial cleanup failed or timed out; restart the bridge")
        with contextlib.suppress(Exception):
            async with asyncio.timeout(self.timeout):
                await self.writer.wait_closed()
        return clean

    async def run(self):
        if not self.prepared:
            self.worker.thread.start()
        tasks = [asyncio.create_task(self.serial_to_tcp()), asyncio.create_task(self.read_tcp()),
                 asyncio.create_task(self.tcp_to_serial()), asyncio.create_task(self.failed.wait()),
                 asyncio.create_task(self.worker.ended.wait())]
        try:
            done, _ = await asyncio.wait(tasks, return_when=asyncio.FIRST_COMPLETED)
            for task in done:
                task.result()
        except Exception as error:
            LOG.warning("USB session ended (%s)", type(error).__name__)
        finally:
            cleanup = asyncio.create_task(self.cleanup(tasks))
            try:
                self.clean = await asyncio.shield(cleanup)
            except asyncio.CancelledError:
                self.clean = await cleanup
                raise
            if self.worker.error:
                LOG.warning("USB serial backend ended (%s)", self.worker.error)
        return self.clean


class SerialBridge(Bridge):
    def __init__(self, port, factory, timeout=20.0):
        super().__init__(port, factory, timeout)

    def metadata(self):
        return {"api_version": 1, "transport": transport_info(), "device_id": self.device}

    def make_session(self, reader, writer):
        return SerialSession(reader, writer, self.device, self.client_factory, self.timeout)


async def serve_serial(args):
    try:
        import serial
    except ImportError as error:
        raise RuntimeError("install tools/ble_bridge/requirements-usb.txt first") from error
    if getattr(args, "usb", False):
        from serial.tools.list_ports import comports
        from serial_management import serve_managed_serial
        return await serve_managed_serial(args, comports, serial.Serial)
    bridge = SerialBridge(args.serial_port, serial.Serial, args.timeout)
    server = await asyncio.start_server(bridge.accept, args.listen, args.port, limit=BUFFER_LIMIT)
    LOG.info("USB serial bridge listening on %s:%s; one client at a time", args.listen, args.port)
    try:
        await asyncio.Event().wait()
    finally:
        server.close()
        await bridge.close()
        await server.wait_closed()
