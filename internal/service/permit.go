package service

import (
	"fmt"
	"time"

	"github.com/wyw14/cry-161/internal/interlock"
	"github.com/wyw14/cry-161/internal/journal"
	"github.com/wyw14/cry-161/internal/model"
	"github.com/wyw14/cry-161/internal/run"
)

// PermitConditions snapshots the current interlock inputs for a run.
func (s *Service) PermitConditions(runtime *Runtime, value model.Run) interlock.Conditions {
	starter := &run.Starter{Registry: s.Runs, Vacuum: s.Vacuum, Shutter: runtime.Shutter, Source: s.Source, Alarms: runtime.Alarms, Overrides: runtime.Overrides}
	return starter.Conditions(value)
}

// EvaluatePermit decides the interlock for the run's current conditions
// and records the decision, including per-item reasons, in the audit log.
func (s *Service) EvaluatePermit(runtime *Runtime, runID model.ID) (interlock.Decision, error) {
	value, ok := s.Runs.Get(runID)
	if !ok {
		return interlock.Decision{}, fmt.Errorf("run not found")
	}
	decision := interlock.Decide(runID, s.PermitConditions(runtime, *value), runtime.Overrides, time.Now().UTC())
	runtime.InterlockAudit.RecordDecision(decision)
	return decision, nil
}

// GrantOverride registers a reviewed release for the run's current fault
// generation. The denial being reviewed is evaluated and audited first, so
// the grant is always bound to a recorded fault set; the grant stops
// applying as soon as the fault set changes or the ttl elapses.
func (s *Service) GrantOverride(runtime *Runtime, runID model.ID, operator, note string, ttl time.Duration) (interlock.Override, interlock.Decision, error) {
	decision, err := s.EvaluatePermit(runtime, runID)
	if err != nil {
		return interlock.Override{}, decision, err
	}
	if decision.Allowed {
		return interlock.Override{}, decision, fmt.Errorf("no active faults to override")
	}
	grant, err := runtime.Overrides.Grant(runID, operator, note, ttl, time.Now().UTC())
	if err != nil {
		return interlock.Override{}, decision, err
	}
	runtime.Timeline.Add(runID, "override", fmt.Sprintf("generation %d by %s until %s", grant.Generation, grant.Operator, grant.ExpiresAt.Format(time.RFC3339)))
	_ = s.Journal.Append(journal.OverrideGranted(runID, grant.Operator, grant.Generation))
	return grant, decision, nil
}
