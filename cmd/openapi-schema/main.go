// Command openapi-schema writes the registered Huma contract without opening a listener.
package main

import (
	"encoding/json"
	"github.com/gofiber/fiber/v3"
	"github.com/st3fan/masscanr/internal/service"
	"os"
)

func main() {
	api := &service.API{}
	api.Extra = func(app *fiber.App) { (&service.Manager{Store: api.Store}).Routes(api, app) }
	api.Handler()
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(api.Huma.OpenAPI()); err != nil {
		panic(err)
	}
}
