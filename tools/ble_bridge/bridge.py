#!/usr/bin/env python3
"""Expose one Chameleon NUS connection as a loopback TCP byte stream."""

import argparse
import asyncio
import contextlib
import ipaddress
import logging
import math
import sys

NUS_SERVICE = "6e400001-b5a3-f393-e0a9-e50e24dcca9e"
NUS_RX = "6e400002-b5a3-f393-e0a9-e50e24dcca9e"
NUS_TX = "6e400003-b5a3-f393-e0a9-e50e24dcca9e"
BUFFER_LIMIT = 65536
LOG = logging.getLogger("ble_bridge")


class Session:
    def __init__(self, reader, writer, device, client_factory, timeout, pair):
        self.reader, self.writer = reader, writer
        self.timeout = timeout
        self.ready = asyncio.Event()
        self.failed = asyncio.Event()
        self.reason = ""
        self.closed = False
        self.queue = asyncio.Queue()
        self.buffered = 0
        self.incoming = asyncio.Queue()
        self.incoming_buffered = 0
        self.client = client_factory(
            device, disconnected_callback=self.disconnected, pair=pair,
            timeout=timeout,
        )
        self.rx = None
        self.response = True
        self.chunk_size = 20

    def fail(self, reason):
        if not self.closed and not self.failed.is_set():
            self.reason = reason
            self.failed.set()

    def disconnected(self, _client):
        self.fail("BLE disconnected")

    def notification(self, _characteristic, data):
        # Bleak invokes callbacks on the asyncio loop. Do not drop bytes or
        # let an old session deliver notifications to a subsequent client.
        if self.closed or self.failed.is_set() or not data:
            return
        if self.buffered + len(data) > BUFFER_LIMIT:
            self.fail("BLE notification buffer overflow")
            return
        self.buffered += len(data)
        self.queue.put_nowait(bytes(data))

    async def ble_to_tcp(self):
        async with asyncio.timeout(self.timeout):
            await self.client.connect()
            service = self.client.services.get_service(NUS_SERVICE)
            if service is None:
                raise RuntimeError("Nordic UART Service not found")
            self.rx = service.get_characteristic(NUS_RX)
            tx = service.get_characteristic(NUS_TX)
            if self.rx is None or tx is None or "notify" not in tx.properties:
                raise RuntimeError("NUS RX/TX characteristics are unavailable")
            self.response = "write" in self.rx.properties
            if not self.response and "write-without-response" not in self.rx.properties:
                raise RuntimeError("NUS RX characteristic is not writable")
            # A conservative 20-byte ceiling avoids relying on platform MTU
            # reporting. Honor a smaller characteristic limit if supplied.
            if not self.response:
                self.chunk_size = min(20, self.rx.max_write_without_response_size)
                if self.chunk_size < 1:
                    raise RuntimeError("invalid NUS write size")
            await self.client.start_notify(tx, self.notification)
        self.ready.set()
        LOG.info("BLE connected; forwarding application protocol")
        while True:
            data = await self.queue.get()
            self.writer.write(data)
            async with asyncio.timeout(self.timeout):
                await self.writer.drain()
            self.buffered -= len(data)

    async def read_tcp(self):
        while True:
            data = await self.reader.read(4096)
            if not data:
                return
            if self.incoming_buffered + len(data) > BUFFER_LIMIT:
                self.fail("TCP input buffer overflow")
                return
            self.incoming_buffered += len(data)
            self.incoming.put_nowait(data)

    async def tcp_to_ble(self):
        await self.ready.wait()
        while True:
            data = await self.incoming.get()
            for start in range(0, len(data), self.chunk_size):
                async with asyncio.timeout(self.timeout):
                    await self.client.write_gatt_char(
                        self.rx, data[start:start + self.chunk_size],
                        response=self.response,
                    )
            self.incoming_buffered -= len(data)

    async def run(self):
        tasks = [asyncio.create_task(self.ble_to_tcp()),
                 asyncio.create_task(self.read_tcp()),
                 asyncio.create_task(self.tcp_to_ble()),
                 asyncio.create_task(self.failed.wait())]
        clean = False
        try:
            done, _ = await asyncio.wait(tasks, return_when=asyncio.FIRST_COMPLETED)
            for task in done:
                task.result()
            if self.failed.is_set():
                LOG.warning("session ended: %s", self.reason)
        except Exception as error:
            # No protocol payloads, keys or pairing credentials in logs.
            LOG.warning("session ended: %s", str(error) or type(error).__name__)
        finally:
            self.closed = True
            for task in tasks:
                task.cancel()
            await asyncio.gather(*tasks, return_exceptions=True)
            self.writer.close()
            try:
                async with asyncio.timeout(self.timeout):
                    await self.client.disconnect()
                clean = True
            except Exception as error:
                LOG.error("BLE cleanup failed; restart the bridge: %s",
                          str(error) or type(error).__name__)
            with contextlib.suppress(Exception):
                async with asyncio.timeout(self.timeout):
                    await self.writer.wait_closed()
        return clean


