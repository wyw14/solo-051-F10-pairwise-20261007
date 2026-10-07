package interlock

import (
	"context"
	"errors"
	"fmt"
	"github.com/wyw14/cry-161/internal/model"
	"github.com/wyw14/cry-161/internal/shutter"
	"github.com/wyw14/cry-161/internal/source"
	"github.com/wyw14/cry-161/internal/vacuum"
	"testing"
	"time"
)

func goodObservation(runID model.ID) Observation {
	vc := vacuum.NewController()
	if err := vc.Start(context.Background()); err != nil {
		panic(err)
	}
	return Observation{
		Run:     model.Run{ID: runID, State: model.Conditioning},
		Vacuum:  vc,
		Shutter: shutter.NewFeedback(),
		Source:  source.NewController(100),
	}
}

func TestEvaluateAllowsWhenAllConditionsSatisfied(t *testing.T) {
	book := NewOverrideBook()
	decision := Evaluate(goodObservation(model.NewID()), book, time.Now())
	if !decision.Allowed {
		t.Fatalf("expected allowed, got reasons %#v", decision.Reasons)
	}
	if len(decision.Reasons) != 0 {
		t.Fatalf("expected no reasons, got %#v", decision.Reasons)
	}
}

func TestEvaluateReportsEachViolatedReason(t *testing.T) {
	runID := model.NewID()

	// Fresh vacuum controller has not reached process pressure.
	obs := goodObservation(runID)
	obs.Vacuum = vacuum.NewController()
	obs.Run.State = model.Cooling
	obs.Shutter = shutter.NewFeedback()
	obs.Shutter.Update("open")
	obs.Alarms = []string{"operator-walkthrough"}

	// Energize the source so source_power is violated too.
	if err := obs.Source.Start(context.Background(), 7); err != nil {
		t.Fatalf("source start: %v", err)
	}
	defer obs.Source.Stop()

	decision := Evaluate(obs, NewOverrideBook(), time.Now())
	if decision.Allowed {
		t.Fatal("expected denial")
	}
	got := map[Reason]bool{}
	for _, reason := range decision.Reasons {
		if reason.Covered {
			t.Errorf("reason %s should not be covered", reason.Code)
		}
		if reason.Detail == "" {
			t.Errorf("reason %s has no detail", reason.Code)
		}
		got[reason.Code] = true
	}
	for _, want := range []Reason{ReasonVacuum, ReasonState, ReasonShutter, ReasonSourcePower, ReasonManualAlarm} {
		if !got[want] {
			t.Errorf("missing reason %s in %v", want, got)
		}
	}

	var denied *PermitDeniedError
	if !errors.As(fmt.Errorf("wrap: %w", &PermitDeniedError{Decision: decision}), &denied) {
		t.Fatal("PermitDeniedError should be discoverable with errors.As")
	}
	if !errors.Is(&PermitDeniedError{Decision: decision}, ErrPermitDenied) {
		t.Fatal("PermitDeniedError must match ErrPermitDenied")
	}
}

func TestReviewOverridesNamedReasonsUntilDeadline(t *testing.T) {
	runID := model.NewID()
	obs := goodObservation(runID)
	obs.Run.State = model.Cooling
	book := NewOverrideBook()
	now := time.Now().UTC()

	first := Evaluate(obs, book, now)
	if first.Allowed || first.Generation != 1 {
		t.Fatalf("expected denied generation 1, got allowed=%v gen=%d", first.Allowed, first.Generation)
	}

	deadline := now.Add(time.Hour)
	if _, err := book.Review(runID, "alice", []Reason{ReasonState}, first.Generation, deadline, now); err != nil {
		t.Fatalf("review: %v", err)
	}

	second := Evaluate(obs, book, now.Add(time.Minute))
	if !second.Allowed {
		t.Fatalf("expected review to grant permit, uncovered %v", second.UncoveredReasons())
	}
	if len(second.Reasons) != 1 || !second.Reasons[0].Covered || second.Reasons[0].Reviewer != "alice" {
		t.Fatalf("expected state covered by alice, got %#v", second.Reasons)
	}

	// Past the deadline the same review no longer covers the fault.
	expired := Evaluate(obs, book, deadline.Add(time.Second))
	if expired.Allowed {
		t.Fatal("expired review must not grant the permit")
	}
	status := expired.Reviews[0]
	if status.Valid || status.InvalidReason != "review deadline passed" {
		t.Fatalf("expected expired review, got %#v", status)
	}
}

