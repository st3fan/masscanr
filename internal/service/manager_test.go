package service

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/dbos-inc/dbos-transact-golang/dbos"
	"github.com/st3fan/masscanr/internal/backend"
	"github.com/st3fan/masscanr/internal/backend/registry"
	"github.com/st3fan/masscanr/internal/cloud"
	"github.com/st3fan/masscanr/internal/controller"
	"github.com/st3fan/masscanr/internal/scan"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type providerFixture struct {
	validate func(context.Context) error
	mu       sync.Mutex
	workers  map[string]cloud.Worker
	creates  int
}

func (p *providerFixture) Validate(ctx context.Context, _ cloud.Spec) error {
	if p.validate != nil {
		return p.validate(ctx)
	}
	return nil
}
func (p *providerFixture) Create(_ context.Context, s cloud.Spec) (cloud.Worker, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.creates++
	w := cloud.Worker{ID: fmt.Sprint(s.Shard), IP: "192.0.2.1", Password: "fixture-secret", Label: cloud.Label, Tags: []string{"masscanr-run-" + s.RunID}, Ready: true}
	p.workers[w.ID] = w
	return w, nil
}
func (p *providerFixture) Get(_ context.Context, id string) (cloud.Worker, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	w, ok := p.workers[id]
	if !ok {
		return w, cloud.ErrNotFound
	}
	return w, nil
}
func (p *providerFixture) List(context.Context, string) ([]cloud.Worker, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []cloud.Worker
	for _, w := range p.workers {
		out = append(out, w)
	}
	return out, nil
}
func (p *providerFixture) Delete(_ context.Context, w cloud.Worker, id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	v, ok := p.workers[w.ID]
	if !ok {
		return nil
	}
	if v.Label != cloud.Label || len(v.Tags) != 1 || v.Tags[0] != "masscanr-run-"+id {
		return fmt.Errorf("not owned")
	}
	delete(p.workers, w.ID)
	return nil
}

type runnerFixture struct {
	name    string
	block   bool
	calls   atomic.Int32
	started chan struct{}
	once    sync.Once
}

func (r *runnerFixture) Prepare(context.Context, cloud.Worker, backend.Preparation) (scan.Prepared, error) {
	return scan.Prepared{Identity: scan.Identity{Name: r.name, Version: "fixture", Build: "fixture"}}, nil
}
func (r *runnerFixture) Scan(ctx context.Context, _ cloud.Worker, _ backend.Plan, sink func(scan.Finding) error, _ func(scan.Progress)) error {
	r.calls.Add(1)
	r.once.Do(func() { close(r.started) })
	if r.block {
		<-ctx.Done()
		return ctx.Err()
	}
	return sink(scan.Finding{Address: netip.MustParseAddr("192.0.2.1"), Port: 80, Protocol: "tcp", State: "open", Timestamp: 1})
}
func reserveFixture(t *testing.T, s *Store, name string) Run {
	t.Helper()
	a := &API{Store: s}
	h := a.Handler()
	w := request(h, "POST", "/v1/scans", `{"targets":["192.0.2.1"],"shards":1,"ports":"80","backend":"`+name+`"}`)
	var v Scan
	if e := json.Unmarshal(w.Body.Bytes(), &v); e != nil {
		t.Fatal(e)
	}
	w = request(h, "POST", "/v1/scans/"+v.ID+"/preflight", "")
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body)
	}
	var p Plan
	json.Unmarshal(w.Body.Bytes(), &p)
	r, e := s.Reserve(context.Background(), v.ID, p.ID, newID(), true)
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func waitRun(t *testing.T, s *Store, id string) Run {
	t.Helper()
	until := time.Now().Add(15 * time.Second)
	for time.Now().Before(until) {
		var active bool
		if e := s.Pool.QueryRow(context.Background(), `SELECT active FROM masscanr.runs WHERE id=$1`, id).Scan(&active); e != nil {
			t.Fatal(e)
		}
		if !active {
			r, e := s.GetRun(context.Background(), id)
			if e != nil {
				t.Fatal(e)
			}
			return r
		}
		time.Sleep(20 * time.Millisecond)
	}
	r, _ := s.GetRun(context.Background(), id)
	t.Fatalf("run never settled: %+v", r)
	return Run{}
}
func TestDurableExecution(t *testing.T) {
	for _, name := range []string{"masscan", "zmap"} {
		for _, stop := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stop=%v", name, stop), func(t *testing.T) {
				s := testStore(t)
				dc, e := s.WorkflowContext(context.Background())
				if e != nil {
					t.Fatal(e)
				}
				m := NewManager(s, dc, t.TempDir())
				p := &providerFixture{workers: map[string]cloud.Worker{}}
				runner := &runnerFixture{name: name, block: stop, started: make(chan struct{})}
				m.Factory = func(r Run) *controller.Controller {
					b, _ := registry.Resolve(name)
					timeouts := r.Plan.Timeouts
					timeouts.Poll = time.Millisecond
					return &controller.Controller{RunID: r.ID, Backend: b, Provider: p, Runner: runner, Timeouts: timeouts}
				}
				if e = dbos.Launch(dc); e != nil {
					t.Fatal(e)
				}
				t.Cleanup(func() { dbos.Shutdown(dc, 5*time.Second) })
				r := reserveFixture(t, s, name)
				if e = m.Launch(r.ID); e != nil {
					t.Fatal(e)
				}
				if e = m.Launch(r.ID); e != nil {
					t.Fatal(e)
				}
				select {
				case <-runner.started:
				case <-time.After(10 * time.Second):
					t.Fatal("scanner not started")
				}
				if stop {
					if e = m.requestStop(context.Background(), r.ID); e != nil {
						t.Fatal(e)
					}
				}
				got := waitRun(t, s, r.ID)
				if got.Summary.CleanupOutcome != "complete" {
					t.Fatal(got)
				}
				want := "complete"
				if stop {
					want = "cancelled"
				}
				if got.Summary.ScanOutcome != want {
					t.Fatal(got)
				}
				if runner.calls.Load() != 1 || p.creates != 1 {
					t.Fatal("duplicate execution")
				}
			})
		}
	}
}
func TestAttemptedExecutionNeverReplays(t *testing.T) {
	s := testStore(t)
	r := reserveFixture(t, s, "masscan")
	s.Pool.Exec(context.Background(), `UPDATE masscanr.runs SET attempted=true WHERE id=$1`, r.ID)
	m := &Manager{Store: s}
	if e := m.execute(context.Background(), r.ID); e != nil {
		t.Fatal(e)
	}
	got, _ := s.GetRun(context.Background(), r.ID)
	if got.Summary.ScanOutcome != "interrupted" {
		t.Fatal(got)
	}
}

