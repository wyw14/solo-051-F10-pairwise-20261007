package interlock

import (
	"fmt"
	"github.com/wyw14/cry-161/internal/model"
	"sort"
	"sync"
	"time"
)

// Override is an operator review that accepts one or more violated interlock
// conditions. It is valid only for the fault generation it was recorded
// against and only until its deadline.
type Override struct {
	ID         model.ID  `json:"id"`
	RunID      model.ID  `json:"run_id"`
	Operator   string    `json:"operator"`
	Reasons    []Reason  `json:"reasons"`
	Generation int       `json:"generation"`
	Deadline   time.Time `json:"deadline"`
	ReviewedAt time.Time `json:"reviewed_at"`
}

func (o Override) Active(generation int, now time.Time) bool {
	return o.Generation == generation && !now.UTC().After(o.Deadline)
}

// OverrideBook tracks the fault generation of each run and the reviews
// recorded against those generations.
type OverrideBook struct {
	mu        sync.RWMutex
	faults    map[model.ID]*faultState
	overrides map[model.ID][]Override
}

type faultState struct {
	generation int
	codes      []Reason
}

func NewOverrideBook() *OverrideBook {
	return &OverrideBook{faults: map[model.ID]*faultState{}, overrides: map[model.ID][]Override{}}
}

// Observe records the currently violated condition set for a run and returns
// its fault generation. The generation starts at one and advances whenever
// the violated set changes (a condition clears, appears, or is replaced).
func (b *OverrideBook) Observe(runID model.ID, codes []Reason) int {
	sorted := normalizedReasons(codes)
	b.mu.Lock()
	defer b.mu.Unlock()
	state, ok := b.faults[runID]
	if !ok {
		state = &faultState{generation: 1, codes: sorted}
		b.faults[runID] = state
		return state.generation
	}
	if !sameReasons(state.codes, sorted) {
		state.generation++
		state.codes = sorted
	}
	return state.generation
}

// Generation returns the current fault generation for a run. A run that has
// never been evaluated returns zero.
func (b *OverrideBook) Generation(runID model.ID) int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if state, ok := b.faults[runID]; ok {
		return state.generation
	}
	return 0
}

// Review records an operator override. expectedGeneration must match the
// generation of the decision the operator reviewed; otherwise a new fault
// generation has appeared and the review is refused. Only conditions that
// are actually violated in that generation may be reviewed.
func (b *OverrideBook) Review(runID model.ID, operator string, reasons []Reason, expectedGeneration int, deadline time.Time, now time.Time) (Override, error) {
	if operator == "" {
		return Override{}, fmt.Errorf("review operator is required")
	}
	deadline = deadline.UTC()
	now = now.UTC()
	if !deadline.After(now) {
		return Override{}, fmt.Errorf("review deadline must be in the future")
	}
	codes := normalizedReasons(reasons)
	if len(codes) == 0 {
		return Override{}, fmt.Errorf("review must name at least one reason")
	}
	for _, code := range codes {
		if !code.Valid() {
			return Override{}, fmt.Errorf("unknown interlock reason %q", code)
		}
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	state, ok := b.faults[runID]
	if !ok {
		return Override{}, fmt.Errorf("run has no interlock decision to review")
	}
	if expectedGeneration == 0 {
		expectedGeneration = state.generation
	}
	if state.generation != expectedGeneration {
		return Override{}, fmt.Errorf("%w: decision was generation %d, current generation is %d", ErrNewFaultGeneration, expectedGeneration, state.generation)
	}
	current := map[Reason]bool{}
	for _, code := range state.codes {
		current[code] = true
	}
	for _, code := range codes {
		if !current[code] {
			return Override{}, fmt.Errorf("reason %s is not violated in generation %d", code, state.generation)
		}
	}

	override := Override{ID: model.NewID(), RunID: runID, Operator: operator, Reasons: codes, Generation: state.generation, Deadline: deadline, ReviewedAt: now}
	b.overrides[runID] = append(b.overrides[runID], override)
	return override, nil
}

// ForRun returns every review recorded for a run, oldest first.
func (b *OverrideBook) ForRun(runID model.ID) []Override {
	b.mu.RLock()
	defer b.mu.RUnlock()
	values := b.overrides[runID]
	out := make([]Override, len(values))
	copy(out, values)
	return out
}

// ErrNewFaultGeneration signals that a review targets an obsolete decision.
var ErrNewFaultGeneration = fmt.Errorf("new fault generation")

func normalizedReasons(codes []Reason) []Reason {
	seen := map[Reason]bool{}
	out := make([]Reason, 0, len(codes))
	for _, code := range codes {
		if seen[code] {
			continue
		}
		seen[code] = true
		out = append(out, code)
	}
	sort.Slice(out, func(i, j int) bool { return rank(out[i]) < rank(out[j]) })
	return out
}

func sameReasons(a, b []Reason) bool {
	if len(a) != len(b) {
		return false
	}
	for index := range a {
		if a[index] != b[index] {
			return false
		}
	}
	return true
}
