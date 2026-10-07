package api

import (
	"bytes"
	"encoding/json"
	"github.com/wyw14/cry-161/internal/interlock"
	"github.com/wyw14/cry-161/internal/model"
	"github.com/wyw14/cry-161/internal/service"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPermitDecisionAndReviewFlow(t *testing.T) {
	svc := service.New()
	runtime := service.NewRuntime()
	handler := newRouter(svc, runtime)
	server := httptest.NewServer(handler)
	defer server.Close()

	recipes := svc.RecipesList()
	runBody, _ := json.Marshal(map[string]string{"batch": "b1", "recipe_id": string(recipes[0].ID)})
	resp, err := http.Post(server.URL+"/api/runs", "application/json", bytes.NewReader(runBody))
	if err != nil {
		t.Fatal(err)
	}
	var value model.Run
	decode(t, resp, &value)
	runURL := server.URL + "/api/runs/" + string(value.ID)

	// Freshly started run is pumping: the state interlock denies coating.
	resp, err = http.Get(runURL + "/permit")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409, got %d", resp.StatusCode)
	}
	var decision interlock.PermitDecision
	decode(t, resp, &decision)
	if decision.Allowed || len(decision.Reasons) != 1 || decision.Reasons[0].Code != interlock.ReasonState {
		t.Fatalf("expected single state reason, got %#v", decision)
	}
	generation := decision.Generation

	// Review the state fault with operator and deadline; permit is granted.
	reviewBody, _ := json.Marshal(map[string]any{"operator": "alice", "reasons": []string{"state"}, "valid_minutes": 30})
	resp, err = http.Post(runURL+"/permit/review", "application/json", bytes.NewReader(reviewBody))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("review: %d %s", resp.StatusCode, body)
	}
	var reviewed struct {
		Decision interlock.PermitDecision `json:"decision"`
	}
	decode(t, resp, &reviewed)
	if !reviewed.Decision.Allowed || reviewed.Decision.Reasons[0].Reviewer != "alice" {
		t.Fatalf("expected covered permit, got %#v", reviewed.Decision)
	}

	// A new fault (manual alarm) starts a new generation and voids the review.
	alarmBody, _ := json.Marshal(map[string]string{"name": "walkthrough"})
	resp, err = http.Post(server.URL+"/api/interlock/alarms", "application/json", bytes.NewReader(alarmBody))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	resp, err = http.Get(runURL + "/permit")
	if err != nil {
		t.Fatal(err)
	}
	decode(t, resp, &decision)
	if decision.Generation != generation+1 {
		t.Fatalf("expected generation %d, got %d", generation+1, decision.Generation)
	}
	for _, status := range decision.Reviews {
		if status.Valid {
			t.Fatal("old review must be invalid for the new generation")
		}
	}

	// Replaying a review against the stale generation is rejected.
	_, err = runtime.PermitReviews.Review(value.ID, "bob",
		[]interlock.Reason{interlock.ReasonState, interlock.ReasonManualAlarm},
		generation, time.Now().Add(time.Hour), time.Now())
	if err == nil {
		t.Fatal("stale-generation review must be rejected")
	}

	// Reviewing the current generation covers both faults and allows start.
	currentBody, _ := json.Marshal(map[string]any{
		"operator":      "bob",
		"reasons":       []string{"state", "manual_alarm"},
		"valid_minutes": 15,
	})
	resp, err = http.Post(runURL+"/permit/review", "application/json", bytes.NewReader(currentBody))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("current review: %d %s", resp.StatusCode, body)
	}
	decode(t, resp, &reviewed)
	if !reviewed.Decision.Allowed {
		t.Fatalf("both faults reviewed should allow, got %#v", reviewed.Decision)
	}
}

func TestPermitReviewRejectsUnknownReason(t *testing.T) {
	svc := service.New()
	runtime := service.NewRuntime()
	handler := newRouter(svc, runtime)
	server := httptest.NewServer(handler)
	defer server.Close()

	recipes := svc.RecipesList()
	runBody, _ := json.Marshal(map[string]string{"batch": "b1", "recipe_id": string(recipes[0].ID)})
	resp, _ := http.Post(server.URL+"/api/runs", "application/json", bytes.NewReader(runBody))
	var value model.Run
	decode(t, resp, &value)

	body, _ := json.Marshal(map[string]any{"operator": "alice", "reasons": []string{"flux-capacitor"}, "valid_minutes": 30})
	resp, _ = http.Post(server.URL+"/api/runs/"+string(value.ID)+"/permit/review", "application/json", bytes.NewReader(body))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown reason, got %d", resp.StatusCode)
	}
}

func decode(t *testing.T, resp *http.Response, target any) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
		t.Fatalf("decode: %v", err)
	}
}
