package interlock

import (
	"fmt"
	"github.com/wyw14/cry-161/internal/model"
	"sort"
	"time"
)

type Decision struct {
	ID         model.ID  `json:"id"`
	RunID      model.ID  `json:"run_id"`
	Allowed    bool      `json:"allowed"`
	Reasons    []string  `json:"reasons"`
	Checks     []Check   `json:"checks"`
	Generation int64     `json:"generation,omitempty"`
	Override   *Override `json:"override,omitempty"`
	At         time.Time `json:"at"`
}

type Audit struct {
	decisions []Decision
}

func NewAudit() *Audit {
	return &Audit{decisions: make([]Decision, 0)}
}

func (a *Audit) Record(runID model.ID, allowed bool, reasons ...string) Decision {
	clean := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		if reason != "" {
			clean = append(clean, reason)
		}
	}
	sort.Strings(clean)
	decision := Decision{ID: model.NewID(), RunID: runID, Allowed: allowed, Reasons: clean, At: time.Now().UTC()}
	a.decisions = append(a.decisions, decision)
	return decision
}

func (a *Audit) Decisions() []Decision {
	result := make([]Decision, len(a.decisions))
	copy(result, a.decisions)
	return result
}

// RecordDecision stores a decision produced by Decide.
func (a *Audit) RecordDecision(decision Decision) Decision {
	a.decisions = append(a.decisions, decision)
	return decision
}

func (a *Audit) Validate(decision Decision) error {
	if decision.ID == "" || decision.RunID == "" {
		return fmt.Errorf("decision identity is missing")
	}
	if !decision.Allowed && len(decision.Reasons) == 0 {
		return fmt.Errorf("denied decision requires reasons")
	}
	return nil
}
