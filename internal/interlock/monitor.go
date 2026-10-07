package interlock

import "sync"

type Monitor struct {
	mu     sync.RWMutex
	alarms []string
}

func NewMonitor() *Monitor           { return &Monitor{alarms: make([]string, 0)} }
func (m *Monitor) Alarm(name string) { m.mu.Lock(); m.alarms = append(m.alarms, name); m.mu.Unlock() }
func (m *Monitor) Alarms() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]string, len(m.alarms))
	copy(out, m.alarms)
	return out
}

// Clear removes every active alarm with the given name and reports whether
// at least one was removed.
func (m *Monitor) Clear(name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	kept := m.alarms[:0]
	removed := false
	for _, alarm := range m.alarms {
		if alarm == name {
			removed = true
			continue
		}
		kept = append(kept, alarm)
	}
	m.alarms = kept
	return removed
}
