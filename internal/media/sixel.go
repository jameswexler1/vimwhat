package media

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"

	"github.com/charmbracelet/x/ansi"
)

type SixelPlacement struct {
	Identifier string
	X          int
	Y          int
	MaxWidth   int
	MaxHeight  int
	Payload    []string
}

type SixelManager struct {
	mu     sync.Mutex
	out    io.Writer
	active map[string]SixelPlacement
	epoch  int
}

func NewSixelManagerForWriter(w io.Writer) *SixelManager {
	return &SixelManager{out: w}
}

func (m *SixelManager) Epoch() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.epoch
}

func (m *SixelManager) Invalidate() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.epoch++
}

func (m *SixelManager) SyncEpoch(ctx context.Context, epoch int, placements []SixelPlacement) error {
	next := make(map[string]SixelPlacement, len(placements))
	for _, placement := range placements {
		if placement.Identifier == "" {
			return fmt.Errorf("sixel identifier is empty")
		}
		if placement.MaxWidth <= 0 || placement.MaxHeight <= 0 {
			return fmt.Errorf("sixel size must be positive")
		}
		if len(placement.Payload) == 0 {
			return fmt.Errorf("sixel payload is empty")
		}
		next[placement.Identifier] = placement
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	if epoch != m.epoch {
		return nil
	}
	if m.out == nil {
		return fmt.Errorf("sixel output is unavailable")
	}
	if m.active == nil {
		m.active = map[string]SixelPlacement{}
	}
	if len(next) == 0 && len(m.active) == 0 {
		return nil
	}

	nextActive := make(map[string]SixelPlacement, len(next))
	buf := &bytes.Buffer{}
	buf.WriteString(ansi.SaveCursor)
	wrote := false
	for _, identifier := range sortedSixelPlacementKeys(m.active) {
		placement := m.active[identifier]
		nextPlacement, keep := next[identifier]
		if keep && sameSixelPlacement(placement, nextPlacement) {
			continue
		}
		writeSixelClear(buf, placement)
		wrote = true
	}
	for _, identifier := range sortedSixelPlacementKeys(next) {
		placement := next[identifier]
		if sameSixelPlacement(m.active[identifier], placement) {
			nextActive[identifier] = placement
			continue
		}
		writeSixelClear(buf, placement)
		writeSixelPayload(buf, placement)
		wrote = true
		nextActive[identifier] = placement
	}
	buf.WriteString(ansi.RestoreCursor)

	if !wrote {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	_, err := m.out.Write(buf.Bytes())
	if err != nil {
		return fmt.Errorf("write sixel output: %w", err)
	}
	m.active = nextActive
	return nil
}

func (m *SixelManager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.out == nil || len(m.active) == 0 {
		m.active = nil
		return nil
	}
	buf := &bytes.Buffer{}
	buf.WriteString(ansi.SaveCursor)
	for _, identifier := range sortedSixelPlacementKeys(m.active) {
		writeSixelClear(buf, m.active[identifier])
	}
	buf.WriteString(ansi.RestoreCursor)
	if _, err := m.out.Write(buf.Bytes()); err != nil {
		return fmt.Errorf("clear sixel output: %w", err)
	}
	m.active = nil
	return nil
}

func SixelPlacementsSignature(placements []SixelPlacement) string {
	if len(placements) == 0 {
		return ""
	}
	parts := make([]string, 0, len(placements))
	for _, placement := range placements {
		parts = append(parts, fmt.Sprintf(
			"%q %d %d %d %d %s",
			placement.Identifier,
			placement.X,
			placement.Y,
			placement.MaxWidth,
			placement.MaxHeight,
			sixelPayloadDigest(placement.Payload),
		))
	}
	sort.Strings(parts)
	return strings.Join(parts, "\n")
}

func writeSixelClear(buf *bytes.Buffer, placement SixelPlacement) {
	width := maxInt(1, placement.MaxWidth)
	height := maxInt(1, placement.MaxHeight)
	padding := strings.Repeat(" ", width)
	for row := 0; row < height; row++ {
		buf.WriteString(ansi.CursorPosition(placement.X+1, placement.Y+row+1))
		buf.WriteString(padding)
	}
}

func writeSixelPayload(buf *bytes.Buffer, placement SixelPlacement) {
	buf.WriteString(ansi.CursorPosition(placement.X+1, placement.Y+1))
	buf.WriteString(strings.Join(placement.Payload, "\n"))
}

func sameSixelPlacement(left, right SixelPlacement) bool {
	return left.Identifier == right.Identifier &&
		left.X == right.X &&
		left.Y == right.Y &&
		left.MaxWidth == right.MaxWidth &&
		left.MaxHeight == right.MaxHeight &&
		slicesEqual(left.Payload, right.Payload)
}

func slicesEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func sixelPayloadDigest(payload []string) string {
	sum := sha1.Sum([]byte(strings.Join(payload, "\n")))
	return hex.EncodeToString(sum[:])
}

func sortedSixelPlacementKeys(values map[string]SixelPlacement) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
