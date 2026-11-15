package webui

import (
	"io/fs"
	"mime"
	"path"
	"strings"

	"github.com/gofiber/fiber/v3"
)

// Register serves only the embedded SPA and its own assets. API and docs routes
// keep their existing handlers; unknown reserved paths never become index.html.
func Register(app *fiber.App, files fs.FS) {
	if files == nil {
		return
	}
	app.Use(func(c fiber.Ctx) error {
		if c.Method() != fiber.MethodGet && c.Method() != fiber.MethodHead {
			return c.Next()
		}
		requested := c.Path()
		if requested == "/" {
			requested = "/index.html"
		}
		if strings.HasPrefix(requested, "/v1/") || requested == "/v1" || requested == "/healthz" || requested == "/openapi.json" || strings.HasPrefix(requested, "/docs") {
			return c.Next()
		}
		name := strings.TrimPrefix(path.Clean(requested), "/")
		if strings.HasPrefix(requested, "/assets/") {
			data, err := fs.ReadFile(files, name)
			if err != nil {
				return c.Next()
			}
			c.Set("Cache-Control", "public, max-age=31536000, immutable")
			c.Type(path.Ext(name))
			return c.Send(data)
		}
		if requested != "/index.html" && strings.Contains(path.Base(name), ".") {
			return c.Next()
		}
		data, err := fs.ReadFile(files, "index.html")
		if err != nil {
			return c.Next()
		}
		c.Set("Content-Type", mime.TypeByExtension(".html"))
		c.Set("Cache-Control", "no-store")
		return c.Send(data)
	})
}
