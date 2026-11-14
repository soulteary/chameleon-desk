//go:build darwin && cgo

package chameleon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/soulteary/chameleon-desk/dfu"
	"github.com/tinygo-org/cbgo"
)

// The manager is created only on an explicit BLE call, never at package init.
var bleNative = &coreBLE{gate: make(chan struct{}, 1), state: make(chan struct{}, 1), pending: map[string]chan error{}, active: map[string]*nativeConnection{}, closing: map[string]chan struct{}{}}

type coreBLE struct {
	cbgo.CentralManagerDelegateBase
	once     sync.Once
	manager  cbgo.CentralManager
	gate     chan struct{}
	state    chan struct{}
	mu       sync.Mutex
	discover func(cbgo.Peripheral, cbgo.AdvFields, int)
	pending  map[string]chan error
	active   map[string]*nativeConnection
	closing  map[string]chan struct{}
}

func (d *coreBLE) lock(ctx context.Context) error {
	select {
	case d.gate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (d *coreBLE) unlock() { <-d.gate }
func (d *coreBLE) ready(ctx context.Context) error {
	d.once.Do(func() { d.manager = cbgo.NewCentralManager(nil); d.manager.SetDelegate(d) })
	for {
		switch d.manager.State() {
		case cbgo.ManagerStatePoweredOn:
			return nil
		case cbgo.ManagerStateUnauthorized:
			return errors.New("Bluetooth permission denied; grant access in macOS Privacy & Security")
		case cbgo.ManagerStatePoweredOff:
			return errors.New("Bluetooth adapter is powered off")
		case cbgo.ManagerStateUnsupported:
			return ErrBLEUnavailable
		}
		select {
		case <-d.state:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
func (d *coreBLE) CentralManagerDidUpdateState(c cbgo.CentralManager) {
	select {
	case d.state <- struct{}{}:
	default:
	}
}
func (d *coreBLE) DidDiscoverPeripheral(c cbgo.CentralManager, p cbgo.Peripheral, a cbgo.AdvFields, rssi int) {
	d.mu.Lock()
	f := d.discover
	d.mu.Unlock()
	if f != nil {
		f(p, a, rssi)
	}
}
func (d *coreBLE) DidConnectPeripheral(c cbgo.CentralManager, p cbgo.Peripheral) {
	id := p.Identifier().String()
	d.mu.Lock()
	ch := d.pending[id]
	d.mu.Unlock()
	if ch == nil {
		c.CancelConnect(p)
		return
	}
	select {
	case ch <- nil:
	default:
	}
}
func (d *coreBLE) DidFailToConnectPeripheral(c cbgo.CentralManager, p cbgo.Peripheral, e error) {
	if e == nil {
		e = errors.New("BLE connection failed")
	}
	id := p.Identifier().String()
	d.mu.Lock()
	ch := d.pending[id]
	d.mu.Unlock()
	if ch != nil {
		select {
		case ch <- e:
		default:
		}
	}
}
func (d *coreBLE) DidDisconnectPeripheral(c cbgo.CentralManager, p cbgo.Peripheral, e error) {
	id := p.Identifier().String()
	d.mu.Lock()
	n := d.active[id]
	delete(d.active, id)
	if closing := d.closing[id]; closing != nil {
		close(closing)
		delete(d.closing, id)
	}
	pending := d.pending[id]
	d.mu.Unlock()
	if e == nil {
		e = io.EOF
	}
	if n != nil {
		n.abort(e)
	}
	if pending != nil {
		select {
		case pending <- e:
		default:
		}
	}
}
func (d *coreBLE) disconnect(p cbgo.Peripheral) {
	id := p.Identifier().String()
	d.mu.Lock()
	delete(d.active, id)
	if p.State() == cbgo.PeripheralStateDisconnected {
		d.mu.Unlock()
		p.SetDelegate(nil)
		return
	}
	ch := d.closing[id]
	if ch == nil {
		ch = make(chan struct{})
		d.closing[id] = ch
	}
	d.mu.Unlock()
	d.manager.CancelConnect(p)
	select {
	case <-ch:
	case <-time.After(time.Second):
	}
	p.SetDelegate(nil)
}
func nativeBLEScan(ctx context.Context, duration time.Duration) ([]BLEDevice, error) {
	d := bleNative
	if e := d.lock(ctx); e != nil {
		return nil, e
	}
	defer d.unlock()
	if e := d.ready(ctx); e != nil {
		return nil, e
	}
	var mu sync.Mutex
	found := map[string]BLEDevice{}
	d.mu.Lock()
	d.discover = func(p cbgo.Peripheral, a cbgo.AdvFields, rssi int) {
		name := a.LocalName
		if name == "" {
			name = p.Name()
		}
		dfuAdvert := strings.Contains(strings.ToLower(name), "dfu")
		for _, u := range a.ServiceUUIDs {
			dfuAdvert = dfuAdvert || bleUUIDEqual(u.String(), DFUServiceUUID)
		}
		if !strings.HasPrefix(name, "ChameleonUltra") && !strings.HasPrefix(name, "ChameleonLite") && !dfuAdvert {
			return
		}
		mu.Lock()
		found[p.Identifier().String()] = BLEDevice{ID: p.Identifier().String(), Name: name, RSSI: rssi, DFU: dfuAdvert}
		mu.Unlock()
	}
	d.mu.Unlock()
	d.manager.Scan([]cbgo.UUID{cbgo.MustParseUUID(NUSServiceUUID), cbgo.MustParseUUID(DFUServiceUUID)}, nil)
	defer func() { d.manager.StopScan(); d.mu.Lock(); d.discover = nil; d.mu.Unlock() }()
	timer := time.NewTimer(duration)
	defer timer.Stop()
	var err error
	select {
	case <-timer.C:
	case <-ctx.Done():
		err = ctx.Err()
	}
	mu.Lock()
	out := make([]BLEDevice, 0, len(found))
	for _, v := range found {
		out = append(out, v)
	}
	mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, err
}

type nativeConnection struct {
	dfuMode   bool
	responses chan []byte
	firmware  cbgo.Characteristic
	cbgo.PeripheralDelegateBase
	owner      *coreBLE
	peripheral cbgo.Peripheral
	stream     *notificationStream
	done       chan struct{}
	once       sync.Once
	services   chan error
	chars      chan error
	notify     chan error
	ack        chan error
	tx         cbgo.Characteristic
	rx         cbgo.Characteristic
	timeout    time.Duration
}

func sendBLE(ch chan error, e error) {
	select {
	case ch <- e:
	default:
	}
}
func (n *nativeConnection) abort(e error)                                  { n.once.Do(func() { close(n.done); n.stream.fail(e) }) }
func (n *nativeConnection) DidDiscoverServices(p cbgo.Peripheral, e error) { sendBLE(n.services, e) }
func (n *nativeConnection) DidDiscoverCharacteristics(p cbgo.Peripheral, s cbgo.Service, e error) {
	sendBLE(n.chars, e)
}
func (n *nativeConnection) DidUpdateNotificationState(p cbgo.Peripheral, c cbgo.Characteristic, e error) {
	if bleUUIDEqual(c.UUID().String(), NUSTXUUID) || (n.dfuMode && bleUUIDEqual(c.UUID().String(), DFUControlUUID)) {
		sendBLE(n.notify, e)
	}
}
func (n *nativeConnection) DidUpdateValueForCharacteristic(p cbgo.Peripheral, c cbgo.Characteristic, e error) {
	if !bleUUIDEqual(c.UUID().String(), NUSTXUUID) && !(n.dfuMode && bleUUIDEqual(c.UUID().String(), DFUControlUUID)) {
		return
	}
	if e != nil {
		n.abort(e)
		return
	}
	if n.dfuMode {
		select {
		case n.responses <- append([]byte(nil), c.Value()...):
		default:
			n.abort(fmt.Errorf("DFU notification overflow"))
		}
	} else {
		n.stream.push(c.Value())
	}
}
func (n *nativeConnection) DidWriteValueForCharacteristic(p cbgo.Peripheral, c cbgo.Characteristic, e error) {
	if bleUUIDEqual(c.UUID().String(), NUSRXUUID) || (n.dfuMode && bleUUIDEqual(c.UUID().String(), DFUControlUUID)) {
		sendBLE(n.ack, e)
	}
}
func waitBLE(ctx context.Context, ch chan error, done <-chan struct{}) error {
	select {
	case e := <-ch:
		return e
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return ErrClosed
	}
}
func nativeBLEOpen(ctx context.Context, id string, timeout time.Duration) (io.ReadWriteCloser, error) {
	n, e := nativeBLEConnect(ctx, id, timeout, false)
	if e != nil {
		return nil, e
	}
	return n.stream, nil
}
func nativeBLEDFUOpen(ctx context.Context, id string, timeout time.Duration) (dfu.BLETransport, error) {
	return nativeBLEConnect(ctx, id, timeout, true)
}
func nativeBLEConnect(ctx context.Context, id string, timeout time.Duration, dfuMode bool) (*nativeConnection, error) {
	serviceUUID, rxUUID, txUUID := NUSServiceUUID, NUSRXUUID, NUSTXUUID
	if dfuMode {
		serviceUUID = DFUServiceUUID
		rxUUID = DFUControlUUID
		txUUID = DFUControlUUID
	}
	uuid, e := cbgo.ParseUUID(id)
	if e != nil || len(uuid) != 16 {
		return nil, fmt.Errorf("macOS BLE address must be a peripheral UUID")
	}
	id = uuid.String()
	d := bleNative
	if e = d.lock(ctx); e != nil {
		return nil, e
	}
	defer d.unlock()
	if e = d.ready(ctx); e != nil {
		return nil, e
	}
	d.mu.Lock()
	closing := d.closing[id]
	already := d.active[id] != nil
	d.mu.Unlock()
	if already {
		return nil, fmt.Errorf("BLE device is already connected")
	}
	if closing != nil {
		select {
		case <-closing:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	peers := d.manager.RetrievePeripheralsWithIdentifiers([]cbgo.UUID{uuid})
	if len(peers) == 0 {
		return nil, fmt.Errorf("unknown peripheral UUID; run ble scan first")
	}
	p := peers[0]
	connected := make(chan error, 1)
	d.mu.Lock()
	d.pending[id] = connected
	d.mu.Unlock()
	defer func() { d.mu.Lock(); delete(d.pending, id); d.mu.Unlock() }()
	d.manager.Connect(p, nil)
	if e = waitBLE(ctx, connected, nil); e != nil {
		d.disconnect(p)
		return nil, e
	}
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	n := &nativeConnection{dfuMode: dfuMode, responses: make(chan []byte, 16), owner: d, peripheral: p, done: make(chan struct{}), services: make(chan error, 1), chars: make(chan error, 1), notify: make(chan error, 1), ack: make(chan error, 1), timeout: timeout}
	n.stream = newNotificationStream(20, func(b []byte) (int, error) {
		select {
		case <-n.done:
			return 0, ErrClosed
		default:
		}
		for {
			select {
			case <-n.ack:
				continue
			default:
			}
			break
		}
		p.WriteCharacteristic(b, n.rx, true)
		timer := time.NewTimer(n.timeout)
		defer timer.Stop()
		select {
		case e := <-n.ack:
			if e != nil {
				return 0, e
			}
			return len(b), nil
		case <-n.done:
			return 0, ErrClosed
		case <-timer.C:
			return 0, fmt.Errorf("BLE write acknowledgement timeout")
		}
	}, func() error { n.abort(ErrClosed); d.disconnect(p); return nil })
	p.SetDelegate(n)
	failed := true
	defer func() {
		if failed {
			_ = n.stream.Close()
		}
	}()
	p.DiscoverServices([]cbgo.UUID{cbgo.MustParseUUID(serviceUUID)})
	if e = waitBLE(ctx, n.services, n.done); e != nil {
		return nil, e
	}
	var service cbgo.Service
	haveService := false
	for _, s := range p.Services() {
		if bleUUIDEqual(s.UUID().String(), serviceUUID) {
			service = s
			haveService = true
			break
		}
	}
	if !haveService {
		return nil, fmt.Errorf("Nordic UART service not found")
	}
	p.DiscoverCharacteristics([]cbgo.UUID{cbgo.MustParseUUID(rxUUID), cbgo.MustParseUUID(txUUID), cbgo.MustParseUUID(DFUPacketUUID)}, service)
	if e = waitBLE(ctx, n.chars, n.done); e != nil {
		return nil, e
	}
	haveRX, haveTX := false, false
	havePacket := false
	for _, c := range service.Characteristics() {
		if bleUUIDEqual(c.UUID().String(), DFUPacketUUID) {
			n.firmware = c
			havePacket = true
		}
		if bleUUIDEqual(c.UUID().String(), rxUUID) || (n.dfuMode && bleUUIDEqual(c.UUID().String(), DFUControlUUID)) {
			n.rx = c
			haveRX = true
		}
		if bleUUIDEqual(c.UUID().String(), txUUID) || (n.dfuMode && bleUUIDEqual(c.UUID().String(), DFUControlUUID)) {
			n.tx = c
			haveTX = true
		}
	}
	if !haveRX || !haveTX || (dfuMode && !havePacket) {
		return nil, fmt.Errorf("Nordic UART characteristics incomplete")
	}
	if dfuMode && (n.rx.Properties()&cbgo.CharacteristicPropertyWrite == 0 || n.firmware.Properties()&cbgo.CharacteristicPropertyWriteWithoutResponse == 0 || n.tx.Properties()&(cbgo.CharacteristicPropertyNotify|cbgo.CharacteristicPropertyIndicate) == 0) {
		return nil, fmt.Errorf("Secure DFU characteristic permissions are incompatible")
	}
	p.SetNotify(true, n.tx)
	if e = waitBLE(ctx, n.notify, n.done); e != nil {
		return nil, e
	}
	d.mu.Lock()
	d.active[id] = n
	d.mu.Unlock()
	failed = false
	return n, nil
}

func (n *nativeConnection) WriteControl(ctx context.Context, b []byte) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	_, e := n.stream.Write(b)
	return e
}
func (n *nativeConnection) WritePacket(ctx context.Context, b []byte) error {
	if len(b) > 20 {
		return fmt.Errorf("DFU packet exceeds 20 bytes")
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-n.done:
		return ErrClosed
	default:
	}
	n.peripheral.WriteCharacteristic(b, n.firmware, false)
	return nil
}
func (n *nativeConnection) Notification(ctx context.Context) ([]byte, error) {
	select {
	case b := <-n.responses:
		return b, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-n.done:
		return nil, ErrClosed
	}
}
func (n *nativeConnection) Close() error { return n.stream.Close() }
