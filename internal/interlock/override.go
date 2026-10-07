package interlock

import (
	"fmt"
	"sync"
	"time"

	"github.com/wyw14/cry-161/internal/model"
)

// Override is a reviewed release granted by an operator for one specific
// fault generation, valid until ExpiresAt. It never applies to a newer
// fault generation: any new or changed fault requires a fresh review.
type Override struct {
	ID         model.ID  `json:"id"`
	RunID      model.ID  `json:"run_id"`
	Operator   string    `json:"operator"`
	Generation int64     `json:"generation"`
	Note       string    `json:"note,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// Expired reports whether the override is no longer valid at now.
func (o Override) Expired(now time.Time) bool { return !now.Before(o.ExpiresAt) }

// Overrides tracks the fault generation of each run and stores review
// grants. The generation increments whenever the run's fault fingerprint
// changes, so a grant is bound to exactly the fault set that was reviewed.
type Overrides struct {
	mu         sync.RWMutex
	generation map[model.ID]int64
	faults     map[model.ID]string
	grants     map[model.ID][]Override
}

func NewOverrides() *Overrides {
	return &Overrides{generation: map[model.ID]int64{}, faults: map[model.ID]string{}, grants: map[model.ID][]Override{}}
}

// Track records the run's current fault fingerprint and returns its fault
// generation. The generation increments on every change of the fault set,
// including a change back to a previously seen set.
func (o *Overrides) Track(runID model.ID, faultKey string) int64 {
	o.mu.Lock()
	defer o.mu.Unlock()
	if previous, seen := o.faults[runID]; !seen || previous != faultKey {
		o.generation[runID]++
		o.faults[runID] = faultKey
	}
	return o.generation[runID]
}

// Grant records a reviewed release bound to the run's current fault
// generation. It requires an operator, a positive ttl, and an active
// fault set — there is nothing to review when no fault is present.
func (o *Overrides) Grant(runID model.ID, operator, note string, ttl time.Duration, now time.Time) (Override, error) {
	if operator == "" {
		return Override{}, fmt.Errorf("override operator is required")
	}
	if ttl <= 0 {
		return Override{}, fmt.Errorf("override ttl must be positive")
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.faults[runID] == "" {
		return Override{}, fmt.Errorf("no active fault generation to override")
	}
	grant := Override{ID: model.NewID(), RunID: runID, Operator: operator, Generation: o.generation[runID], Note: note, CreatedAt: now, ExpiresAt: now.Add(ttl)}
	o.grants[runID] = append(o.grants[runID], grant)
	return grant, nil
}

// Latest returns the most recent grant for the run.
func (o *Overrides) Latest(runID model.ID) (Override, bool) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	grants := o.grants[runID]
	if len(grants) == 0 {
		return Override{}, false
	}
	return grants[len(grants)-1], true
}

// List returns all grants for the run, oldest first.
func (o *Overrides) List(runID model.ID) []Override {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return append([]Override(nil), o.grants[runID]...)
}
