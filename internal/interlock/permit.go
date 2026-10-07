package interlock

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/wyw14/cry-161/internal/model"
	"github.com/wyw14/cry-161/internal/shutter"
	"github.com/wyw14/cry-161/internal/vacuum"
)

var ErrPermitDenied = errors.New("permit denied")

// Check names, in evaluation order.
const (
	CheckVacuum  = "vacuum"
	CheckState   = "state"
	CheckShutter = "shutter"
	CheckSource  = "source_power"
	CheckAlarms  = "alarms"
)

// MaxSourcePower is the safe source power ceiling for a permit.
const MaxSourcePower = 100.0

// Conditions carries every input a permit decision needs.
type Conditions struct {
	State       model.RunState `json:"state"`
	Pressure    float64        `json:"pressure"`
	Stable      bool           `json:"stable"`
	Shutter     string         `json:"shutter"`
	SourcePower float64        `json:"source_power"`
	Alarms      []string       `json:"alarms"`
}

// Check is the outcome of one interlock item. Fault identifies the fault
// condition when the check fails; it is empty when the check passes.
type Check struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail"`
	Fault  string `json:"fault,omitempty"`
}

// Evaluate runs every interlock check and returns them in a fixed order.
func Evaluate(cond Conditions) []Check {
	checks := make([]Check, 0, 5)

	vacuumOK := cond.Stable && cond.Pressure < vacuum.StableCeiling
	vacuumDetail := fmt.Sprintf("pressure %.6g stable", cond.Pressure)
	vacuumFault := ""
	if !vacuumOK {
		vacuumDetail = fmt.Sprintf("pressure %.6g stable=%t, need stable below %.6g", cond.Pressure, cond.Stable, vacuum.StableCeiling)
		vacuumFault = fmt.Sprintf("vacuum(stable=%t,below_ceiling=%t)", cond.Stable, cond.Pressure < vacuum.StableCeiling)
	}
	checks = append(checks, Check{Name: CheckVacuum, Passed: vacuumOK, Detail: vacuumDetail, Fault: vacuumFault})

	stateOK := cond.State == model.Conditioning || cond.State == model.Coating
	stateDetail := fmt.Sprintf("state %s permits coating", cond.State)
	stateFault := ""
	if !stateOK {
		stateDetail = fmt.Sprintf("state %s, need %s or %s", cond.State, model.Conditioning, model.Coating)
		stateFault = fmt.Sprintf("state(%s)", cond.State)
	}
	checks = append(checks, Check{Name: CheckState, Passed: stateOK, Detail: stateDetail, Fault: stateFault})

	shutterOK := cond.Shutter == shutter.StateClosed
	shutterDetail := "shutter closed"
	shutterFault := ""
	if !shutterOK {
		shutterDetail = fmt.Sprintf("shutter %s, need %s", cond.Shutter, shutter.StateClosed)
		shutterFault = fmt.Sprintf("shutter(%s)", cond.Shutter)
	}
	checks = append(checks, Check{Name: CheckShutter, Passed: shutterOK, Detail: shutterDetail, Fault: shutterFault})

	sourceOK := cond.SourcePower >= 0 && cond.SourcePower <= MaxSourcePower
	sourceDetail := fmt.Sprintf("source power %.6g within limit %.6g", cond.SourcePower, MaxSourcePower)
	sourceFault := ""
	if !sourceOK {
		sourceDetail = fmt.Sprintf("source power %.6g outside [0, %.6g]", cond.SourcePower, MaxSourcePower)
		bucket := "negative"
		if cond.SourcePower > MaxSourcePower {
			bucket = "over_limit"
		}
		sourceFault = fmt.Sprintf("source_power(%s)", bucket)
	}
	checks = append(checks, Check{Name: CheckSource, Passed: sourceOK, Detail: sourceDetail, Fault: sourceFault})

	alarmsOK := len(cond.Alarms) == 0
	alarmsDetail := "no manual alarms"
	alarmsFault := ""
	if !alarmsOK {
		alarmsDetail = fmt.Sprintf("manual alarms active: %s", strings.Join(cond.Alarms, ", "))
		alarmsFault = fmt.Sprintf("alarms(%s)", strings.Join(sortedCopy(cond.Alarms), ","))
	}
	checks = append(checks, Check{Name: CheckAlarms, Passed: alarmsOK, Detail: alarmsDetail, Fault: alarmsFault})

	return checks
}

// Failed returns the checks that did not pass.
func Failed(checks []Check) []Check {
	failed := make([]Check, 0, len(checks))
	for _, check := range checks {
		if !check.Passed {
			failed = append(failed, check)
		}
	}
	return failed
}

// FaultKey fingerprints the fault condition: the same condition always
// yields the same key, while any new, cleared, or materially changed fault
// (different run state, different shutter position, a new alarm) yields a
// different one. Continuous readings like pressure are excluded so noise
// does not mint new generations.
func FaultKey(checks []Check) string {
	faults := make([]string, 0, len(checks))
	for _, check := range checks {
		if check.Fault != "" {
			faults = append(faults, check.Fault)
		}
	}
	sort.Strings(faults)
	return strings.Join(faults, "|")
}

func sortedCopy(values []string) []string {
	copied := append([]string(nil), values...)
	sort.Strings(copied)
	return copied
}

// PermitError is a denial carrying the full decision for inspection.
type PermitError struct {
	Decision Decision
}

func (e *PermitError) Error() string {
	return fmt.Sprintf("%s: %s", ErrPermitDenied, strings.Join(e.Decision.Reasons, "; "))
}

func (e *PermitError) Unwrap() error { return ErrPermitDenied }

// Decide evaluates conditions, tracks the run's fault generation, and
// applies a review override when one covers the current generation.
func Decide(runID model.ID, cond Conditions, overrides *Overrides, now time.Time) Decision {
	checks := Evaluate(cond)
	decision := Decision{ID: model.NewID(), RunID: runID, Checks: checks, Reasons: make([]string, 0), At: now}
	failed := Failed(checks)
	for _, check := range failed {
		decision.Reasons = append(decision.Reasons, check.Detail)
	}
	if overrides != nil {
		decision.Generation = overrides.Track(runID, FaultKey(checks))
	}
	if len(failed) == 0 {
		decision.Allowed = true
		return decision
	}
	if overrides == nil {
		return decision
	}
	grant, ok := overrides.Latest(runID)
	if !ok {
		return decision
	}
	switch {
	case grant.Generation != decision.Generation:
		decision.Reasons = append(decision.Reasons, fmt.Sprintf("override by %s covers fault generation %d, current generation is %d", grant.Operator, grant.Generation, decision.Generation))
	case grant.Expired(now):
		decision.Reasons = append(decision.Reasons, fmt.Sprintf("override by %s expired at %s", grant.Operator, grant.ExpiresAt.Format(time.RFC3339)))
	default:
		decision.Allowed = true
		decision.Override = &grant
		decision.Reasons = append(decision.Reasons, fmt.Sprintf("override release by %s until %s", grant.Operator, grant.ExpiresAt.Format(time.RFC3339)))
	}
	return decision
}

// Permit evaluates conditions and returns nil when the run may proceed;
// a denial is returned as *PermitError with the per-item reasons.
func Permit(runID model.ID, cond Conditions, overrides *Overrides, now time.Time) error {
	decision := Decide(runID, cond, overrides, now)
	if decision.Allowed {
		return nil
	}
	return &PermitError{Decision: decision}
}
