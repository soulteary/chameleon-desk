//go:build !darwin || !cgo

package chameleon

import (
	"context"
	"github.com/soulteary/chameleon-desk/dfu"
	"io"
	"time"
)

func nativeBLEScan(context.Context, time.Duration) ([]BLEDevice, error) {
	return nil, ErrBLEUnavailable
}
func nativeBLEOpen(context.Context, string, time.Duration) (io.ReadWriteCloser, error) {
	return nil, ErrBLEUnavailable
}

func nativeBLEDFUOpen(context.Context, string, time.Duration) (dfu.BLETransport, error) {
	return nil, ErrBLEUnavailable
}
