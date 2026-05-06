package media

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"sync"
	"time"
)

type Placement struct {
	Identifier string
	X          int
	Y          int
	MaxWidth   int
	MaxHeight  int
	Path       string
	Scaler     string
}

type OverlayManager struct {
	output string

	mu     sync.Mutex
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	active map[string]Placement
	epoch  int
}

type overlayCommand struct {
	Action     string `json:"action"`
	Identifier string `json:"identifier"`
	X          int    `json:"x,omitempty"`
	Y          int    `json:"y,omitempty"`
	MaxWidth   int    `json:"max_width,omitempty"`
	MaxHeight  int    `json:"max_height,omitempty"`
	Path       string `json:"path,omitempty"`
	Scaler     string `json:"scaler,omitempty"`
}

func NewOverlayManager(output string) *OverlayManager {
	return &OverlayManager{output: output}
}

func NewOverlayManagerForWriter(w io.Writer) *OverlayManager {
	return &OverlayManager{stdin: nopWriteCloser{Writer: w}}
}

func (m *OverlayManager) Place(ctx context.Context, placement Placement) error {
	return m.Sync(ctx, []Placement{placement})
}

func (m *OverlayManager) Sync(ctx context.Context, placements []Placement) error {
	return m.sync(ctx, placements, 0, false)
}

func (m *OverlayManager) Epoch() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.epoch
}

func (m *OverlayManager) Invalidate() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.epoch++
}

func (m *OverlayManager) SyncEpoch(ctx context.Context, epoch int, placements []Placement) error {
	return m.sync(ctx, placements, epoch, true)
}

func (m *OverlayManager) sync(ctx context.Context, placements []Placement, epoch int, checkEpoch bool) error {
	next := make(map[string]Placement, len(placements))
	for _, placement := range placements {
		if placement.Identifier == "" {
			return fmt.Errorf("overlay identifier is empty")
		}
		if placement.Path == "" {
			return fmt.Errorf("overlay path is empty")
		}
		if placement.MaxWidth <= 0 || placement.MaxHeight <= 0 {
			return fmt.Errorf("overlay size must be positive")
		}
		if placement.Scaler == "" {
			placement.Scaler = "fit_contain"
		}
		next[placement.Identifier] = placement
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if checkEpoch && epoch != m.epoch {
		return nil
	}
	if m.active == nil {
		m.active = map[string]Placement{}
	}
	if len(next) == 0 && len(m.active) == 0 {
		return nil
	}
	if err := m.ensureStarted(ctx); err != nil {
		return err
	}

	for _, identifier := range sortedPlacementKeys(m.active) {
		if _, ok := next[identifier]; ok {
			continue
		}
		if err := m.send(ctx, overlayCommand{Action: "remove", Identifier: identifier}); err != nil {
			return err
		}
		delete(m.active, identifier)
	}

	for _, identifier := range sortedPlacementKeys(next) {
		placement := next[identifier]
		if samePlacement(m.active[identifier], placement) {
			continue
		}
		if _, ok := m.active[identifier]; ok {
			if err := m.send(ctx, overlayCommand{Action: "remove", Identifier: identifier}); err != nil {
				return err
			}
		}
		if err := m.send(ctx, overlayCommand{
			Action:     "add",
			Identifier: placement.Identifier,
			X:          placement.X,
			Y:          placement.Y,
			MaxWidth:   placement.MaxWidth,
			MaxHeight:  placement.MaxHeight,
			Path:       placement.Path,
			Scaler:     placement.Scaler,
		}); err != nil {
			return err
		}
		m.active[identifier] = placement
	}
	return nil
}

func OverlayAddCommandJSON(placement Placement) string {
	if placement.Scaler == "" {
		placement.Scaler = "fit_contain"
	}
	data, err := json.Marshal(overlayCommand{
		Action:     "add",
		Identifier: placement.Identifier,
		X:          placement.X,
		Y:          placement.Y,
		MaxWidth:   placement.MaxWidth,
		MaxHeight:  placement.MaxHeight,
		Path:       placement.Path,
		Scaler:     placement.Scaler,
	})
	if err != nil {
		return ""
	}
	return string(data)
}

func (m *OverlayManager) Remove(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.active) == 0 {
		return nil
	}
	if err := m.ensureStarted(ctx); err != nil {
		return err
	}
	for _, identifier := range sortedPlacementKeys(m.active) {
		if err := m.send(ctx, overlayCommand{Action: "remove", Identifier: identifier}); err != nil {
			return err
		}
		delete(m.active, identifier)
	}
	return nil
}

func (m *OverlayManager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var err error
	if len(m.active) > 0 && m.stdin != nil {
		for _, identifier := range sortedPlacementKeys(m.active) {
			if removeErr := m.send(context.Background(), overlayCommand{Action: "remove", Identifier: identifier}); err == nil {
				err = removeErr
			}
			delete(m.active, identifier)
		}
	}
	if m.stdin != nil {
		if closeErr := m.stdin.Close(); err == nil {
			err = closeErr
		}
		m.stdin = nil
	}
	if m.cmd == nil || m.cmd.Process == nil {
		return err
	}

	done := make(chan error, 1)
	go func() {
		done <- m.cmd.Wait()
	}()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		_ = m.cmd.Process.Kill()
		<-done
	}
	m.cmd = nil
	return err
}

func (m *OverlayManager) ensureStarted(ctx context.Context) error {
	if m.stdin != nil {
		return nil
	}
	if m.active == nil {
		m.active = map[string]Placement{}
	}
	output := m.output
	if output == "" {
		output = DetectUeberzugPPOutput()
	}
	if output == "" {
		return fmt.Errorf("ueberzug++ output adapter is unavailable")
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	args := []string{"layer", "--silent", "--parser", "json", "--output", output}
	cmd := exec.Command("ueberzugpp", args...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("ueberzug++ stdin: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start ueberzug++: %w", err)
	}
	m.cmd = cmd
	m.stdin = stdin
	return nil
}

func (m *OverlayManager) send(ctx context.Context, command overlayCommand) error {
	data, err := json.Marshal(command)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	if _, err := m.stdin.Write(data); err != nil {
		return fmt.Errorf("write ueberzug++ command: %w", err)
	}
	return nil
}

func samePlacement(left, right Placement) bool {
	return left.Identifier == right.Identifier &&
		left.X == right.X &&
		left.Y == right.Y &&
		left.MaxWidth == right.MaxWidth &&
		left.MaxHeight == right.MaxHeight &&
		left.Path == right.Path &&
		left.Scaler == right.Scaler
}

func sortedPlacementKeys(values map[string]Placement) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

type nopWriteCloser struct {
	io.Writer
}

func (w nopWriteCloser) Close() error {
	return nil
}
