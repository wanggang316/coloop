// Package api exposes the-loop's HTTP surface: a small JSON API over the
// engine plus the embedded single-page admin UI. It owns transport concerns
// only and delegates all behaviour to *engine.Engine.
package api

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/wanggang316/the-loop/internal/engine"
	"github.com/wanggang316/the-loop/internal/model"
)

// New builds the HTTP handler. ui serves the embedded SPA; reload is the hook
// invoked by POST /api/reload to re-read configuration and reschedule cron.
func New(e *engine.Engine, ui fs.FS, reload func() error) http.Handler {
	s := &server{eng: e, ui: ui, reload: reload}
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /api/tasks", s.listTasks)
	mux.HandleFunc("POST /api/tasks", s.createTask)
	mux.HandleFunc("GET /api/tasks/{name}", s.getTask)
	mux.HandleFunc("PUT /api/tasks/{name}", s.updateTask)
	mux.HandleFunc("DELETE /api/tasks/{name}", s.deleteTask)
	mux.HandleFunc("POST /api/tasks/{name}/run", s.runTask)
	mux.HandleFunc("GET /api/tasks/{name}/runs", s.listRuns)
	mux.HandleFunc("GET /api/tasks/{name}/runs/{id}", s.getRun)
	mux.HandleFunc("GET /api/tasks/{name}/runs/{id}/log", s.runLog)
	mux.HandleFunc("GET /api/tasks/{name}/runs/{id}/stream", s.runStream)
	mux.HandleFunc("GET /api/runner-presets", s.listRunnerPresets)
	mux.HandleFunc("GET /api/runners", s.listRunners)
	mux.HandleFunc("POST /api/runners", s.createRunner)
	mux.HandleFunc("PUT /api/runners/{name}", s.updateRunner)
	mux.HandleFunc("DELETE /api/runners/{name}", s.deleteRunner)
	mux.HandleFunc("GET /api/notifiers", s.listNotifiers)
	mux.HandleFunc("POST /api/notifiers", s.createNotifier)
	mux.HandleFunc("PUT /api/notifiers/{name}", s.updateNotifier)
	mux.HandleFunc("DELETE /api/notifiers/{name}", s.deleteNotifier)
	mux.HandleFunc("POST /api/reload", s.reloadConfig)

	// Catch-all: serve the SPA (with route fallback to index.html).
	mux.HandleFunc("GET /", s.static)

	return mux
}

// server holds the dependencies shared by every handler.
type server struct {
	eng    *engine.Engine
	ui     fs.FS
	reload func() error
}

func (s *server) healthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "ok")
}

func (s *server) listTasks(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.eng.ListTasks())
}

