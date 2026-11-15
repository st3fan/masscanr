package webui

import (
	"io"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/gofiber/fiber/v3"
)

func TestRoutesAndAssetIsolation(t *testing.T) {
	app := fiber.New()
	app.Get("/v1/targets", func(c fiber.Ctx) error { return c.SendString("api") })
	files := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("dashboard")}, "assets/app.js": &fstest.MapFile{Data: []byte("script")}}
	Register(app, files)
	for _, tc := range []struct {
		path, body string
		status     int
	}{
		{"/", "dashboard", 200}, {"/runs/123", "dashboard", 200}, {"/assets/app.js", "script", 200},
		{"/v1/targets", "api", 200}, {"/v1/missing", "", 404}, {"/assets/missing.js", "", 404}, {"/openapi.json", "", 404},
	} {
		response, err := app.Test(httptest.NewRequest("GET", tc.path, nil))
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != tc.status || tc.status == 200 && string(body) != tc.body {
			t.Fatalf("%s: %d %q", tc.path, response.StatusCode, body)
		}
	}
}