class Bridge:
    def __init__(self, device, client_factory, timeout=20.0, pair=False):
        self.device, self.client_factory = device, client_factory
        self.timeout, self.pair = timeout, pair
        self.active = False
        self.blocked = False
        self.tasks = set()
        self.writer = None

    def accept(self, reader, writer):
        if self.active or self.blocked:
            # Never insert a text greeting/error into the binary protocol.
            writer.close()
            return
        self.active = True
        self.writer = writer
        task = asyncio.create_task(self.handle(reader, writer))
        self.tasks.add(task)
        task.add_done_callback(self.tasks.discard)

    async def handle(self, reader, writer):
        try:
            session = Session(reader, writer, self.device, self.client_factory,
                              self.timeout, self.pair)
            self.blocked = not await session.run()
        except asyncio.CancelledError:
            raise
        except Exception as error:
            self.blocked = True
            writer.close()
            LOG.error("session setup failed; restart the bridge: %s", error)
        finally:
            self.active = False
            self.writer = None

    async def close(self):
        self.blocked = True
        # An accepted task can be canceled before handle() starts, in which
        # case its finally block never runs. Close the socket here as well.
        writer = self.writer
        if writer is not None:
            writer.close()
        tasks = list(self.tasks)
        for task in tasks:
            task.cancel()
        await asyncio.gather(*tasks, return_exceptions=True)
        if writer is not None:
            with contextlib.suppress(Exception):
                async with asyncio.timeout(self.timeout):
                    await writer.wait_closed()
        self.active = False
        self.writer = None


def loopback(value):
    try:
        if ipaddress.ip_address(value).is_loopback:
            return value
    except ValueError:
        pass
    raise argparse.ArgumentTypeError("listen address must be a loopback IP")


def positive_timeout(value):
    try:
        number = float(value)
        if math.isfinite(number) and number > 0:
            return number
    except ValueError:
        pass
    raise argparse.ArgumentTypeError("timeout must be finite and positive")


def port_number(value):
    try:
        number = int(value)
        if 1 <= number <= 65535:
            return number
    except ValueError:
        pass
    raise argparse.ArgumentTypeError("port must be 1..65535")


def parser():
    p = argparse.ArgumentParser(description=__doc__)
    target = p.add_mutually_exclusive_group(required=True)
    target.add_argument("--device", help="BLE address, or device UUID on macOS")
    target.add_argument("--scan", action="store_true", help="list advertising NUS devices and exit")
    p.add_argument("--listen", type=loopback, default="127.0.0.1")
    p.add_argument("--port", type=port_number, default=12345)
    p.add_argument("--timeout", type=positive_timeout, default=20.0,
                   help="seconds for discovery, connection, writes and cleanup (default: 20)")
    p.add_argument("--pair", action="store_true", help="request OS-managed pairing (platform dependent)")
    return p


async def serve(args):
    # Keep offline tests and --help independent of optional BLE dependencies.
    try:
        from bleak import BleakClient, BleakScanner
    except ImportError as error:
        raise RuntimeError("install tools/ble_bridge/requirements.txt first") from error
    if args.scan:
        devices = await BleakScanner.discover(
            timeout=args.timeout, return_adv=True, service_uuids=[NUS_SERVICE])
        for device, adv in devices.values():
            print(f"{device.address}\t{adv.local_name or device.name or ''}\t{adv.rssi} dBm")
        return
    device = await BleakScanner.find_device_by_address(args.device, timeout=args.timeout)
    if device is None:
        raise RuntimeError("BLE device not found")
    bridge = Bridge(device, BleakClient, timeout=args.timeout, pair=args.pair)
    server = await asyncio.start_server(bridge.accept, args.listen, args.port,
                                        limit=BUFFER_LIMIT)
    LOG.info("listening on %s:%s; one client at a time", args.listen, args.port)
    try:
        # start_server is already accepting. In Python 3.13, cancellation of
        # serve_forever waits for client transports; close them ourselves first.
        await asyncio.Event().wait()
    finally:
        server.close()
        await bridge.close()
        await server.wait_closed()


def main():
    if sys.version_info < (3, 11):
        print("ble_bridge requires Python 3.11 or later", file=sys.stderr)
        return 1
    args = parser().parse_args()
    logging.basicConfig(level=logging.INFO, format="%(levelname)s: %(message)s")
    try:
        asyncio.run(serve(args))
    except KeyboardInterrupt:
        pass
    except Exception as error:
        LOG.error("%s", error)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
