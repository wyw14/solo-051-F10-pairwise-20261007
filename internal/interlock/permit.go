package interlock

import (
	"errors"
	"fmt"
	"github.com/wyw14/cry-161/internal/model"
	"github.com/wyw14/cry-161/internal/shutter"
	"github.com/wyw14/cry-161/internal/source"
	"github.com/wyw14/cry-161/internal/vacuum"
	"sort"
	"strings"
	"time"
)

var ErrPermitDenied = errors.New("permit denied")

// Reason identifies a single interlock condition that can block a permit.
type Reason string

const (
	ReasonVacuum      Reason = "vacuum"
	ReasonState       Reason = "state"
	ReasonShutter     Reason = "shutter"
	ReasonSourcePower Reason = "source_power"
	ReasonManualAlarm Reason = "manual_alarm"
)

// AllReasons lists every condition the interlock evaluates, in display order.
func AllReasons() []Reason {
	return []Reason{ReasonVacuum, ReasonState, ReasonShutter, ReasonSourcePower, ReasonManualAlarm}
}

func (r Reason) Valid() bool {
	for _, candidate := range AllReasons() {
		if candidate == r {
			return true
		}
	}
	return false
}

// Observation bundles the live signals the interlock decision is made from.
type Observation struct {
	Run     model.Run
	Vacuum  *vacuum.Controller
	Shutter *shutter.Feedback
	Source  *source.Controller
	Alarms  []string
}

// ReasonDetail reports one blocking condition and whether a review covers it.
type ReasonDetail struct {
	Code     Reason     `json:"code"`
	Detail   string     `json:"detail"`
	Covered  bool       `json:"covered"`
	Reviewer string     `json:"reviewer,omitempty"`
	ReviewID model.ID   `json:"review_id,omitempty"`
	Deadline *time.Time `json:"deadline,omitempty"`
}

// ReviewStatus exposes a recorded review and whether it is still usable.
type ReviewStatus struct {
	ID            model.ID  `json:"id"`
	Operator      string    `json:"operator"`
	Reasons       []Reason  `json:"reasons"`
	Generation    int       `json:"generation"`
	Deadline      time.Time `json:"deadline"`
	ReviewedAt    time.Time `json:"reviewed_at"`
	Valid         bool      `json:"valid"`
	InvalidReason string    `json:"invalid_reason,omitempty"`
}

// PermitDecision is the itemized result of an interlock evaluation.
type PermitDecision struct {
	RunID      model.ID       `json:"run_id"`
	Allowed    bool           `json:"allowed"`
	Generation int            `json:"generation"`
	Reasons    []ReasonDetail `json:"reasons"`
	Reviews    []ReviewStatus `json:"reviews,omitempty"`
	At         time.Time      `json:"at"`
}

// ActiveReasons returns the currently violated reason codes, sorted.
func (d PermitDecision) ActiveReasons() []Reason {
	out := make([]Reason, 0, len(d.Reasons))
	for _, reason := range d.Reasons {
		out = append(out, reason.Code)
	}
	return out
}

// UncoveredReasons returns violated reasons that no valid review covers.
func (d PermitDecision) UncoveredReasons() []Reason {
	out := make([]Reason, 0, len(d.Reasons))
	for _, reason := range d.Reasons {
		if !reason.Covered {
			out = append(out, reason.Code)
		}
	}
	return out
}

func (d PermitDecision) Error() string {
	if d.Allowed {
		return "permit allowed"
	}
	parts := make([]string, 0, len(d.Reasons))
	for _, reason := range d.Reasons {
		if reason.Covered {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s: %s", reason.Code, reason.Detail))
	}
	if len(parts) == 0 {
		parts = append(parts, "review expired for a new fault generation")
	}
	return fmt.Sprintf("%s: %s", ErrPermitDenied, strings.Join(parts, "; "))
}

// PermitDeniedError wraps the itemized decision when a permit is refused.
type PermitDeniedError struct {
	Decision PermitDecision
}

func (e *PermitDeniedError) Error() string        { return e.Decision.Error() }
func (e *PermitDeniedError) Is(target error) bool { return target == ErrPermitDenied }

// Evaluate runs every interlock check and applies reviews recorded in book.
// A review only covers faults from the same fault generation; any change in
// the violated condition set starts a new generation and voids prior reviews.
func Evaluate(obs Observation, book *OverrideBook, now time.Time) PermitDecision {
	faults := inspect(obs)
	codes := make([]Reason, 0, len(faults))
	for code := range faults {
		codes = append(codes, code)
	}
	sort.Slice(codes, func(i, j int) bool { return rank(codes[i]) < rank(codes[j]) })

	generation := book.Observe(obs.Run.ID, codes)
	reviews := book.ForRun(obs.Run.ID)

	decision := PermitDecision{RunID: obs.Run.ID, Generation: generation, At: now.UTC()}
	covering := map[Reason]Override{}
	statuses := make([]ReviewStatus, 0, len(reviews))
	for _, review := range reviews {
		status := ReviewStatus{ID: review.ID, Operator: review.Operator, Reasons: append([]Reason(nil), review.Reasons...), Generation: review.Generation, Deadline: review.Deadline, ReviewedAt: review.ReviewedAt}
		switch {
		case review.Generation != generation:
			status.InvalidReason = "new fault generation"
		case now.UTC().After(review.Deadline):
			status.InvalidReason = "review deadline passed"
		default:
			status.Valid = true
			for _, code := range review.Reasons {
				// First valid review wins the annotation; coverage is the union.
				if _, exists := covering[code]; !exists {
					covering[code] = review
				}
			}
		}
		statuses = append(statuses, status)
	}
	decision.Reviews = statuses

	allowed := true
	for _, code := range codes {
		detail := ReasonDetail{Code: code, Detail: faults[code]}
		if review, ok := covering[code]; ok {
			detail.Covered = true
			detail.Reviewer = review.Operator
			detail.ReviewID = review.ID
			deadline := review.Deadline
			detail.Deadline = &deadline
		} else {
			allowed = false
		}
		decision.Reasons = append(decision.Reasons, detail)
	}
	decision.Allowed = allowed
	return decision
}

func rank(code Reason) int {
	for index, candidate := range AllReasons() {
		if candidate == code {
			return index
		}
	}
	return len(AllReasons())
}

// inspect evaluates the five interlock conditions and returns violated codes
// with human-readable details.
func inspect(obs Observation) map[Reason]string {
	faults := map[Reason]string{}

	if obs.Vacuum == nil {
		faults[ReasonVacuum] = "vacuum controller unavailable"
	} else if err := vacuum.Check(obs.Vacuum); err != nil {
		faults[ReasonVacuum] = err.Error()
	}

	if obs.Run.State != model.Conditioning && obs.Run.State != model.Coating {
		faults[ReasonState] = fmt.Sprintf("run state %s cannot start coating", obs.Run.State)
	}

	if obs.Shutter == nil {
		faults[ReasonShutter] = "shutter feedback unavailable"
	} else if state, _ := obs.Shutter.State(); state != "closed" {
		faults[ReasonShutter] = fmt.Sprintf("shutter is %s, must be closed", state)
	}

	if obs.Source == nil {
		faults[ReasonSourcePower] = "source controller unavailable"
	} else if power := obs.Source.Power(); power != 0 {
		faults[ReasonSourcePower] = fmt.Sprintf("source already energized at %.6g", power)
	}

	if len(obs.Alarms) > 0 {
		faults[ReasonManualAlarm] = fmt.Sprintf("manual alarm active: %s", strings.Join(obs.Alarms, ", "))
	}

	return faults
}
