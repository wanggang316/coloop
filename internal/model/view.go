package model

import "time"

// TaskView is the list/summary projection of a task for the API.
// GET /api/tasks returns []TaskView.
type TaskView struct {
	Task    Task      `json:"task"`
	LastRun *Run      `json:"last_run,omitempty"`
	NextRun time.Time `json:"next_run,omitempty"`
	Running bool      `json:"running"`
}

// TaskDetail is the per-task projection including recent run history.
// GET /api/tasks/{name} returns TaskDetail.
type TaskDetail struct {
	Task    Task      `json:"task"`
	Runs    []Run     `json:"runs"`
	NextRun time.Time `json:"next_run,omitempty"`
	Running bool      `json:"running"`
}

// TriggerResponse is returned by POST /api/tasks/{name}/run.
type TriggerResponse struct {
	RunID int64 `json:"run_id"`
}

// APIError is the JSON error envelope returned on non-2xx responses.
type APIError struct {
	Error string `json:"error"`
}
