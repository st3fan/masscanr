// Package registry resolves one concrete backend at the CLI composition point.
package registry

import (
	"fmt"
	"github.com/st3fan/masscanr/internal/backend"
	"github.com/st3fan/masscanr/internal/backend/masscan"
	"github.com/st3fan/masscanr/internal/backend/zmap"
)

func Resolve(name string) (backend.Backend, error) {
	switch name {
	case "masscan":
		return masscan.Backend{}, nil
	case "zmap":
		return zmap.Backend{}, nil
	default:
		return nil, fmt.Errorf("unknown backend %q; choose masscan or zmap", name)
	}
}
