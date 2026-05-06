//go:build windows

package media

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestSixelManagerDrawsSkipsAndClearsPlacements(t *testing.T) {
	var buf bytes.Buffer
	manager := NewSixelManagerForWriter(&buf)
	placement := SixelPlacement{
		Identifier: "image-1",
		X:          1,
		Y:          2,
		MaxWidth:   3,
		MaxHeight:  2,
		Payload:    []string{"\x1bPqfake\x1b\\"},
	}

	if err := manager.SyncEpoch(context.Background(), manager.Epoch(), []SixelPlacement{placement}); err != nil {
		t.Fatalf("SyncEpoch() error = %v", err)
	}
	if out := buf.String(); !strings.Contains(out, "\x1b[3;2H") || !strings.Contains(out, placement.Payload[0]) {
		t.Fatalf("sixel output = %q, want cursor placement and payload", out)
	}

	buf.Reset()
	if err := manager.SyncEpoch(context.Background(), manager.Epoch(), []SixelPlacement{placement}); err != nil {
		t.Fatalf("unchanged SyncEpoch() error = %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("unchanged placement wrote output: %q", buf.String())
	}

	buf.Reset()
	moved := placement
	moved.X = 4
	if err := manager.SyncEpoch(context.Background(), manager.Epoch(), []SixelPlacement{moved}); err != nil {
		t.Fatalf("moved SyncEpoch() error = %v", err)
	}
	if out := buf.String(); !strings.Contains(out, "\x1b[3;2H") || !strings.Contains(out, "\x1b[3;5H") || !strings.Contains(out, moved.Payload[0]) {
		t.Fatalf("moved sixel output = %q, want old clear, new cursor, and payload", out)
	}

	buf.Reset()
	if err := manager.SyncEpoch(context.Background(), manager.Epoch(), nil); err != nil {
		t.Fatalf("clear SyncEpoch() error = %v", err)
	}
	if out := buf.String(); !strings.Contains(out, "\x1b[3;5H") || strings.Contains(out, moved.Payload[0]) {
		t.Fatalf("clear sixel output = %q, want clear without payload", out)
	}
}

func TestSixelManagerSyncEpochIgnoresStalePlacements(t *testing.T) {
	var buf bytes.Buffer
	manager := NewSixelManagerForWriter(&buf)
	staleEpoch := manager.Epoch()
	manager.Invalidate()

	if err := manager.SyncEpoch(context.Background(), staleEpoch, []SixelPlacement{{
		Identifier: "image-1",
		X:          1,
		Y:          2,
		MaxWidth:   3,
		MaxHeight:  2,
		Payload:    []string{"\x1bPqfake\x1b\\"},
	}}); err != nil {
		t.Fatalf("stale SyncEpoch() error = %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("stale SyncEpoch() wrote output: %q", buf.String())
	}
}

func TestSixelManagerSyncEpochHonorsCanceledContext(t *testing.T) {
	var buf bytes.Buffer
	manager := NewSixelManagerForWriter(&buf)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := manager.SyncEpoch(ctx, manager.Epoch(), []SixelPlacement{{
		Identifier: "image-1",
		X:          1,
		Y:          2,
		MaxWidth:   3,
		MaxHeight:  2,
		Payload:    []string{"\x1bPqfake\x1b\\"},
	}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("SyncEpoch() error = %v, want context canceled", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("canceled SyncEpoch() wrote output: %q", buf.String())
	}
}
