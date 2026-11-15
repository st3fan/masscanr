package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/st3fan/masscanr/internal/scanfile"
)

type artifactAPI struct {
	dir    string
	getRun func(context.Context, string) (Run, error)
}

type inspection struct {
	RunID            string            `json:"run_id"`
	Format           string            `json:"format"`
	Schema           int               `json:"schema"`
	CreatedAt        time.Time         `json:"created_at"`
	Config           scanfile.Config   `json:"config"`
	AcceptedFindings uint64            `json:"accepted_findings"`
	Records          uint64            `json:"records"`
	Workers          []scanfile.Worker `json:"workers"`
	ScanOutcome      string            `json:"scan_outcome"`
	CleanupOutcome   string            `json:"cleanup_outcome"`
	Complete         bool              `json:"complete"`
	Truncated        bool              `json:"truncated"`
	Undeleted        []string          `json:"undeleted"`
}

type contextReader struct {
	context.Context
	io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.Context.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(p)
}

func (a artifactAPI) runJournal(ctx context.Context, id string) (string, scanfile.State, int, string) {
	if parsed, err := uuid.Parse(id); err != nil || parsed.String() != id {
		return "", scanfile.State{}, 404, "run not found"
	}
	run, err := a.getRun(ctx, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return "", scanfile.State{}, 404, "run not found"
		}
		return "", scanfile.State{}, 500, "internal error"
	}
	path := filepath.Join(a.dir, id+".scan")
	if run.Artifact == "" || filepath.Clean(run.Artifact) != filepath.Clean(path) {
		return "", scanfile.State{}, 409, "run journal unavailable"
	}
	f, err := openArtifact(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", scanfile.State{}, 409, "run journal unavailable"
		}
		return "", scanfile.State{}, 500, "run journal unavailable"
	}
	defer f.Close()
	state, err := scanfile.Replay(contextReader{ctx, f}, nil)
	if err != nil || state.Header == nil || state.Header.RunID != id {
		return "", scanfile.State{}, 500, "run journal invalid"
	}
	return path, state, 0, ""
}

func openArtifact(path string) (*os.File, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("artifact is not a regular file")
	}
	return os.Open(path)
}

func (a artifactAPI) inspect(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	_, state, code, message := a.runJournal(r.Context(), r.PathValue("id"))
	if code != 0 {
		respond(w, code, map[string]string{"error": message})
		return
	}
	workers := make([]scanfile.Worker, 0, len(state.Workers))
	for _, worker := range state.Workers {
		workers = append(workers, worker)
	}
	slices.SortFunc(workers, func(a, b scanfile.Worker) int {
		if a.Shard != b.Shard {
			return a.Shard - b.Shard
		}
		return strings.Compare(a.InstanceID, b.InstanceID)
	})
	result := inspection{RunID: state.Header.RunID, Format: state.Header.Format, Schema: state.Header.Schema, CreatedAt: state.Header.CreatedAt, Config: state.Header.Config, AcceptedFindings: state.Found, Records: state.Seq, Workers: workers, ScanOutcome: "unconfirmed", CleanupOutcome: "unconfirmed", Complete: state.Complete(), Truncated: state.Truncated, Undeleted: state.Undeleted()}
	if state.Finished != nil {
		result.ScanOutcome = state.Finished.ScanOutcome
		result.CleanupOutcome = state.Finished.CleanupOutcome
	}
	if state.Truncated {
		result.ScanOutcome = "unconfirmed"
	}
	respond(w, 200, result)
}

func (a artifactAPI) export(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	format := r.PathValue("format")
	if format != "csv" && format != "jsonl" {
		bad(w, fmt.Errorf("format must be csv or jsonl"))
		return
	}
	path, state, code, message := a.runJournal(r.Context(), r.PathValue("id"))
	if code != 0 {
		respond(w, code, map[string]string{"error": message})
		return
	}
	if !state.Complete() {
		respond(w, 409, map[string]string{"error": "scan results incomplete"})
		return
	}
	path = strings.TrimSuffix(path, ".scan") + "." + format
	f, err := openArtifact(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			respond(w, 409, map[string]string{"error": "saved export missing"})
			return
		}
		respond(w, 500, map[string]string{"error": "saved export unavailable"})
		return
	}
	defer f.Close()
	if format == "csv" {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	} else {
		w.Header().Set("Content-Type", "application/x-ndjson")
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", r.PathValue("id")+"."+format))
	_, _ = io.Copy(w, f)
}
