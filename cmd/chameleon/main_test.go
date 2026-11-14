package main

import (
	"context"
	"errors"
	"testing"
	"time"

	chameleon "github.com/soulteary/chameleon-desk"
)

type scannerFunc func(context.Context) ([]chameleon.Card, error)

func (f scannerFunc) ScanISO14443A(ctx context.Context) ([]chameleon.Card, error) { return f(ctx) }

func TestWaitForCardThenSuccess(t *testing.T) {
	calls := 0
	scanner := scannerFunc(func(ctx context.Context) ([]chameleon.Card, error) {
		calls++
		if _, ok := ctx.Deadline(); ok {
			t.Error("poll deadline must not interrupt mode restoration")
		}
		if calls == 1 {
			return nil, chameleon.ErrNoCard
		}
		return []chameleon.Card{{UID: chameleon.HexBytes{1, 2, 3, 4}}}, nil
	})
	cards, e := scanCards(context.Background(), scanner, time.Second, time.Millisecond)
	if e != nil || calls != 2 || len(cards) != 1 {
		t.Fatalf("%+v %v calls=%d", cards, e, calls)
	}
}
func TestWaitDoesNotRetryFailedRestore(t *testing.T) {
	calls := 0
	scanner := scannerFunc(func(context.Context) ([]chameleon.Card, error) {
		calls++
		return nil, errors.Join(chameleon.ErrNoCard, &chameleon.ModeRestoreError{Err: chameleon.ErrClosed})
	})
	_, e := scanCards(context.Background(), scanner, time.Second, time.Millisecond)
	var restore *chameleon.ModeRestoreError
	if !errors.As(e, &restore) || calls != 1 {
		t.Fatalf("%v calls=%d", e, calls)
	}
}
func TestWaitExpiry(t *testing.T) {
	calls := 0
	scanner := scannerFunc(func(context.Context) ([]chameleon.Card, error) { calls++; return nil, chameleon.ErrNoCard })
	_, e := scanCards(context.Background(), scanner, time.Millisecond, time.Second)
	if !errors.Is(e, chameleon.ErrNoCard) || calls != 1 {
		t.Fatalf("%v calls=%d", e, calls)
	}
}
func TestOneShotNoCard(t *testing.T) {
	scanner := scannerFunc(func(context.Context) ([]chameleon.Card, error) { return nil, chameleon.ErrNoCard })
	if _, e := scanCards(context.Background(), scanner, 0, time.Second); !errors.Is(e, chameleon.ErrNoCard) {
		t.Fatal(e)
	}
}
