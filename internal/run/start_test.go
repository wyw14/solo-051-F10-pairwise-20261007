package run

import (
	"context"
	"errors"
	"github.com/wyw14/cry-161/internal/interlock"
	"github.com/wyw14/cry-161/internal/model"
	"github.com/wyw14/cry-161/internal/shutter"
	"github.com/wyw14/cry-161/internal/source"
	"github.com/wyw14/cry-161/internal/vacuum"
	"testing"
	"time"
)

func newTestStarter() (*Starter, *vacuum.Controller, *model.Run) {
	registry := NewRegistry()
	vc := vacuum.NewController()
	value := &model.Run{ID: model.NewID(), Batch: "b1", RecipeID: model.NewID(), State: model.Conditioning, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	registry.Add(value)
	starter := &Starter{
		Registry: registry,
		Vacuum:   vc,
		Shutter:  shutter.NewFeedback(),
		Source:   source.NewController(100),
		Alarms:   func() []string { return nil },
		Reviews:  interlock.NewOverrideBook(),
		Audit:    interlock.NewAudit(),
	}
	return starter, vc, value
}

func TestStarterDeniesWithItemizedReasons(t *testing.T) {
	starter, _, value := newTestStarter()
	// Vacuum never started: vacuum fails; run state is conditioning so it passes.
	err := starter.Permit(context.Background(), value.ID)
	if err == nil {
		t.Fatal("expected denial")
	}
	decision, ok := IsPermitDenied(err)
	if !ok {
		t.Fatalf("expected permit denied error, got %v", err)
	}
	if len(decision.UncoveredReasons()) != 1 || decision.UncoveredReasons()[0] != interlock.ReasonVacuum {
		t.Fatalf("expected only vacuum to block, got %v", decision.UncoveredReasons())
	}
	if !errors.Is(err, interlock.ErrPermitDenied) {
		t.Fatal("permit error must match ErrPermitDenied")
	}
}

func TestStarterReviewGrantsStartForSameGeneration(t *testing.T) {
	starter, _, value := newTestStarter()

	decision, err := starter.Decision(context.Background(), value.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := starter.Reviews.Review(value.ID, "alice", decision.UncoveredReasons(), decision.Generation, time.Now().Add(time.Hour), time.Now()); err != nil {
		t.Fatalf("review: %v", err)
	}
	if err := starter.StartCoating(context.Background(), value.ID); err != nil {
		t.Fatalf("coating should start after review: %v", err)
	}
	got, _ := starter.Registry.Get(value.ID)
	if got.State != model.Coating {
		t.Fatalf("expected coating, got %s", got.State)
	}
}

func TestStarterReviewVoidedByNewFaultGeneration(t *testing.T) {
	starter, _, value := newTestStarter()
	ctx := context.Background()

	decision, _ := starter.Decision(ctx, value.ID)
	if _, err := starter.Reviews.Review(value.ID, "alice", decision.UncoveredReasons(), decision.Generation, time.Now().Add(time.Hour), time.Now()); err != nil {
		t.Fatalf("review: %v", err)
	}

	// New fault appears (manual alarm): generation advances, review voided.
	starter.Alarms = func() []string { return []string{"walkthrough"} }
	if err := starter.Permit(ctx, value.ID); err == nil {
		t.Fatal("new fault generation must not be covered by the earlier review")
	}
	decision, _ = starter.Decision(ctx, value.ID)
	if decision.Generation != 2 {
		t.Fatalf("expected generation 2, got %d", decision.Generation)
	}
}
