"""Versioned, authenticated loopback management shared by device bridges."""

import asyncio
import contextlib
import json
import os
import secrets
import stat

from bridge import Bridge, Session, connect_nus, transport_info, NUS_SERVICE


class APIError(Exception):
    def __init__(self, status, message):
        self.status = status
        super().__init__(message)


def load_token(path):
    try:
        descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    except FileExistsError:
        descriptor = os.open(path, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0) | getattr(os, "O_NONBLOCK", 0))
        with os.fdopen(descriptor) as stream:
            metadata = os.fstat(stream.fileno())
            if not stat.S_ISREG(metadata.st_mode) or (os.name == "posix" and metadata.st_mode & 0o077):
                raise ValueError("token file must be a private regular file")
            content = stream.read(257)
            if len(content) > 256:
                raise ValueError("token file is too large")
            token = content.strip()
    else:
        token = secrets.token_hex(32)
        with os.fdopen(descriptor, "w") as stream:
            stream.write(token + "\n")
    if len(token) != 64 or any(c not in "0123456789abcdef" for c in token):
        raise ValueError("token file must contain 64 lowercase hexadecimal characters")
    return token


class Link:
    def __init__(self, owner):
        self.owner = owner
        self.session = None
        self.parameters = None
        self.client = owner.factory(owner.selected, disconnected_callback=self.disconnected,
                                    pair=owner.pair, timeout=owner.timeout)

    def notification(self, characteristic, data):
        if self.session is not None:
            self.session.notification(characteristic, data)

    def disconnected(self, client):
        if self.session is not None:
            self.session.disconnected(client)
        if self.owner.link is self and self.owner.state == "connected":
            self.owner.state = "disconnected"

    def connected(self):
        pass

    async def connect(self):
        self.parameters = await connect_nus(self.client, self.notification, self.owner.timeout)

    async def disconnect(self):
        async with asyncio.timeout(self.owner.timeout):
            await self.client.disconnect()


class ManagedBridge(Bridge):
    def __init__(self, owner):
        super().__init__(None, owner.factory, owner.timeout, owner.pair)
        self.owner = owner

    def metadata(self):
        return {"api_version": 1, "transport": transport_info(True),
                "device_id": self.owner.selected.address}

    def accept(self, reader, writer):
        if self.owner.state != "connected" or self.owner.link is None:
            writer.close()
            return
        super().accept(reader, writer)

    def make_session(self, reader, writer):
        return Session(reader, writer, self.owner.selected, self.client_factory,
                       self.timeout, self.pair, prepared=self.owner.link)

    def session_ended(self):
        if self.session is not None and not self.session.clean:
            self.blocked = True
        self.owner.link = None
        self.owner.state = "blocked" if self.blocked else "disconnected"
        if self.blocked:
            self.owner.error = "BLE cleanup failed; restart the bridge"


