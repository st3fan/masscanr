package controller

import (
	"fmt"

	"github.com/st3fan/masscanr/internal/backend"
	"github.com/st3fan/masscanr/internal/cloud"
)

func preparationFor(b backend.Backend, w cloud.Worker) (backend.Preparation, error) {
	if w.Platform == "" || w.Platform == "alpine" {
		return b.Preparation(), nil
	}
	p, ok := b.(backend.PlatformBackend)
	if !ok {
		return backend.Preparation{}, fmt.Errorf("backend %s does not support platform %s", b.Name(), w.Platform)
	}
	return p.PreparationFor(w.Platform)
}
