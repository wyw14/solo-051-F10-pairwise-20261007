package api

import (
	"github.com/go-chi/chi/v5"
	"github.com/wyw14/cry-161/internal/service"
	"net/http"
)

func NewRouter(svc *service.Service) http.Handler {
	return newRouter(svc, service.NewRuntime())
}

func newRouter(svc *service.Service, runtime *service.Runtime) http.Handler {
	book := checkpointBook()
	r := chi.NewRouter()
	r.Get("/healthz", health)
	r.Get("/api/runs", runs(svc))
	r.Post("/api/runs", startRun(svc))
	r.Get("/api/chambers", chambers(svc))
	r.Get("/api/recipes", recipes(svc))
	r.Get("/api/thickness", thickness(svc))
	r.Get("/api/incidents", incidents(svc))
	r.Handle("/runs.html", http.FileServer(http.Dir("web")))
	r.Handle("/chambers.html", http.FileServer(http.Dir("web")))
	r.Handle("/layers.html", http.FileServer(http.Dir("web")))
	r.Handle("/incidents.html", http.FileServer(http.Dir("web")))
	r.Get("/api/runtime", runtimeStatus(svc, runtime))
	r.Get("/api/audit", auditStatus(svc, runtime))
	r.Post("/api/layers/run", runLayer(svc, runtime))
	r.Post("/api/runs/{runID}/recover", recoverRun(svc, runtime))
	r.Post("/api/runs/{runID}/release", releaseRun(svc, runtime))
	r.Get("/api/runs/{runID}/permit", permitDecision(svc, runtime))
	r.Post("/api/runs/{runID}/permit/review", permitReview(svc, runtime))
	r.Post("/api/interlock/alarms", raiseAlarm(svc, runtime))
	r.Delete("/api/interlock/alarms/{name}", clearAlarm(svc, runtime))
	r.Get("/api/runs/{runID}/timeline", timeline(svc, runtime))
	r.Post("/api/runs/{runID}/checkpoints", captureCheckpoint(svc, runtime, book))
	r.Get("/api/runs/{runID}/checkpoints", listCheckpoints(svc, book))
	r.Get("/api/snapshot", restoreSnapshot(svc, book))
	r.Get("/api/metrics", metricsStatus(svc, runtime))
	r.Put("/api/recipes/{recipeID}", recipeEdit(svc))
	return r
}
