package media

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
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
	active string
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

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.ensureStarted(ctx); err != nil {
		return err
	}
	if m.active != "" {
		if err := m.send(overlayCommand{Action: "remove", Identifier: m.active}); err != nil {
			return err
		}
		m.active = ""
	}
	if err := m.send(overlayCommand{
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
	m.active = placement.Identifier
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

	if m.active == "" {
		return nil
	}
	if err := m.ensureStarted(ctx); err != nil {
		return err
	}
	if err := m.send(overlayCommand{Action: "remove", Identifier: m.active}); err != nil {
		return err
	}
	m.active = ""
	return nil
}

func (m *OverlayManager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var err error
	if m.active != "" && m.stdin != nil {
		err = m.send(overlayCommand{Action: "remove", Identifier: m.active})
		m.active = ""
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
	output := m.output
	if output == "" {
		output = DetectUeberzugPPOutput()
	}
	if output == "" {
		return fmt.Errorf("ueberzug++ output adapter is unavailable")
	}

	args := []string{"layer", "--silent", "--parser", "json", "--output", output}
	cmd := exec.CommandContext(ctx, "ueberzugpp", args...)
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

func (m *OverlayManager) send(command overlayCommand) error {
	data, err := json.Marshal(command)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if _, err := m.stdin.Write(data); err != nil {
		return fmt.Errorf("write ueberzug++ command: %w", err)
	}
	return nil
}

type nopWriteCloser struct {
	io.Writer
}

func (w nopWriteCloser) Close() error {
	return nil
}
