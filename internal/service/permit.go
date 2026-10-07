package service

import (
	"context"
	"fmt"
	"github.com/wyw14/cry-161/internal/interlock"
	"github.com/wyw14/cry-161/internal/model"
	"github.com/wyw14/cry-161/internal/run"
	"time"
)

func (s *Service) permitStarter(runtime *Runtime) *run.Starter {
	return &run.Starter{
		Registry: s.Runs,
		Vacuum:   s.Vacuum,
		Shutter:  runtime.Shutter.Feedback(),
		Source:   s.Source,
		Alarms:   runtime.InterlockMonitor.Alarms,
		Reviews:  runtime.PermitReviews,
		Audit:    runtime.InterlockAudit,
	}
}

// PermitDecision evaluates the interlocks for a run and returns the
// itemized decision without changing run state.
func (s *Service) PermitDecision(ctx context.Context, runtime *Runtime, runID model.ID) (interlock.PermitDecision, error) {
	return s.permitStarter(runtime).Decision(ctx, runID)
}

// RaiseInterlockAlarm records a manually raised alarm that blocks permits
// until cleared.
func (s *Service) RaiseInterlockAlarm(runtime *Runtime, name string) {
	runtime.InterlockMonitor.Alarm(name)
}

// ReviewPermit records an operator review of the currently violated
// interlock reasons and returns the re-evaluated decision. The review binds
// to the decision's fault generation: if a new fault generation has appeared
// by the time it is applied, the review is refused.
func (s *Service) ReviewPermit(ctx context.Context, runtime *Runtime, runID model.ID, operator string, reasons []interlock.Reason, deadline time.Time) (interlock.Override, interlock.PermitDecision, error) {
	starter := s.permitStarter(runtime)
	decision, err := starter.Decision(ctx, runID)
	if err != nil {
		return interlock.Override{}, interlock.PermitDecision{}, err
	}
	if decision.Allowed {
		return interlock.Override{}, decision, fmt.Errorf("permit is already allowed; no review needed")
	}
	override, err := runtime.PermitReviews.Review(runID, operator, reasons, decision.Generation, deadline, time.Now().UTC())
	if err != nil {
		return interlock.Override{}, decision, err
	}
	decision, err = starter.Decision(ctx, runID)
	if err != nil {
		return override, interlock.PermitDecision{}, err
	}
	return override, decision, nil
}
