package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func request(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
	return w
}
func TestCRUD(t *testing.T) {
	s := testStore(t)
	h := (&API{Store: s}).Handler()
	w := request(h, "POST", "/v1/scans", `{"targets":["192.0.2.1"]}`)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body)
	}
	var v Scan
	json.Unmarshal(w.Body.Bytes(), &v)
	if v.Definition.Shards != 2 {
		t.Fatal(v)
	}
	w = request(h, "PUT", "/v1/scans/"+v.ID, `{"revision":9,"definition":{"targets":["192.0.2.2"]}}`)
	if w.Code != 409 {
		t.Fatal(w.Code, w.Body)
	}
	for _, body := range []string{`{"targets":[],"unknown":true}`, `null`, `{"targets":["192.0.2.1"]} {}`} {
		w = request(h, "POST", "/v1/scans", body)
		if w.Code != 400 {
			t.Fatal(body, w.Code)
		}
	}
	w = request(h, "DELETE", "/v1/scans/"+v.ID, "")
	if w.Code != 204 {
		t.Fatal(w.Code)
	}
	if _, e := s.GetScan(context.Background(), v.ID); e != ErrNotFound {
		t.Fatal(e)
	}
}
