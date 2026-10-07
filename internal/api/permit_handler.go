package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/wyw14/cry-161/internal/model"
	"github.com/wyw14/cry-161/internal/service"
)

func evaluatePermit(svc *service.Service, runtime *service.Runtime) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		runID, ok := model.ParseID(chi.URLParam(r, "runID"))
		if !ok {
			http.Error(w, "invalid run id", http.StatusBadRequest)
			return
		}
		decision, err := svc.EvaluatePermit(runtime, runID)
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

func grantOverride(svc *service.Service, runtime *service.Runtime) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		runID, ok := model.ParseID(chi.URLParam(r, "runID"))
		if !ok {
			http.Error(w, "invalid run id", http.StatusBadRequest)
			return
		}
		var input struct {
			Operator   string `json:"operator"`
			Note       string `json:"note"`
			TTLSeconds int64  `json:"ttl_seconds"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if input.Operator == "" {
			input.Operator = r.Header.Get("X-Operator")
		}
		grant, _, err := svc.GrantOverride(runtime, runID, input.Operator, input.Note, time.Duration(input.TTLSeconds)*time.Second)
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		writeJSON(w, grant)
	}
}

func listOverrides(svc *service.Service, runtime *service.Runtime) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_ = svc
		runID, ok := model.ParseID(chi.URLParam(r, "runID"))
		if !ok {
			http.Error(w, "invalid run id", http.StatusBadRequest)
			return
		}
		writeJSON(w, runtime.Overrides.List(runID))
	}
}