class Controller:
    def __init__(self, scanner, factory, timeout=20.0, pair=False):
        self.scanner, self.factory = scanner, factory
        self.timeout, self.pair = timeout, pair
        self.devices = {}
        self.records = []
        self.selected = None
        self.link = None
        self.connect_task = None
        self.state = "disconnected"
        self.error = ""
        self.lock = asyncio.Lock()
        self.bridge = ManagedBridge(self)
        self.data_address = ""

    def status(self):
        return {"api_version": 1, "state": self.state,
                "selected_device": self.record(self.selected) if self.selected else None,
                "session_active": self.bridge.active,
                "transport": transport_info(True), "data_address": self.data_address,
                "error": self.error}

    def record(self, device):
        return {"id": device.address, "name": device.name or ""}

    def available(self):
        if self.lock.locked() or self.state != "disconnected" or self.bridge.active or self.link:
            raise APIError(409, "disconnect before scanning, selecting or connecting")

    async def scan(self):
        self.available()
        async with self.lock:
            self.state, self.error = "scanning", ""
            try:
                found = await self.scanner.discover(timeout=self.timeout, return_adv=True,
                                                    service_uuids=[NUS_SERVICE])
                self.devices = {device.address: device for device, _ in found.values()}
                if self.selected and self.selected.address not in self.devices:
                    self.selected = None
                self.records = [{"id": device.address, "name": adv.local_name or device.name or "",
                                 "rssi": adv.rssi} for device, adv in found.values()]
                return {"api_version": 1, "devices": self.records}
            finally:
                self.state = "disconnected"

    async def select(self, identifier):
        self.available()
        if identifier not in self.devices:
            raise APIError(400, "select a device from the last scan")
        self.selected = self.devices[identifier]
        self.error = ""
        return self.status()

    def make_link(self):
        return Link(self)

    async def connect(self):
        self.available()
        if self.selected is None:
            raise APIError(409, "select a device first")
        async with self.lock:
            self.state, self.error = "connecting", ""
            link = None
            try:
                link = self.make_link()
                self.link = link
                self.connect_task = asyncio.create_task(link.connect())
                await self.connect_task
                self.state = "connected"
                link.connected()
                return self.status()
            except BaseException as error:
                self.state = "disconnecting"
                try:
                    if link:
                        await link.disconnect()
                    self.state = "disconnected"
                except Exception:
                    self.state = "blocked"
                    self.bridge.blocked = True
                self.link = None
                if isinstance(error, asyncio.CancelledError):
                    raise
                self.error = str(error) or type(error).__name__
                raise APIError(502, self.error) from error
            finally:
                self.connect_task = None

    async def disconnect(self):
        operation = asyncio.create_task(self._disconnect())
        try:
            return await asyncio.shield(operation)
        except asyncio.CancelledError:
            # A dropped HTTP response must not interrupt BLE cleanup or leave
            # a link reusable while a disconnect is still pending.
            await operation
            raise

    async def _disconnect(self):
        if self.state == "scanning":
            raise APIError(409, "scan is still running")
        if self.state == "blocked":
            raise APIError(409, "bridge cleanup failed; restart the bridge")
        if self.state == "disconnecting":
            raise APIError(409, "disconnect is already running")
        if self.connect_task is not None:
            task = self.connect_task
            if not task.done():
                task.cancel()
            await asyncio.gather(task, return_exceptions=True)
            # connect() owns cleanup while holding the management lock.
            async with self.lock:
                pass
            if self.state == "connected":
                return await self._disconnect()
            return self.status()
        self.state = "disconnecting"
        if self.bridge.active:
            session = self.bridge.session
            await self.bridge.close()
            clean = session is None or session.clean
            self.bridge.blocked = not clean
        elif self.link:
            try:
                await self.link.disconnect()
                self.bridge.blocked = False
            except Exception as error:
                self.error = str(error) or type(error).__name__
                self.bridge.blocked = True
        self.link = None
        self.state = "blocked" if self.bridge.blocked else "disconnected"
        return self.status()

    async def close(self):
        with contextlib.suppress(APIError):
            await self.disconnect()
        await self.bridge.close()


