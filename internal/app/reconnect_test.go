package app

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestInitialConnectionRetriesWithBoundedBackoff(t *testing.T) {
	attempts := 0
	var delays []time.Duration
	err := retryConnection(context.Background(), func(context.Context) error {
		attempts++
		if attempts < 9 {
			return errors.New("network unavailable")
		}
		return nil
	}, func(_ error, d time.Duration) { delays = append(delays, d) }, func(context.Context, time.Duration) error { return nil })
	if err != nil || attempts != 9 || delays[0] != time.Second || delays[len(delays)-1] != 30*time.Second {
		t.Fatalf("attempts=%d delays=%v err=%v", attempts, delays, err)
	}
}

func TestConnectionRetryWaitIsCancellable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	err := retryConnection(ctx, func(context.Context) error { return errors.New("offline") }, func(error, time.Duration) { cancel() }, waitReconnect)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("retry cancellation: %v", err)
	}
}
