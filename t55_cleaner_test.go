package chameleon

import (
	"context"
	"testing"
)

func em410xScan(id []byte) readerStep {
	return readerStep{command: 3000, request: []byte{}, status: 0x40, data: append([]byte{0x00, 0x01}, id...)}
}

func t55Write(probe, newPassword, candidate []byte, status uint16) readerStep {
	req := append(append(append([]byte(nil), probe...), newPassword...), candidate...)
	return readerStep{command: 3001, request: req, status: status}
}

func TestCleanT55PasswordFound(t *testing.T) {
	probe := []byte{0x11, 0x22, 0x33, 0x44, 0x55}
	np := []byte{0, 0, 0, 0}
	original := []byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE}
	good := []byte{0x51, 0x24, 0x36, 0x48}
	bad := []byte{0x00, 0x00, 0x00, 0x00}
	c := readerSimulator(t, []readerStep{
		{command: 1002, status: 0x68, data: []byte{1}}, em410xScan(original), // initial scan
		{command: 1002, status: 0x68, data: []byte{1}}, t55Write(probe, np, bad, 0x40), // write with wrong pw "succeeds" on wire
		{command: 1002, status: 0x68, data: []byte{1}}, em410xScan(original), // but readback still old -> keep going
		{command: 1002, status: 0x68, data: []byte{1}}, t55Write(probe, np, good, 0x40),
		{command: 1002, status: 0x68, data: []byte{1}}, em410xScan(probe), // readback matches -> found
	})
	attempts := 0
	out, e := c.CleanT55Password(context.Background(), [][]byte{bad, good}, probe, np, true, func(int, []byte) error { attempts++; return nil })
	if e != nil || !out.Found || out.Password.String() != "51243648" || out.Attempts != 2 || attempts != 2 {
		t.Fatalf("%+v %v (progress %d)", out, e, attempts)
	}
	if out.OriginalID.String() != "AABBCCDDEE" {
		t.Fatalf("original id %s", out.OriginalID)
	}
}

func TestCleanT55PasswordGuards(t *testing.T) {
	probe := []byte{1, 2, 3, 4, 5}
	np := []byte{0, 0, 0, 0}
	c := readerSimulator(t, nil)
	// allowWrite false is refused before any device traffic.
	if _, e := c.CleanT55Password(context.Background(), [][]byte{{0, 0, 0, 0}}, probe, np, false, func(int, []byte) error { return nil }); e == nil {
		t.Fatal("unauthorized clean accepted")
	}
	if _, e := c.CleanT55Password(context.Background(), [][]byte{{0, 0, 0, 0}}, probe, np, true, nil); e == nil {
		t.Fatal("missing progress accepted")
	}
	// A probe equal to the current ID cannot detect success.
	c = readerSimulator(t, []readerStep{{command: 1002, status: 0x68, data: []byte{1}}, em410xScan(probe)})
	if _, e := c.CleanT55Password(context.Background(), [][]byte{{0, 0, 0, 0}}, probe, np, true, func(int, []byte) error { return nil }); e == nil {
		t.Fatal("probe equal to current ID accepted")
	}
}

func TestCleanT55PasswordNotFound(t *testing.T) {
	probe := []byte{0x11, 0x22, 0x33, 0x44, 0x55}
	np := []byte{0, 0, 0, 0}
	original := []byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE}
	cand := []byte{0x01, 0x02, 0x03, 0x04}
	c := readerSimulator(t, []readerStep{
		{command: 1002, status: 0x68, data: []byte{1}}, em410xScan(original),
		{command: 1002, status: 0x68, data: []byte{1}}, t55Write(probe, np, cand, 0x40),
		{command: 1002, status: 0x68, data: []byte{1}}, em410xScan(original),
	})
	out, e := c.CleanT55Password(context.Background(), [][]byte{cand}, probe, np, true, func(int, []byte) error { return nil })
	if e != nil || out.Found || out.Attempts != 1 {
		t.Fatalf("%+v %v", out, e)
	}
}
