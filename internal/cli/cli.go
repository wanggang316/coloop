// Package cli implements the-loop's command-line client. It is a thin HTTP
// client over the daemon's JSON/SSE API; it never imports the engine or api
// packages and talks to a running daemon purely over HTTP. The only offline
// command is "validate", which loads and checks a config file directly.
package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/wanggang316/the-loop/internal/config"
	"github.com/wanggang316/the-loop/internal/model"
)

const defaultAddr = "http://127.0.0.1:8765"

// Dispatch runs a single CLI subcommand and returns a process exit code.
// cfgPath is the config file used both for the offline "validate" command and
// as the last-resort source of the daemon's listen address.
func Dispatch(cmd string, args []string, cfgPath string) int {
	switch cmd {
	case "ls":
		return runLs(args, cfgPath)
	case "run":
		return runRun(args, cfgPath)
	case "logs":
		return runLogs(args, cfgPath)
	case "validate":
		return runValidate(args, cfgPath)
	case "reload":
		return runReload(args, cfgPath)
	case "", "help", "-h", "--help":
		usage(os.Stdout)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", cmd)
		usage(os.Stderr)
		return 2
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `loop — control the-loop daemon

usage:
  loop ls                       list tasks
  loop run <task>               trigger a task, print the new run id
  loop logs [-f] <task> [id]    show a run's log (latest run if id omitted; -f to follow)
  loop validate [path]          validate a config file offline
  loop reload                   ask the daemon to reload its config

flags:
  --addr <url>   daemon base URL (overrides LOOP_ADDR and config listen)
`)
}

// ---------------------------------------------------------------------------
// base URL resolution
// ---------------------------------------------------------------------------

// resolveAddr picks the daemon base URL: explicit --addr flag wins, then the
// LOOP_ADDR env var, then the listen address from the config file, and finally
// the built-in default. A bare host:port (e.g. ":8765") is normalised to an
// http:// URL.
func resolveAddr(flagAddr, cfgPath string) string {
	if flagAddr != "" {
		return normalizeAddr(flagAddr)
	}
	if env := os.Getenv("LOOP_ADDR"); env != "" {
		return normalizeAddr(env)
	}
	if cfgPath != "" {
		if cfg, err := config.Load(cfgPath); err == nil && cfg.Listen != "" {
			return normalizeAddr(cfg.Listen)
		}
	}
	return defaultAddr
}

// normalizeAddr turns a listen-style address into a usable base URL. It accepts
// values like "http://host:port", "host:port" and ":port". A leading ":" or a
// missing host binds to 127.0.0.1, mirroring how a daemon listening on ":port"
// is reached from the same machine. The result has no trailing slash.
func normalizeAddr(addr string) string {
	addr = strings.TrimSpace(addr)
	addr = strings.TrimRight(addr, "/")
	if !strings.HasPrefix(addr, "http://") && !strings.HasPrefix(addr, "https://") {
		if strings.HasPrefix(addr, ":") {
			addr = "127.0.0.1" + addr
		} else if host, port, ok := strings.Cut(addr, ":"); ok && host == "" {
			addr = "127.0.0.1:" + port
		}
		addr = "http://" + addr
	}
	return addr
}

// ---------------------------------------------------------------------------
// HTTP helpers
// ---------------------------------------------------------------------------

var httpClient = &http.Client{Timeout: 30 * time.Second}

// doJSON performs a request and decodes a 2xx JSON body into out (out may be
// nil to discard the body). Non-2xx responses are turned into an error using
// the API's {"error": ...} envelope when present.
func doJSON(method, url string, out any) error {
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		return err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach daemon at %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return httpError(resp)
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// httpError extracts a useful error from a non-2xx response.
func httpError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var apiErr model.APIError
	if err := json.Unmarshal(body, &apiErr); err == nil && apiErr.Error != "" {
		return fmt.Errorf("%s: %s", resp.Status, apiErr.Error)
	}
	if msg := strings.TrimSpace(string(body)); msg != "" {
		return fmt.Errorf("%s: %s", resp.Status, msg)
	}
	return errors.New(resp.Status)
}

// fail prints an error to stderr and returns a non-zero exit code.
func fail(format string, a ...any) int {
	fmt.Fprintf(os.Stderr, "loop: "+format+"\n", a...)
	return 1
}

// subFlags builds a FlagSet that exposes the shared --addr flag and parses the
// subcommand's args, silencing the default error output so we control it.
func subFlags(name string, args []string) (*flag.FlagSet, *string, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	addr := fs.String("addr", "", "daemon base URL")
	err := fs.Parse(args)
	return fs, addr, err
}

// ---------------------------------------------------------------------------
// ls
// ---------------------------------------------------------------------------

func runLs(args []string, cfgPath string) int {
	_, addr, err := subFlags("ls", args)
	if err != nil {
		return fail("ls: %v", err)
	}
	base := resolveAddr(*addr, cfgPath)

	var views []model.TaskView
	if err := doJSON(http.MethodGet, base+"/api/tasks", &views); err != nil {
		return fail("%v", err)
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tTYPE\tCRON\tLAST-STATUS\tNEXT-RUN")
	for _, v := range views {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			v.Task.Name,
			dash(string(v.Task.Type)),
			dash(v.Task.Cron),
			lastStatus(v),
			nextRun(v.NextRun),
		)
	}
	_ = tw.Flush()
	return 0
}

