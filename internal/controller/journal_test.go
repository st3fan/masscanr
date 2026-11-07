package controller

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/st3fan/masscanr/internal/cloud"
	"github.com/st3fan/masscanr/internal/scan"
	"github.com/st3fan/masscanr/internal/scanfile"
)

type syncOutput struct {
	bytes.Buffer
	syncs, failAt int
}

func TestImmediateCancellationStillProducesAValidDocument(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	p := newProvider()
	var out bytes.Buffer
	_, err := testController(p, fakeRunner{}).Run(ctx, testConfig(), &out, nil)
	s, replayErr := scanfile.Replay(&out, nil)
	if !errors.Is(err, context.Canceled) || replayErr != nil || s.Finished == nil || s.Finished.ScanOutcome != "cancelled" || p.created != 0 {
		t.Fatal(err, replayErr, p.created)
	}
}

func (s *syncOutput) Sync() error {
	s.syncs++
	if s.syncs == s.failAt {
		return errors.New("fixture disk sync failure")
	}
	return nil
}

func TestDurabilityBarriersBeforeAndAfterCreation(t *testing.T) {
	for barrier := 1; barrier <= 5; barrier++ {
		t.Run(fmt.Sprint(barrier), func(t *testing.T) {
			p := newProvider()
			out := &syncOutput{failAt: barrier}
			_, err := testController(p, fakeRunner{}).Run(t.Context(), testConfig(), out, nil)
			wantCreated := (barrier - 1) / 2
			if err == nil || !strings.Contains(err.Error(), "disk sync failure") || p.created != wantCreated || p.deleted != p.created || len(p.workers) != 1 {
				t.Fatal(err, p.created, p.deleted)
			}
			if barrier > 1 {
				s, replayErr := scanfile.Replay(strings.NewReader(out.String()), nil)
				if replayErr != nil || s.Finished != nil {
					t.Fatal("invalid partial document", replayErr)
				}
			}
		})
	}
}

func TestCompleteScanRetainsIndependentCleanupFailure(t *testing.T) {
	p := newProvider()
	p.deleteFail = true
	var out bytes.Buffer
	summary, err := testController(p, fakeRunner{}).Run(t.Context(), testConfig(), &out, nil)
	s, replayErr := scanfile.Replay(&out, nil)
	if err == nil || replayErr != nil || !s.Complete() || s.Finished.CleanupOutcome != "incomplete" || len(s.Finished.Undeleted) != 2 || summary.ScanOutcome != "complete" || summary.CleanupOutcome != "incomplete" {
		t.Fatal(summary, err, replayErr)
	}
}

func TestJournalRedactsWorkerPasswordAndPreservesUnknownTelemetry(t *testing.T) {
	var out bytes.Buffer
	r := fakeRunner{scan: func(ctx context.Context, w cloud.Worker, args []string, out io.Writer, progress func(scan.Progress)) error {
		progress(scan.Progress{PercentUnknown: true, RateUnknown: true, ETAUnknown: true})
		return errors.New("failure containing " + w.Password)
	}}
	_, err := testController(newProvider(), r).Run(t.Context(), testConfig(), &out, func(e Event) {
		if strings.Contains(e.Detail, "secret") {
			t.Error("password leaked through lifecycle event")
		}
	})
	data := out.String()
	s, replayErr := scanfile.Replay(strings.NewReader(data), nil)
	if err == nil || strings.Contains(err.Error(), "secret") || replayErr != nil || s.Finished == nil || strings.Contains(data, "secret") || !strings.Contains(data, "[REDACTED]") {
		t.Fatal(err, replayErr, data)
	}
	for _, p := range s.Progress {
		if p.Percent != nil || p.Rate != nil || p.ETASeconds != nil {
			t.Fatal("unknown telemetry became zero", p)
		}
	}
}

func TestControllerAssignsFrozenTargetProvenance(t *testing.T) {
	for _, directFirst := range []bool{false, true} {
		cfg := testConfig()
		args := []string{"AS1234", "192.0.2.0/24"}
		if directFirst {
			args[0], args[1] = args[1], args[0]
		}
		cfg.RequestedTargets, _ = scan.ParseRequestedTargets(args)
		for i := range cfg.RequestedTargets {
			if cfg.RequestedTargets[i].IsAS() {
				cfg.RequestedTargets[i].Prefixes = []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("192.0.2.0/25")}
				cfg.RequestedTargets[i].Resolution = &scan.Resolution{Source: scan.RIPEEndpoint, FetchedAt: time.Unix(100, 0), SnapshotAt: time.Unix(90, 0)}
			}
		}
		p := newProvider()
		p.onCreate = func() { cfg.RequestedTargets[0].Target = "changed" }
		var out bytes.Buffer
		summary, err := testController(p, fakeRunner{}).Run(t.Context(), cfg, &out, nil)
		var findings []scanfile.Finding
		_, replayErr := scanfile.Replay(&out, func(f scanfile.Finding) error { findings = append(findings, f); return nil })
		if err != nil || replayErr != nil || summary.Found != 2 || len(findings) != 2 {
			t.Fatal(summary, err, replayErr, findings)
		}
		for _, f := range findings {
			wantTarget, wantPrefix := "AS1234", "192.0.2.0/25"
			if directFirst {
				wantTarget, wantPrefix = "192.0.2.0/24", ""
			}
			if f.Target != wantTarget || f.Prefix != wantPrefix {
				t.Fatal(f)
			}
		}
	}
}