func (s *server) getTask(w http.ResponseWriter, r *http.Request) {
	detail, err := s.eng.GetTask(r.PathValue("name"))
	if err != nil {
		writeEngineErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (s *server) runTask(w http.ResponseWriter, r *http.Request) {
	id, err := s.eng.Trigger(r.PathValue("name"), model.TriggerManual)
	if err != nil {
		writeEngineErr(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, model.TriggerResponse{RunID: id})
}

func (s *server) createTask(w http.ResponseWriter, r *http.Request) {
	var t model.Task
	if !decodeJSON(w, r, &t) {
		return
	}
	created, err := s.eng.CreateTask(t)
	if err != nil {
		writeTaskErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (s *server) updateTask(w http.ResponseWriter, r *http.Request) {
	var t model.Task
	if !decodeJSON(w, r, &t) {
		return
	}
	updated, err := s.eng.UpdateTask(r.PathValue("name"), t)
	if err != nil {
		writeTaskErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *server) deleteTask(w http.ResponseWriter, r *http.Request) {
	if err := s.eng.DeleteTask(r.PathValue("name")); err != nil {
		writeTaskErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// listRunnerPresets serves the built-in runner templates the UI offers as
// starting points. They are static, so this needs nothing from the engine.
func (s *server) listRunnerPresets(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, model.RunnerPresets)
}

func (s *server) listRunners(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.eng.ListRunners())
}

func (s *server) createRunner(w http.ResponseWriter, r *http.Request) {
	var rn model.AgentRunner
	if !decodeJSON(w, r, &rn) {
		return
	}
	created, err := s.eng.CreateRunner(rn)
	if err != nil {
		writeTaskErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (s *server) updateRunner(w http.ResponseWriter, r *http.Request) {
	var rn model.AgentRunner
	if !decodeJSON(w, r, &rn) {
		return
	}
	updated, err := s.eng.UpdateRunner(r.PathValue("name"), rn)
	if err != nil {
		writeTaskErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *server) deleteRunner(w http.ResponseWriter, r *http.Request) {
	if err := s.eng.DeleteRunner(r.PathValue("name")); err != nil {
		writeTaskErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) listNotifiers(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.eng.ListNotifiers())
}

func (s *server) createNotifier(w http.ResponseWriter, r *http.Request) {
	var c model.NotifierDef
	if !decodeJSON(w, r, &c) {
		return
	}
	created, err := s.eng.CreateNotifier(c)
	if err != nil {
		writeTaskErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (s *server) updateNotifier(w http.ResponseWriter, r *http.Request) {
	var c model.NotifierDef
	if !decodeJSON(w, r, &c) {
		return
	}
	updated, err := s.eng.UpdateNotifier(r.PathValue("name"), c)
	if err != nil {
		writeTaskErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *server) deleteNotifier(w http.ResponseWriter, r *http.Request) {
	if err := s.eng.DeleteNotifier(r.PathValue("name")); err != nil {
		writeTaskErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) listRuns(w http.ResponseWriter, r *http.Request) {
	limit := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			writeErr(w, http.StatusBadRequest, "invalid limit")
			return
		}
		limit = n
	}
	runs, err := s.eng.ListRuns(r.PathValue("name"), limit)
	if err != nil {
		writeEngineErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, runs)
}

func (s *server) getRun(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	run, err := s.eng.GetRun(r.PathValue("name"), id)
	if err != nil {
		writeEngineErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (s *server) runLog(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	rc, err := s.eng.OpenLog(r.PathValue("name"), id)
	if err != nil {
		writeEngineErr(w, err)
		return
	}
	defer rc.Close()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, rc)
}

func (s *server) runStream(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	ctx := r.Context()
	events, err := s.eng.StreamLog(ctx, r.PathValue("name"), id)
	if err != nil {
		writeEngineErr(w, err)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	for {
		select {
		case <-ctx.Done():
			return
		case ev, more := <-events:
			if !more {
				return
			}
			if ev.Done {
				_, _ = io.WriteString(w, "event: done\ndata: "+string(ev.Status)+"\n\n")
				flusher.Flush()
				return
			}
			_, _ = io.WriteString(w, "data: "+ev.Line+"\n\n")
			flusher.Flush()
		}
	}
}

func (s *server) reloadConfig(w http.ResponseWriter, _ *http.Request) {
	if s.reload != nil {
		if err := s.reload(); err != nil {
			log.Printf("api: reload failed: %v", err)
			writeErr(w, http.StatusInternalServerError, "internal error")
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// static serves the embedded SPA. Unknown non-API paths fall back to
// index.html so client-side routing works on deep links and reloads.
func (s *server) static(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if name == "" {
		name = "index.html"
	}

	if s.serveFile(w, r, name) {
		return
	}
	// SPA fallback for non-API routes only.
	if !strings.HasPrefix(r.URL.Path, "/api") && !strings.HasPrefix(r.URL.Path, "/healthz") {
		s.serveFile(w, r, "index.html")
		return
	}
	writeErr(w, http.StatusNotFound, "not found")
}

// serveFile opens name from the UI FS and serves it, reporting whether it
// existed. http.ServeContent sets the Content-Type and handles range/caching.
func (s *server) serveFile(w http.ResponseWriter, r *http.Request, name string) bool {
	f, err := s.ui.Open(name)
	if err != nil {
		return false
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil || info.IsDir() {
		return false
	}
	// The SPA assets are embedded in the binary, so they only change on a
	// binary upgrade — but embed.FS reports a zero ModTime, leaving the response
	// with no validator. Without this, browsers cache the assets heuristically
	// and keep serving a stale app.js after an upgrade. no-cache forces a
	// revalidate (effectively a refetch here) on every load, which is cheap for
	// a handful of small local files.
	w.Header().Set("Cache-Control", "no-cache")

	rs, ok := f.(io.ReadSeeker)
	if !ok {
		// Embedded FS files implement io.ReadSeeker; this guards exotic FS impls.
		w.Header().Set("Content-Type", contentType(name))
		_, _ = io.Copy(w, f)
		return true
	}
	http.ServeContent(w, r, name, info.ModTime(), rs)
	return true
}

// parseID extracts the {id} path value, writing a 404 on a malformed id.
func parseID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return 0, false
	}
	return id, true
}

// writeJSON serializes v as JSON with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeErr writes a model.APIError envelope with the given status code.
func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, model.APIError{Error: msg})
}

// writeEngineErr maps an engine error to the right status: ErrNotFound -> 404,
// anything else -> 500. Internal errors are logged server-side and reported to
// the client as a generic message to avoid leaking paths/secrets.
func writeEngineErr(w http.ResponseWriter, err error) {
	if errors.Is(err, engine.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	log.Printf("api: internal error: %v", err)
	writeErr(w, http.StatusInternalServerError, "internal error")
}

// decodeJSON decodes the request body into v, writing a 400 and returning false
// on failure so the caller can simply `return`.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return false
	}
	return true
}

// writeTaskErr maps task-editing errors to HTTP status codes: not found -> 404,
// duplicate -> 409, validation -> 400, no config path -> 403, else 500.
func writeTaskErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, engine.ErrNotFound):
		writeErr(w, http.StatusNotFound, "not found")
	case errors.Is(err, engine.ErrDuplicate):
		writeErr(w, http.StatusConflict, err.Error())
	case errors.Is(err, engine.ErrInvalid):
		writeErr(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, engine.ErrReadOnly):
		writeErr(w, http.StatusForbidden, err.Error())
	default:
		log.Printf("api: task edit error: %v", err)
		writeErr(w, http.StatusInternalServerError, "internal error")
	}
}

// contentType is a minimal fallback MIME map for the SPA's known asset types,
// used only when http.ServeContent cannot sniff the type.
func contentType(name string) string {
	switch {
	case strings.HasSuffix(name, ".html"):
		return "text/html; charset=utf-8"
	case strings.HasSuffix(name, ".js"):
		return "text/javascript; charset=utf-8"
	case strings.HasSuffix(name, ".css"):
		return "text/css; charset=utf-8"
	default:
		return "application/octet-stream"
	}
}
