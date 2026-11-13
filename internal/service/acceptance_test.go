package service

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

func TestAdmissionIsAtomicAndHistorySurvivesDelete(t *testing.T) {
	s := testStore(t)
	h := (&API{Store: s}).Handler()
	w := request(h, "POST", "/v1/scans", `{"targets":["192.0.2.1"]}`)
	var v Scan
	json.Unmarshal(w.Body.Bytes(), &v)
	w = request(h, "POST", "/v1/scans/"+v.ID+"/preflight", "")
	var p Plan
	json.Unmarshal(w.Body.Bytes(), &p)
	ctx := context.Background()
	var wg sync.WaitGroup
	out := make(chan Run, 8)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := s.Reserve(ctx, v.ID, p.ID, newID(), false)
			if e == nil {
				out <- r
			} else {
				errs <- e
			}
		}()
	}
	wg.Wait()
	close(out)
	close(errs)
	if len(out) != 1 {
		t.Fatalf("accepted %d runs", len(out))
	}
	for e := range errs {
		if e != ErrConflict {
			t.Fatal(e)
		}
	}
	r := <-out
	s.Pool.Exec(ctx, `UPDATE masscanr.runs SET active=false WHERE id=$1`, r.ID)
	if e := s.DeleteScan(ctx, v.ID); e != nil {
		t.Fatal(e)
	}
	if _, e := s.GetRun(ctx, r.ID); e != nil {
		t.Fatal(e)
	}
}
func TestStaleAndExpiredPlansCannotStart(t *testing.T) {
	for _, edit := range []bool{true, false} {
		t.Run(time.Now().String(), func(t *testing.T) {
			s := testStore(t)
			h := (&API{Store: s}).Handler()
			w := request(h, "POST", "/v1/scans", `{"targets":["192.0.2.1"]}`)
			var v Scan
			json.Unmarshal(w.Body.Bytes(), &v)
			w = request(h, "POST", "/v1/scans/"+v.ID+"/preflight", "")
			var p Plan
			json.Unmarshal(w.Body.Bytes(), &p)
			ctx := context.Background()
			if edit {
				v.Revision++
				v.Definition.Targets = []string{"198.51.100.1"}
				if e := s.SaveScan(ctx, v, 1, false); e != nil {
					t.Fatal(e)
				}
			} else {
				p.ExpiresAt = time.Now().Add(-time.Second)
				b, _ := json.Marshal(p)
				s.Pool.Exec(ctx, `UPDATE masscanr.plans SET data=$2 WHERE id=$1`, p.ID, b)
			}
			if _, e := s.Reserve(ctx, v.ID, p.ID, newID(), true); e != ErrConflict {
				t.Fatal(e)
			}
			var n int
			s.Pool.QueryRow(ctx, `SELECT count(*) FROM masscanr.runs`).Scan(&n)
			if n != 0 {
				t.Fatal("created run before acceptance")
			}
		})
	}
}
