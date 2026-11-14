package service

import (
	"context"
	"encoding/json"
	"github.com/st3fan/masscanr/internal/scan"
	"testing"
)

func TestFrozenPlanAndApproval(t *testing.T) {
	s := testStore(t)
	calls := 0
	a := &API{Store: s, Resolve: func(_ context.Context, v []scan.Target) ([]scan.Target, error) { calls++; return v, nil }}
	h := a.Handler()
	w := request(h, "POST", "/v1/scans", `{"targets":["192.0.2.0/24"],"ports":"0-65535"}`)
	var v Scan
	json.Unmarshal(w.Body.Bytes(), &v)
	w = request(h, "POST", "/v1/scans/"+v.ID+"/preflight", "")
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body)
	}
	var p Plan
	json.Unmarshal(w.Body.Bytes(), &p)
	if !p.RequiresConfirmation {
		t.Fatal(p)
	}
	if _, e := s.Reserve(context.Background(), v.ID, p.ID, "key", false); e != ErrConflict {
		t.Fatal(e)
	}
	run, e := s.Reserve(context.Background(), v.ID, p.ID, "key", true)
	if e != nil {
		t.Fatal(e)
	}
	same, e := s.Reserve(context.Background(), v.ID, p.ID, "key", true)
	if e != nil || same.ID != run.ID || calls != 1 {
		t.Fatal(same, e, calls)
	}
	if e = s.DeleteScan(context.Background(), v.ID); e != ErrConflict {
		t.Fatal(e)
	}
}
