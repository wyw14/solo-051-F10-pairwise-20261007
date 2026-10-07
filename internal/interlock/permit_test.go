package interlock

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/wyw14/cry-161/internal/model"
	"github.com/wyw14/cry-161/internal/shutter"
)

var testNow = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

func goodConditions() Conditions {
	return Conditions{State: model.Conditioning, Pressure: 0.00001, Stable: true, Shutter: shutter.StateClosed, SourcePower: 10}
}

func failedNames(checks []Check) []string {
	names := make([]string, 0)
	for _, check := range checks {
		if !check.Passed {
			names = append(names, check.Name+"/"+check.Detail)
		}
	}
	return names
}

func TestDecideAllowsHealthyConditions(t *testing.T) {
	decision := Decide(model.NewID(), goodConditions(), NewOverrides(), testNow)
	if !decision.Allowed {
		t.Fatalf("expected permit, got denial: %v", decision.Reasons)
	}
	if len(Failed(decision.Checks)) != 0 {
		t.Fatalf("expected no failed checks, got %v", failedNames(decision.Checks))
	}
}

func TestDecideReportsEveryFailingItem(t *testing.T) {
	cond := goodConditions()
	cond.Stable = false
	cond.State = model.Loading
	cond.Shutter = shutter.StateOpen
	cond.SourcePower = MaxSourcePower + 1
	cond.Alarms = []string{"door-open"}
	decision := Decide(model.NewID(), cond, NewOverrides(), testNow)
	if decision.Allowed {
		t.Fatal("expected denial")
	}
	if len(decision.Checks) != 5 {
		t.Fatalf("expected 5 checks, got %d", len(decision.Checks))
	}
	for _, name := range []string{CheckVacuum, CheckState, CheckShutter, CheckSource, CheckAlarms} {
		found := false
		for _, check := range decision.Checks {
			if check.Name == name && !check.Passed && check.Detail != "" {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected failed check %q with detail, checks: %+v", name, decision.Checks)
		}
	}
	if len(decision.Reasons) != 5 {
		t.Fatalf("expected 5 reasons, got %v", decision.Reasons)
	}
}

func TestPermitErrorCarriesDecision(t *testing.T) {
	cond := goodConditions()
	cond.Alarms = []string{"e-stop"}
	err := Permit(model.NewID(), cond, nil, testNow)
	if err == nil || !errors.Is(err, ErrPermitDenied) {
		t.Fatalf("expected ErrPermitDenied, got %v", err)
	}
	var permitErr *PermitError
	if !errors.As(err, &permitErr) {
		t.Fatalf("expected *PermitError, got %T", err)
	}
	if !strings.Contains(err.Error(), "e-stop") {
		t.Fatalf("expected alarm detail in error, got %q", err.Error())
	}
}

func TestOverrideCoversReviewedGenerationUntilExpiry(t *testing.T) {
	overrides := NewOverrides()
	runID := model.NewID()
	cond := goodConditions()
	cond.Alarms = []string{"e-stop"}

	denied := Decide(runID, cond, overrides, testNow)
	if denied.Allowed {
		t.Fatal("expected denial before override")
	}
	grant, err := overrides.Grant(runID, "operator-a", "reviewed", time.Hour, testNow)
	if err != nil {
		t.Fatalf("grant failed: %v", err)
	}
	if grant.Generation != denied.Generation {
		t.Fatalf("grant generation %d != decision generation %d", grant.Generation, denied.Generation)
	}

	released := Decide(runID, cond, overrides, testNow.Add(time.Minute))
	if !released.Allowed || released.Override == nil || released.Override.Operator != "operator-a" {
		t.Fatalf("expected override release, got %+v", released)
	}

	expired := Decide(runID, cond, overrides, testNow.Add(2*time.Hour))
	if expired.Allowed {
		t.Fatal("expected denial after override expiry")
	}
}

func TestOverrideDoesNotCoverNewFaultGeneration(t *testing.T) {
	overrides := NewOverrides()
	runID := model.NewID()
	cond := goodConditions()
	cond.Alarms = []string{"e-stop"}

	first := Decide(runID, cond, overrides, testNow)
	if _, err := overrides.Grant(runID, "operator-a", "", time.Hour, testNow); err != nil {
		t.Fatalf("grant failed: %v", err)
	}

	// A new fault appears: the fault set changes, so the generation must
	// advance and the previous grant must no longer release the permit.
	cond.Shutter = shutter.StateOpen
	second := Decide(runID, cond, overrides, testNow.Add(time.Minute))
	if second.Generation == first.Generation {
		t.Fatal("expected new fault generation when a new fault appears")
	}
	if second.Allowed {
		t.Fatal("override must not cover a new fault generation")
	}

	// The new fault clears; even though the fault set now matches the
	// reviewed one exactly, the generation advanced again, so the old
	// grant still must not apply.
	cond.Shutter = shutter.StateClosed
	third := Decide(runID, cond, overrides, testNow.Add(2*time.Minute))
	if third.Generation == second.Generation || third.Generation == first.Generation {
		t.Fatal("expected generation to advance when the fault set changes back")
	}
	if third.Allowed {
		t.Fatal("override must not cover a later generation with the same faults")
	}

	// A fresh review of the current generation releases it again.
	if _, err := overrides.Grant(runID, "operator-b", "", time.Hour, testNow.Add(3*time.Minute)); err != nil {
		t.Fatalf("re-grant failed: %v", err)
	}
	released := Decide(runID, cond, overrides, testNow.Add(4*time.Minute))
	if !released.Allowed || released.Override == nil || released.Override.Operator != "operator-b" {
		t.Fatalf("expected release by fresh review, got %+v", released)
	}
}

func TestGrantValidation(t *testing.T) {
	overrides := NewOverrides()
	runID := model.NewID()
	if _, err := overrides.Grant(runID, "", "", time.Hour, testNow); err == nil {
		t.Fatal("expected error for missing operator")
	}
	if _, err := overrides.Grant(runID, "operator-a", "", 0, testNow); err == nil {
		t.Fatal("expected error for non-positive ttl")
	}
	if _, err := overrides.Grant(runID, "operator-a", "", time.Hour, testNow); err == nil {
		t.Fatal("expected error when no fault generation is active")
	}
}

func TestChangedFaultConditionMintsNewGeneration(t *testing.T) {
	overrides := NewOverrides()
	runID := model.NewID()
	cond := goodConditions()
	cond.State = model.Pumping

	first := Decide(runID, cond, overrides, testNow)
	if _, err := overrides.Grant(runID, "operator-a", "", time.Hour, testNow); err != nil {
		t.Fatalf("grant failed: %v", err)
	}

	// The run moves on while the state check keeps failing: the reviewed
	// condition is gone, so the grant must stop applying.
	cond.State = model.Cooling
	moved := Decide(runID, cond, overrides, testNow.Add(time.Minute))
	if moved.Generation == first.Generation {
		t.Fatal("expected new generation when the failing state changes")
	}
	if moved.Allowed {
		t.Fatal("override must not cover a changed fault condition")
	}

	// A new manual alarm on top of the same state fault is a new fault.
	cond.Alarms = []string{"e-stop"}
	alarmed := Decide(runID, cond, overrides, testNow.Add(2*time.Minute))
	if alarmed.Generation == moved.Generation {
		t.Fatal("expected new generation when a new alarm appears")
	}
	if alarmed.Allowed {
		t.Fatal("override must not cover a generation with a new alarm")
	}
}

func TestReadingNoiseDoesNotMintNewGeneration(t *testing.T) {
	overrides := NewOverrides()
	runID := model.NewID()
	cond := goodConditions()
	cond.Stable = false
	cond.Pressure = 0.5

	first := Decide(runID, cond, overrides, testNow)
	if _, err := overrides.Grant(runID, "operator-a", "", time.Hour, testNow); err != nil {
		t.Fatalf("grant failed: %v", err)
	}

	// Same fault condition, different pressure reading: same generation,
	// the grant still applies.
	cond.Pressure = 0.7
	again := Decide(runID, cond, overrides, testNow.Add(time.Minute))
	if again.Generation != first.Generation {
		t.Fatal("pressure noise must not mint a new generation")
	}
	if !again.Allowed {
		t.Fatal("expected override to still cover the unchanged fault condition")
	}
}
