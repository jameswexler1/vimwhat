package app

import (
	"context"
	"time"
)

// retryConnection keeps startup recoverable while the database-backed UI is
// available. Authentication failures require explicit login and are terminal.
func retryConnection(ctx context.Context, connect func(context.Context) error, failed func(error, time.Duration), wait func(context.Context, time.Duration) error) error {
	delay := time.Second
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := connect(ctx)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if isLoggedOutConnectionError(err) {
			return err
		}
		failed(err, delay)
		if err := wait(ctx, delay); err != nil {
			return err
		}
		delay = min(30*time.Second, delay*2)
	}
}

func waitReconnect(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