class ManagementServer:
    def __init__(self, controller, token, authority):
        self.controller, self.token, self.authority = controller, token, authority
        self.tasks = set()
        self.writers = set()

    def accept(self, reader, writer):
        self.writers.add(writer)
        task = asyncio.create_task(self.handle(reader, writer))
        self.tasks.add(task)
        task.add_done_callback(self.tasks.discard)

    async def dispatch(self, method, path, body):
        if method == "GET" and path == "/v1/status":
            return self.controller.status()
        if method != "POST":
            raise APIError(405, "method not allowed")
        if not isinstance(body, dict):
            raise APIError(400, "body must be a JSON object")
        expected = {"device_id"} if path == "/v1/select" else set()
        if set(body) != expected:
            raise APIError(400, "unexpected or missing fields")
        if path == "/v1/scan":
            return await self.controller.scan()
        if path == "/v1/select":
            if not isinstance(body["device_id"], str):
                raise APIError(400, "device_id must be a string")
            return await self.controller.select(body["device_id"])
        if path == "/v1/connect":
            try:
                return await self.controller.connect()
            except asyncio.CancelledError:
                if asyncio.current_task().cancelling():
                    raise
                raise APIError(409, "connection was canceled")
        if path == "/v1/disconnect":
            return await self.controller.disconnect()
        raise APIError(404, "unknown management endpoint")

    async def handle(self, reader, writer):
        operation = None
        peer = None
        status, result = 200, {}
        try:
            async with asyncio.timeout(10):
                raw = await reader.readuntil(b"\r\n\r\n")
                if len(raw) > 8192:
                    raise APIError(413, "headers too large")
                lines = raw.decode("ascii").split("\r\n")
                method, path, version = lines[0].split(" ")
                if version != "HTTP/1.1":
                    raise APIError(400, "HTTP/1.1 required")
                headers = {}
                for line in lines[1:-2]:
                    key, value = line.split(":", 1)
                    key = key.lower()
                    if key in headers:
                        raise APIError(400, "duplicate header")
                    headers[key] = value.strip()
                if headers.get("host") != self.authority or "origin" in headers or headers.get("sec-fetch-site") == "cross-site":
                    raise APIError(403, "untrusted request origin or host")
                if not secrets.compare_digest(headers.get("authorization", ""), "Bearer " + self.token):
                    raise APIError(401, "management authorization required")
                if "transfer-encoding" in headers:
                    raise APIError(400, "transfer encoding is not supported")
                length = int(headers.get("content-length", "0"))
                if length < 0 or length > 4096:
                    raise APIError(413, "body too large")
                if method == "POST" and headers.get("content-type", "").split(";")[0] != "application/json":
                    raise APIError(415, "application/json required")
                data = await reader.readexactly(length)
                body = json.loads(data) if data else {}
            operation = asyncio.create_task(self.dispatch(method, path, body))
            peer = asyncio.create_task(reader.read(1))
            done, _ = await asyncio.wait([operation, peer], return_when=asyncio.FIRST_COMPLETED)
            if operation not in done:
                return
            result = operation.result()
        except APIError as error:
            status, result = error.status, {"error": str(error)}
        except (ValueError, UnicodeError, asyncio.IncompleteReadError):
            status, result = 400, {"error": "invalid management request"}
        except (TimeoutError, asyncio.LimitOverrunError):
            status, result = 408, {"error": "request timed out or exceeded header limit"}
        except Exception:
            status, result = 502, {"error": "management operation failed"}
        finally:
            for task in (operation, peer):
                if task and not task.cancelling():
                    task.cancel()
            await asyncio.gather(*(t for t in (operation, peer) if t), return_exceptions=True)
            if not writer.is_closing():
                payload = json.dumps(result, separators=(",", ":")).encode()
                writer.write(f"HTTP/1.1 {status} Result\r\nContent-Type: application/json\r\nCache-Control: no-store\r\nConnection: close\r\nContent-Length: {len(payload)}\r\n\r\n".encode() + payload)
                with contextlib.suppress(Exception):
                    async with asyncio.timeout(5):
                        await writer.drain()
            writer.close()
            with contextlib.suppress(Exception):
                async with asyncio.timeout(5):
                    await asyncio.shield(writer.wait_closed())
            self.writers.discard(writer)

    async def close(self):
        writers = list(self.writers)
        for writer in writers:
            writer.close()
        tasks = list(self.tasks)
        for task in tasks:
            if not task.cancelling():
                task.cancel()
        await asyncio.gather(*tasks, return_exceptions=True)
        for writer in writers:
            with contextlib.suppress(Exception):
                async with asyncio.timeout(5):
                    await asyncio.shield(writer.wait_closed())
        self.writers.clear()


async def serve_managed(args, scanner, factory):
    token = load_token(args.token_file)
    owner = Controller(scanner, factory, args.timeout, args.pair)
    if args.device:
        device = await scanner.find_device_by_address(args.device, timeout=args.timeout)
        if device is None:
            raise RuntimeError("BLE device not found")
        owner.devices[device.address] = device
        owner.selected = device
    data = await asyncio.start_server(owner.bridge.accept, args.listen, args.port, limit=65536)
    host = f"[{args.listen}]" if ":" in args.listen else args.listen
    owner.data_address = f"{host}:{data.sockets[0].getsockname()[1]}"
    control = None
    api = ManagementServer(owner, token, f"{host}:{args.control_port}")
    try:
        control = await asyncio.start_server(api.accept, args.listen, args.control_port, limit=8192)
        print(f"BLE management on http://{api.authority}; data on {owner.data_address}", flush=True)
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
