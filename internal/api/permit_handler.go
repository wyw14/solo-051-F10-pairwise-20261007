package api

import (
	"encoding/json"
	"github.com/wyw14/cry-161/internal/interlock"
	"github.com/wyw14/cry-161/internal/model"
	"github.com/wyw14/cry-161/internal/service"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

// permitDecision returns the itemized interlock decision for a run:
// vacuum, state, shutter, source power and manual alarm reasons, plus the
// reviews currently attached to the run.
func permitDecision(svc *service.Service, runtime *service.Runtime) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		runID, ok := model.ParseID(chi.URLParam(r, "runID"))
		if !ok {
			http.Error(w, "invalid run id", http.StatusBadRequest)
			return
		}
		decision, err := svc.PermitDecision(r.Context(), runtime, runID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		if !decision.Allowed {
			w.WriteHeader(http.StatusConflict)
		}
		writeJSON(w, decision)
	}
}

type permitReviewRequest struct {
	Operator     string   `json:"operator"`
	Reasons      []string `json:"reasons"`
	Deadline     string   `json:"deadline"`
	ValidMinutes int      `json:"valid_minutes"`
}

// permitReview records an operator review of the currently violated
// interlock reasons. The review carries the operator and a deadline and is
// bound to the decision's fault generation; a new fault generation makes the
// review invalid.
func permitReview(svc *service.Service, runtime *service.Runtime) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		runID, ok := model.ParseID(chi.URLParam(r, "runID"))
		if !ok {
			http.Error(w, "invalid run id", http.StatusBadRequest)
			return
		}
		var input permitReviewRequest
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		operator := input.Operator
		if operator == "" {
			operator = r.Header.Get("X-Operator")
		}
		if operator == "" {
			operator = "local-operator"
		}
		if len(input.Reasons) == 0 {
			http.Error(w, "reasons are required", http.StatusBadRequest)
			return
		}
		reasons := make([]interlock.Reason, 0, len(input.Reasons))
		for _, value := range input.Reasons {
			reason := interlock.Reason(value)
			if !reason.Valid() {
				http.Error(w, "unknown interlock reason: "+value, http.StatusBadRequest)
				return
			}
			reasons = append(reasons, reason)
		}

		var deadline time.Time
		if input.Deadline != "" {
			parsed, err := time.Parse(time.RFC3339, input.Deadline)
			if err != nil {
				http.Error(w, "invalid deadline, use RFC3339", http.StatusBadRequest)
				return
			}
			deadline = parsed
		} else if input.ValidMinutes > 0 {
			deadline = time.Now().UTC().Add(time.Duration(input.ValidMinutes) * time.Minute)
		} else {
			http.Error(w, "deadline or valid_minutes is required", http.StatusBadRequest)
			return
		}

		override, decision, err := svc.ReviewPermit(r.Context(), runtime, runID, operator, reasons, deadline)
		if err != nil {
			writeErrorJSON(w, http.StatusConflict, err, decision)
			return
		}
		writeJSON(w, map[string]any{"review": override, "decision": decision})
	}
}

type alarmRequest struct {
	Name string `json:"name"`
}

// raiseAlarm records a manual operator alarm that blocks permits.
func raiseAlarm(svc *service.Service, runtime *service.Runtime) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_ = svc
		var input alarmRequest
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input.Name == "" {
			http.Error(w, "alarm name is required", http.StatusBadRequest)
			return
		}
		runtime.InterlockMonitor.Alarm(input.Name)
		writeJSON(w, map[string]any{"alarms": runtime.InterlockMonitor.Alarms()})
	}
}

// clearAlarm removes a manually raised alarm.
func clearAlarm(svc *service.Service, runtime *service.Runtime) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_ = svc
		name := chi.URLParam(r, "name")
		if !runtime.InterlockMonitor.Clear(name) {
			http.Error(w, "alarm not active", http.StatusNotFound)
			return
		}
		writeJSON(w, map[string]any{"alarms": runtime.InterlockMonitor.Alarms()})
	}
}

func writeErrorJSON(w http.ResponseWriter, status int, err error, decision any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error(), "decision": decision})
}