func lastStatus(v model.TaskView) string {
	if v.Running {
		return string(model.StatusRunning)
	}
	if v.LastRun != nil && v.LastRun.Status != "" {
		return string(v.LastRun.Status)
	}
	return "-"
}

func nextRun(t time.Time) string {
	if t.IsZero() || t.Year() <= 1 {
		return "-"
	}
	return t.Local().Format("Jan 02 15:04:05")
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// ---------------------------------------------------------------------------
// run
// ---------------------------------------------------------------------------

func runRun(args []string, cfgPath string) int {
	fs, addr, err := subFlags("run", args)
	if err != nil {
		return fail("run: %v", err)
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return fail("run: expected exactly one task name")
	}
	base := resolveAddr(*addr, cfgPath)

	var tr model.TriggerResponse
	url := base + "/api/tasks/" + pathEscape(rest[0]) + "/run"
	if err := doJSON(http.MethodPost, url, &tr); err != nil {
		return fail("%v", err)
	}
	fmt.Println(tr.RunID)
	return 0
}

// ---------------------------------------------------------------------------
// logs
// ---------------------------------------------------------------------------

func runLogs(args []string, cfgPath string) int {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	addr := fs.String("addr", "", "daemon base URL")
	follow := fs.Bool("f", false, "follow the live log stream")
	if err := fs.Parse(args); err != nil {
		return fail("logs: %v", err)
	}
	rest := fs.Args()
	if len(rest) < 1 || len(rest) > 2 {
		return fail("logs: usage: logs [-f] <task> [id]")
	}
	base := resolveAddr(*addr, cfgPath)
	task := rest[0]

	var runID int64
	if len(rest) == 2 {
		id, err := strconv.ParseInt(rest[1], 10, 64)
		if err != nil {
			return fail("logs: invalid run id %q", rest[1])
		}
		runID = id
	} else {
		id, err := latestRunID(base, task)
		if err != nil {
			return fail("%v", err)
		}
		runID = id
	}

	if *follow {
		if err := streamLog(base, task, runID); err != nil {
			return fail("%v", err)
		}
		return 0
	}
	if err := printLog(base, task, runID); err != nil {
		return fail("%v", err)
	}
	return 0
}

// latestRunID fetches the task detail and returns the id of its newest run.
func latestRunID(base, task string) (int64, error) {
	var detail model.TaskDetail
	url := base + "/api/tasks/" + pathEscape(task)
	if err := doJSON(http.MethodGet, url, &detail); err != nil {
		return 0, err
	}
	if len(detail.Runs) == 0 {
		return 0, fmt.Errorf("task %q has no runs yet", task)
	}
	// Runs are returned newest first; fall back to scanning for the max id.
	newest := detail.Runs[0]
	for _, r := range detail.Runs[1:] {
		if r.ID > newest.ID {
			newest = r
		}
	}
	return newest.ID, nil
}

// printLog fetches and prints a finished run's static log.
func printLog(base, task string, runID int64) error {
	url := fmt.Sprintf("%s/api/tasks/%s/runs/%d/log", base, pathEscape(task), runID)
	resp, err := httpClient.Get(url)
	if err != nil {
		return fmt.Errorf("cannot reach daemon at %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return httpError(resp)
	}
	if _, err := io.Copy(os.Stdout, resp.Body); err != nil {
		return fmt.Errorf("read log: %w", err)
	}
	return nil
}

// streamLog consumes the SSE endpoint, printing one log line per "data:" frame
// and stopping when the server emits the named "done" event. The server sends
// one "data:" line per log line and a final `event: done` whose data is the
// terminal status.
func streamLog(base, task string, runID int64) error {
	url := fmt.Sprintf("%s/api/tasks/%s/runs/%d/stream", base, pathEscape(task), runID)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")

	// No client timeout for the streaming connection: it stays open until the
	// run finishes.
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach daemon at %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return httpError(resp)
	}

	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)

	var event string // the current frame's "event:" name, if any
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			// Blank line terminates an event frame; reset and flush.
			event = ""
			out.Flush()
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			data := strings.TrimPrefix(line, "data:")
			data = strings.TrimPrefix(data, " ")
			if event == "done" {
				out.Flush()
				return nil
			}
			fmt.Fprintln(out, data)
		default:
			// Ignore other SSE fields (id:, retry:, comments).
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("stream interrupted: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// validate (offline)
// ---------------------------------------------------------------------------

func runValidate(args []string, cfgPath string) int {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return fail("validate: %v", err)
	}
	path := cfgPath
	if rest := fs.Args(); len(rest) > 0 {
		path = rest[0]
	}
	if path == "" {
		return fail("validate: no config path given")
	}
	if _, err := config.Load(path); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}
	fmt.Println("OK")
	return 0
}

// ---------------------------------------------------------------------------
// reload
// ---------------------------------------------------------------------------

func runReload(args []string, cfgPath string) int {
	_, addr, err := subFlags("reload", args)
	if err != nil {
		return fail("reload: %v", err)
	}
	base := resolveAddr(*addr, cfgPath)
	if err := doJSON(http.MethodPost, base+"/api/reload", nil); err != nil {
		return fail("%v", err)
	}
	fmt.Println("reload requested")
	return 0
}

// pathEscape escapes a single URL path segment. url.PathEscape encodes spaces
// as "%20" (not "+"), which is what path segments require.
func pathEscape(s string) string {
	return url.PathEscape(s)
}
