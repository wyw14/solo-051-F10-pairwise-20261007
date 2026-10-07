package run

import (
	"context"
	"fmt"
	"time"

	"github.com/wyw14/cry-161/internal/interlock"
	"github.com/wyw14/cry-161/internal/model"
	"github.com/wyw14/cry-161/internal/shutter"
	"github.com/wyw14/cry-161/internal/source"
	"github.com/wyw14/cry-161/internal/vacuum"
)

type Starter struct {
	Registry  *Registry
	Vacuum    *vacuum.Controller
	Shutter   *shutter.Controller
	Source    *source.Controller
	Alarms    *interlock.Monitor
	Overrides *interlock.Overrides
}

// Conditions snapshots the interlock inputs for a run. Missing
// dependencies fail safe: vacuum reads as unstable, shutter as unknown.
func (s *Starter) Conditions(run model.Run) interlock.Conditions {
	cond := interlock.Conditions{State: run.State, Shutter: "unknown"}
	if s.Vacuum != nil {
		cond.Pressure = s.Vacuum.Pressure()
		cond.Stable = s.Vacuum.Stable()
	}
	if s.Shutter != nil {
		cond.Shutter = s.Shutter.State()
	}
	if s.Source != nil {
		cond.SourcePower = s.Source.Power()
	}
	if s.Alarms != nil {
		cond.Alarms = s.Alarms.Alarms()
	}
	return cond
}

func (s *Starter) Permit(ctx context.Context, runID model.ID) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	value, ok := s.Registry.Get(runID)
	if !ok {
		return fmt.Errorf("run not found")
	}
	if err := interlock.Permit(runID, s.Conditions(*value), s.Overrides, time.Now().UTC()); err != nil {
		return fmt.Errorf("start permit: %w", err)
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
