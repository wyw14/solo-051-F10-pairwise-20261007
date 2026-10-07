package run

import (
	"context"
	"errors"
	"fmt"
	"github.com/wyw14/cry-161/internal/interlock"
	"github.com/wyw14/cry-161/internal/model"
	"github.com/wyw14/cry-161/internal/shutter"
	"github.com/wyw14/cry-161/internal/source"
	"github.com/wyw14/cry-161/internal/vacuum"
)

type Starter struct {
	Registry *Registry
	Vacuum   *vacuum.Controller
	Shutter  *shutter.Feedback
	Source   *source.Controller
	// Alarms returns currently active manual alarm names.
	Alarms func() []string
	// Reviews holds operator review overrides and fault generations.
	Reviews *interlock.OverrideBook
	// Audit, when set, records every evaluated permit decision.
	Audit *interlock.Audit
}

func (s *Starter) observation(runID model.ID) (interlock.Observation, error) {
	value, ok := s.Registry.Get(runID)
	if !ok {
		return interlock.Observation{}, fmt.Errorf("run not found")
	}
	var alarms []string
	if s.Alarms != nil {
		alarms = s.Alarms()
	}
	return interlock.Observation{Run: *value, Vacuum: s.Vacuum, Shutter: s.Shutter, Source: s.Source, Alarms: alarms}, nil
}

// Decision evaluates the interlock and returns the itemized decision,
// including per-condition reasons and any covering reviews.
func (s *Starter) Decision(ctx context.Context, runID model.ID) (interlock.PermitDecision, error) {
	select {
	case <-ctx.Done():
		return interlock.PermitDecision{}, ctx.Err()
	default:
	}
	obs, err := s.observation(runID)
	if err != nil {
		return interlock.PermitDecision{}, err
	}
	book := s.Reviews
	if book == nil {
		book = interlock.NewOverrideBook()
	}
	decision := interlock.Evaluate(obs, book, timeNow())
	if s.Audit != nil {
		reasons := make([]string, 0, len(decision.Reasons))
		for _, reason := range decision.Reasons {
			reasons = append(reasons, string(reason.Code))
		}
		recorded := s.Audit.Record(runID, decision.Allowed, reasons...)
		_ = recorded
	}
	return decision, nil
}

func (s *Starter) Permit(ctx context.Context, runID model.ID) error {
	decision, err := s.Decision(ctx, runID)
	if err != nil {
		return err
	}
	if !decision.Allowed {
		return fmt.Errorf("start permit: %w", &interlock.PermitDeniedError{Decision: decision})
	}
	return nil
}

func (s *Starter) StartCoating(ctx context.Context, runID model.ID) error {
	if err := s.Permit(ctx, runID); err != nil {
		return err
	}
	if !s.Registry.Transition(runID, model.Coating) {
		return fmt.Errorf("cannot enter coating")
	}
	return nil
}

// IsPermitDenied reports whether err carries an itemized permit denial.
func IsPermitDenied(err error) (*interlock.PermitDecision, bool) {
	var denied *interlock.PermitDeniedError
	if errors.As(err, &denied) {
		decision := denied.Decision
		return &decision, true
	}
	return nil, false
}