func TestReviewDoesNotCoverNewFaultGeneration(t *testing.T) {
	runID := model.NewID()
	obs := goodObservation(runID)
	obs.Run.State = model.Cooling
	book := NewOverrideBook()
	now := time.Now().UTC()

	first := Evaluate(obs, book, now)
	if _, err := book.Review(runID, "alice", []Reason{ReasonState}, first.Generation, now.Add(time.Hour), now); err != nil {
		t.Fatalf("review: %v", err)
	}

	// A new fault (shutter opened while the state fault persists) advances
	// the generation and voids the earlier review.
	obs.Shutter.Update("open")
	second := Evaluate(obs, book, now)
	if second.Generation != 2 {
		t.Fatalf("expected generation 2, got %d", second.Generation)
	}
	for _, status := range second.Reviews {
		if status.Valid || status.InvalidReason != "new fault generation" {
			t.Fatalf("review should be invalidated by new generation, got %#v", status)
		}
	}
	if second.Allowed {
		t.Fatal("review must not cover a new fault generation")
	}

	// Re-recording the old generation is refused.
	_, err := book.Review(runID, "bob", []Reason{ReasonState, ReasonShutter}, first.Generation, now.Add(time.Hour), now)
	if !errors.Is(err, ErrNewFaultGeneration) {
		t.Fatalf("expected ErrNewFaultGeneration, got %v", err)
	}
}

func TestFaultSubstitutionAdvancesGeneration(t *testing.T) {
	runID := model.NewID()
	obs := goodObservation(runID)
	obs.Run.State = model.Cooling
	book := NewOverrideBook()
	now := time.Now().UTC()

	first := Evaluate(obs, book, now) // generation 1: state
	if _, err := book.Review(runID, "alice", []Reason{ReasonState}, first.Generation, now.Add(time.Hour), now); err != nil {
		t.Fatalf("review: %v", err)
	}

	// State corrects but the shutter fault appears: a different fault set.
	obs.Run.State = model.Conditioning
	obs.Shutter.Update("open")
	second := Evaluate(obs, book, now)
	if second.Generation != 2 {
		t.Fatalf("substituted fault should advance generation to 2, got %d", second.Generation)
	}
	if len(second.UncoveredReasons()) != 1 || second.UncoveredReasons()[0] != ReasonShutter {
		t.Fatalf("only shutter should block, got %v", second.UncoveredReasons())
	}
}

func TestReviewRejectsReasonsNotViolated(t *testing.T) {
	runID := model.NewID()
	obs := goodObservation(runID)
	obs.Run.State = model.Cooling
	book := NewOverrideBook()
	now := time.Now().UTC()
	decision := Evaluate(obs, book, now)

	_, err := book.Review(runID, "alice", []Reason{ReasonShutter}, decision.Generation, now.Add(time.Hour), now)
	if err == nil {
		t.Fatal("review of a non-violated reason must be rejected")
	}
}

func TestReviewValidation(t *testing.T) {
	book := NewOverrideBook()
	runID := model.NewID()
	book.Observe(runID, []Reason{ReasonState})
	now := time.Now().UTC()

	if _, err := book.Review(runID, "", []Reason{ReasonState}, 1, now.Add(time.Hour), now); err == nil {
		t.Error("operator required")
	}
	if _, err := book.Review(runID, "alice", nil, 1, now.Add(time.Hour), now); err == nil {
		t.Error("at least one reason required")
	}
	if _, err := book.Review(runID, "alice", []Reason{Reason("bogus")}, 1, now.Add(time.Hour), now); err == nil {
		t.Error("unknown reason must be rejected")
	}
	if _, err := book.Review(runID, "alice", []Reason{ReasonState}, 1, now, now); err == nil {
		t.Error("deadline must be in the future")
	}
	if _, err := book.Review(model.NewID(), "alice", []Reason{ReasonState}, 1, now.Add(time.Hour), now); err == nil {
		t.Error("run without a decision cannot be reviewed")
	}
}

func TestPartialReviewKeepsPermitDenied(t *testing.T) {
	runID := model.NewID()
	obs := goodObservation(runID)
	obs.Run.State = model.Cooling
	obs.Shutter.Update("open")
	book := NewOverrideBook()
	now := time.Now().UTC()

	decision := Evaluate(obs, book, now)
	if _, err := book.Review(runID, "alice", []Reason{ReasonState}, decision.Generation, now.Add(time.Hour), now); err != nil {
		t.Fatalf("review: %v", err)
	}
	decision = Evaluate(obs, book, now)
	if decision.Allowed || len(decision.UncoveredReasons()) != 1 || decision.UncoveredReasons()[0] != ReasonShutter {
		t.Fatalf("shutter should still block, got allowed=%v uncovered=%v", decision.Allowed, decision.UncoveredReasons())
	}

	if _, err := book.Review(runID, "bob", []Reason{ReasonShutter}, decision.Generation, now.Add(2*time.Hour), now); err != nil {
		t.Fatalf("second review: %v", err)
	}
	decision = Evaluate(obs, book, now)
	if !decision.Allowed {
		t.Fatalf("both reasons reviewed should allow, got %v", decision.UncoveredReasons())
	}
}

func TestMonitorClear(t *testing.T) {
	monitor := NewMonitor()
	monitor.Alarm("a")
	monitor.Alarm("b")
	monitor.Alarm("a")
	if !monitor.Clear("a") {
		t.Fatal("expected to clear an alarm")
	}
	got := monitor.Alarms()
	if len(got) != 1 || got[0] != "b" {
		t.Fatalf("unexpected alarms after clear: %v", got)
	}
	if monitor.Clear("missing") {
		t.Fatal("clearing an unknown alarm should report false")
	}
}