func TestCrashHelper(t *testing.T) {
	if os.Getenv("MASSCANR_CRASH_HELPER") != "1" {
		return
	}
	ctx := context.Background()
	s, e := Open(ctx, os.Getenv("MASSCANR_CRASH_DSN"))
	if e != nil {
		t.Fatal(e)
	}
	dc, e := s.WorkflowContext(ctx)
	if e != nil {
		t.Fatal(e)
	}
	m := NewManager(s, dc, os.Getenv("MASSCANR_CRASH_DIR"))
	p := &providerFixture{workers: map[string]cloud.Worker{}, validate: func(ctx context.Context) error {
		os.WriteFile(filepath.Join(m.Dir, "ready"), []byte("ready"), 0600)
		<-ctx.Done()
		return ctx.Err()
	}}
	m.Factory = func(r Run) *controller.Controller {
		b, _ := registry.Resolve("masscan")
		return &controller.Controller{RunID: r.ID, Backend: b, Provider: p, Runner: &runnerFixture{}, Timeouts: r.Plan.Timeouts}
	}
	if e = dbos.Launch(dc); e != nil {
		t.Fatal(e)
	}
	if e = m.Launch(os.Getenv("MASSCANR_CRASH_RUN")); e != nil {
		t.Fatal(e)
	}
	select {}
}
func TestProcessCrashRecovery(t *testing.T) {
	s := testStore(t)
	r := reserveFixture(t, s, "masscan")
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestCrashHelper$")
	cmd.Env = append(os.Environ(), "MASSCANR_CRASH_HELPER=1", "MASSCANR_CRASH_DSN="+s.Pool.Config().ConnString(), "MASSCANR_CRASH_DIR="+dir, "MASSCANR_CRASH_RUN="+r.ID)
	if e := cmd.Start(); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			cmd.Process.Kill()
			cmd.Wait()
		}
	})
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, e := os.Stat(filepath.Join(dir, "ready")); e == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("helper never entered provisioning")
		}
		time.Sleep(20 * time.Millisecond)
	}
	cmd.Process.Kill()
	cmd.Wait()
	dc, e := s.WorkflowContext(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	m := NewManager(s, dc, dir)
	p := &providerFixture{workers: map[string]cloud.Worker{}}
	m.Factory = func(r Run) *controller.Controller {
		return &controller.Controller{Provider: p, Timeouts: r.Plan.Timeouts}
	}
	recovery, e := m.PrepareRecovery(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if e = dbos.Launch(dc); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { dbos.Shutdown(dc, 5*time.Second) })
	if e = m.ResumeReserved(recovery); e != nil {
		t.Fatal(e)
	}
	got := waitRun(t, s, r.ID)
	if got.Summary.ScanOutcome != "interrupted" || got.Summary.CleanupOutcome != "complete" || p.creates != 0 {
		t.Fatal(got)
	}
}
