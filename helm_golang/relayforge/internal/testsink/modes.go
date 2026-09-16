package testsink

import "sync"

// ModeController implements test/mode toggling (mirrors test_sink/modes.py).
type ModeController struct {
	mu         sync.Mutex
	mode       string
	failFirstN int
	attempts   map[string]int
}

func NewModeController() *ModeController {
	return &ModeController{
		mode:       "normal",
		failFirstN: 0,
		attempts:   map[string]int{},
	}
}

// SetMode sets the mode and the fail-first count, clearing per-id attempt
// counters (mirrors set_mode).
func (m *ModeController) SetMode(mode string, n int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mode = mode
	m.failFirstN = n
	m.attempts = map[string]int{}
}

// Reset restores normal mode with no fail-first.
func (m *ModeController) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mode = "normal"
	m.failFirstN = 0
	m.attempts = map[string]int{}
}

// Mode returns the current mode string.
func (m *ModeController) Mode() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mode
}

// FailFirstN returns the current fail-first counter.
func (m *ModeController) FailFirstN() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.failFirstN
}

// CheckMode increments the per-id attempt counter and returns the action for a
// delivery (accept|fail|drop|reject|slow).
func (m *ModeController) CheckMode(id string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.attempts[id]++
	n := m.attempts[id]

	switch m.mode {
	case "fail-first":
		if n <= m.failFirstN {
			return "fail"
		}
		return "accept"
	case "accept-and-drop":
		return "drop"
	case "reject":
		return "reject"
	case "slow":
		return "slow"
	default:
		return "accept"
	}
}
