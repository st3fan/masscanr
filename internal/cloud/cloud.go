// Package cloud defines the temporary worker lifecycle independently of a provider.
package cloud

import (
	"context"
	"errors"
)

var ErrNotFound = errors.New("worker not found")

const Label = "masscanr"

type Spec struct {
	RunID        string
	Shard        int
	Region       string
	InstanceType string
}

type Worker struct {
	ID       string
	IP       string
	Password string `json:"-"`
	Label    string
	Tags     []string
	Ready    bool
}

// Provider must reject deletion unless a fresh lookup proves current-run
// ownership. List must return only that run's workers, never all app workers.
type Provider interface {
	Validate(context.Context, Spec) error
	Create(context.Context, Spec) (Worker, error)
	Get(context.Context, string) (Worker, error)
	List(context.Context, string) ([]Worker, error)
	Delete(context.Context, Worker, string) error
}
