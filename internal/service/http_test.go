package service

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"

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
