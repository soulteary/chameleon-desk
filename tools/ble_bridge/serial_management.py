"""USB serial management using the bridge's authenticated loopback protocol."""

import asyncio
import contextlib
import json
import threading

from management import APIError, Controller, ManagementServer, load_token
from serial_backend import SerialBridge, SerialSession, SerialWorker, transport_info


def port_record(port):
    return {"id": port.device, "name": port.description or port.device,
            "vid": port.vid, "pid": port.pid,
            "serial_number": port.serial_number, "location": port.location,
            "hwid": port.hwid}


class SerialLink:
    def __init__(self, owner):
        self.owner = owner
        self.worker = SerialWorker(owner.selected["id"], owner.factory, owner.timeout)
        self.started = False
        self.monitor = None
        self.close_lock = asyncio.Lock()

    async def connect(self):
        # A path can be reused after unplug/replug. Recheck available metadata;
        # enumeration cannot prove physical identity for indistinguishable ports.
        records = await self.owner.discover()
        if self.owner.selected not in records:
            self.owner.selected = None
            self.owner.devices = {}
            raise RuntimeError("serial port changed or disappeared; scan and select again")
        self.started = True
        await self.worker.connect()

    def connected(self):
        self.monitor = asyncio.create_task(self.watch())

    async def watch(self):
        await self.worker.ended.wait()
        owner = self.owner
        # Once claimed, the data session owns teardown and state publication.
        if owner.link is not self or owner.bridge.active or owner.state != "connected":
            return
        owner.state = "disconnecting"
        try:
            await self.disconnect()
            blocked = False
        except Exception:
            blocked = True
        if owner.link is self:
            owner.link = None
            owner.bridge.blocked = blocked
            owner.state = "blocked" if blocked else "disconnected"
            owner.error = "serial cleanup failed; restart the bridge" if blocked else "serial link ended; connect explicitly after checking device state"

    async def disconnect(self):
        async with self.close_lock:
            if self.started:
                operation = asyncio.create_task(self.worker.disconnect())
                try:
                    await asyncio.shield(operation)
                except asyncio.CancelledError:
                    # An abandoned setup response can cancel this internal
                    # cleanup after setup has already failed. Finish it so the
                    # controller can publish disconnected or blocked state.
                    await operation
        if self.monitor and self.monitor is not asyncio.current_task():
            self.monitor.cancel()
            await asyncio.gather(self.monitor, return_exceptions=True)


class ManagedSerialBridge(SerialBridge):
    def __init__(self, owner):
        super().__init__(None, owner.factory, owner.timeout)
        self.owner = owner

    def metadata(self):
        return {"api_version": 1, "transport": transport_info(True),
                "device_id": self.owner.selected["id"]}

    def accept(self, reader, writer):
        if self.owner.state != "connected" or self.owner.link is None or self.owner.link.worker.ended.is_set():
            writer.close()
            return
        super().accept(reader, writer)

    def make_session(self, reader, writer):
        return SerialSession(reader, writer, self.owner.selected["id"], self.client_factory,
                             self.timeout, prepared=self.owner.link)

    def session_ended(self):
        clean = self.session is not None and self.session.clean
        self.owner.link = None
        # Bridge.close temporarily blocks admission even when cleanup succeeds.
        # The controller owns an explicit disconnect transition and its result.
        if self.owner.state != "disconnecting":
            self.owner.state = "blocked" if self.blocked else "disconnected"
        self.owner.error = "" if clean else "serial cleanup failed; restart the bridge"
        if clean and self.session.worker.error:
            self.owner.error = "serial link ended; connect explicitly after checking device state"


class SerialController(Controller):
    def __init__(self, enumerator, factory, timeout=20.0):
        super().__init__(None, factory, timeout)
        self.enumerator = enumerator
        self.discovery = None
        self.bridge = ManagedSerialBridge(self)

    def record(self, port):
        return dict(port)

    def status(self):
        value = super().status()
        value["transport"] = transport_info(True)
        return value

    def available(self):
        super().available()
        if self.discovery is not None and self.discovery.is_alive():
            raise APIError(409, "serial enumeration is still running")

    async def discover(self):
        if self.discovery is not None and self.discovery.is_alive():
            raise APIError(409, "serial enumeration is still running")
        loop = asyncio.get_running_loop()
        future = loop.create_future()
        def finish(records, error):
            if not future.done():
                if error:
                    future.set_exception(RuntimeError("serial enumeration failed"))
                else:
                    future.set_result(records)
        def enumerate_ports():
            records, error = [], False
            try:
                records = sorted((port_record(p) for p in self.enumerator()), key=lambda p: p["id"])
                if len(records) > 128 or len(json.dumps(records).encode()) > 60000 or len({p["id"] for p in records}) != len(records):
                    raise RuntimeError("invalid serial enumeration")
            except Exception:
                error = True
            with contextlib.suppress(RuntimeError):
                loop.call_soon_threadsafe(finish, records, error)
        # Cancellation cannot stop an OS enumerator. Keep at most one daemon
        # thread and do not let its late result mutate selection or state.
        self.discovery = threading.Thread(target=enumerate_ports, name="chameleon-usb-scan", daemon=True)
        self.discovery.start()
        async with asyncio.timeout(self.timeout):
            return await future

    async def scan(self):
        self.available()
        async with self.lock:
            self.state, self.error = "scanning", ""
            try:
                records = await self.discover()
                self.records = records
                self.devices = {p["id"]: p for p in records}
                if self.selected not in records:
                    self.selected = None
                return {"api_version": 1, "devices": records}
            finally:
                self.state = "disconnected"

    async def _disconnect(self):
        link = self.link
        await super()._disconnect()
        # accept() can claim TCP before its handle task starts. Canceling that
        # task cannot run SerialSession cleanup, so the prepared link still
        # belongs to the controller. Do not publish a reusable disconnected
        # state while that serial owner is alive (or its close failed).
        if link and link.started and (not link.worker.clean or link.worker.thread.is_alive()):
            blocked = self.bridge.blocked
            self.state = "disconnecting"
            try:
                await link.disconnect()
            except Exception:
                blocked = True
            self.bridge.blocked = blocked
            self.state = "blocked" if blocked else "disconnected"
            if blocked:
                self.error = "serial cleanup failed; restart the bridge"
        return self.status()

    def make_link(self):
        return SerialLink(self)


async def serve_managed_serial(args, enumerator, factory):
    token = load_token(args.token_file)
    owner = SerialController(enumerator, factory, args.timeout)
    host = f"[{args.listen}]" if ":" in args.listen else args.listen
    data = await asyncio.start_server(owner.bridge.accept, args.listen, args.port, limit=65536)
    owner.data_address = f"{host}:{data.sockets[0].getsockname()[1]}"
    api = ManagementServer(owner, token, f"{host}:{args.control_port}")
    control = None
    try:
        control = await asyncio.start_server(api.accept, args.listen, args.control_port, limit=8192)
        print(f"USB serial management on http://{api.authority}; data on {owner.data_address}", flush=True)
        await asyncio.Event().wait()
    finally:
        data.close()
        if control:
            control.close()
        await api.close()
        await owner.close()
        await data.wait_closed()
        if control:
            await control.wait_closed()
