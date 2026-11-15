package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func request(h *fiber.App, method, path, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", jsonAPIMediaType)
	}
	resp, err := h.Test(req)
	if err != nil {
		panic(err)
	}
	for k, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(k, value)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
	_ = resp.Body.Close()
	return w
}
func decodeTestResource[T any](b []byte, out *T) error {
	var doc struct {
		Data struct {
			ID         string          `json:"id"`
			Attributes json.RawMessage `json:"attributes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return err
	}
	var attrs map[string]json.RawMessage
	if err := json.Unmarshal(doc.Data.Attributes, &attrs); err != nil {
		return err
	}
	attrs["id"], _ = json.Marshal(doc.Data.ID)
	merged, err := json.Marshal(attrs)
	if err != nil {
		return err
	}
	return json.Unmarshal(merged, out)
}

func decodeTestList[T any](b []byte, out *[]T) error {
	var doc struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return err
	}
	for _, raw := range doc.Data {
		var value T
		if err := decodeTestResource(rawToDocument(raw), &value); err != nil {
			return err
		}
		*out = append(*out, value)
	}
	return nil
}

func rawToDocument(raw json.RawMessage) []byte {
	b, _ := json.Marshal(map[string]json.RawMessage{"data": raw})
	return b
}
func TestCRUD(t *testing.T) {
	s := testStore(t)
	h := (&API{Store: s}).Handler()
	w := request(h, "POST", "/v1/scans", `{"data":{"type":"scans","attributes":{"targets":["192.0.2.1"]}}}`)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body)
	}
	var v Scan
	decodeTestResource(w.Body.Bytes(), &v)
	if v.Definition.Shards != 2 {
		t.Fatal(v)
	}
	w = request(h, "PATCH", "/v1/scans/"+v.ID, `{"data":{"type":"scans","id":"`+v.ID+`","attributes":{"revision":9,"definition":{"targets":["192.0.2.2"]}}}}`)
	if w.Code != 409 {
		t.Fatal(w.Code, w.Body)
	}
	w = request(h, "PATCH", "/v1/scans/"+v.ID, `{"data":{"type":"scans","id":"`+v.ID+`","attributes":{"revision":1,"definition":{"ports":"443"}}}}`)
	var updated Scan
	if w.Code != 200 || decodeTestResource(w.Body.Bytes(), &updated) != nil || updated.Revision != 2 || updated.Definition.Ports != "443" || len(updated.Definition.Targets) != 1 || updated.Definition.Targets[0] != "192.0.2.1" {
		t.Fatal(w.Code, w.Body)
	}
	w = request(h, "PATCH", "/v1/scans/"+v.ID, `{"data":{"type":"scans","id":"`+v.ID+`","attributes":{"revision":2,"definition":{"unknown":true}}}}`)
	if w.Code != 400 {
		t.Fatal(w.Code, w.Body)
	}
	for _, body := range []string{`{"data":{"type":"scans","attributes":{"targets":[],"unknown":true}}}`, `null`, `{"data":{"type":"scans","attributes":{"targets":["192.0.2.1"]}}} {}`} {
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
