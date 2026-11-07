package controller

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

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
