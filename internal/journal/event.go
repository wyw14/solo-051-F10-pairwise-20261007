package journal

import "github.com/wyw14/cry-161/internal/model"

func RunStarted(run model.Run) model.Event {
	return model.NewEvent("run.started", run.ID, map[string]any{"batch": run.Batch, "state": run.State})
}
func RunStateChanged(run model.Run, previous model.RunState) model.Event {
	return model.NewEvent("run.state", run.ID, map[string]any{"from": previous, "to": run.State})
}
func IncidentRaised(incident model.Incident) model.Event {
	return model.NewEvent("incident", incident.RunID, map[string]any{"severity": incident.Severity, "message": incident.Message})
}
func OverrideGranted(runID model.ID, operator string, generation int64) model.Event {
	return model.NewEvent("interlock.override", runID, map[string]any{"operator": operator, "generation": generation})
}
