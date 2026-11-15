package service

import (
	"embed"
	"fmt"
	"html"
	"strings"

	"github.com/gofiber/fiber/v3"
)

//go:embed stoplight-9.0.29/*
var stoplightAssets embed.FS

const stoplightVersion = "9.0.29"

func (a *API) docs(app *fiber.App) {
	assetBase := "/docs/assets/stoplight-" + stoplightVersion + "/"
	app.Get("/docs", func(c fiber.Ctx) error {
		page := fmt.Sprintf(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>masscanr API</title><link rel="stylesheet" href="%sstyles.min.css">
<style>body{margin:0}elements-api{height:100vh;display:block}</style></head>
<body><elements-api apiDescriptionUrl="/openapi.json" router="hash" layout="sidebar"></elements-api>
<script src="%sweb-components.min.js" defer></script></body></html>`, html.EscapeString(assetBase), html.EscapeString(assetBase))
		c.Set("Content-Type", "text/html; charset=utf-8")
		c.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; font-src 'self' data:")
		return c.SendString(page)
	})
	app.Get(assetBase+":name", func(c fiber.Ctx) error {
		name := c.Params("name")
		switch name {
		case "styles.min.css":
			c.Set("Content-Type", "text/css; charset=utf-8")
		case "web-components.min.js":
			c.Set("Content-Type", "text/javascript; charset=utf-8")
		default:
			return c.SendStatus(404)
		}
		if strings.ContainsAny(name, `/\`) {
			return c.SendStatus(404)
		}
		data, err := stoplightAssets.ReadFile("stoplight-" + stoplightVersion + "/" + name)
		if err != nil {
			return err
		}
		c.Set("Cache-Control", "public, max-age=31536000, immutable")
		return c.Send(data)
	})
}
