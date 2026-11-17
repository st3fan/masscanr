// Package cloud defines the temporary worker lifecycle independently of a provider.
package cloud

import (
	"context"
	"crypto"
	"crypto/ed25519"
	"errors"
	"fmt"
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
	Password string  `json:"-"`
	Key      *SSHKey `json:"-"`
	Platform string
	Label    string
	Tags     []string
	Ready    bool
}

func (w Worker) HasAuth() bool {
	return w.Password != "" || w.Key != nil
}

// SSHKey keeps controller-only private material out of ordinary formatting and
// serialized worker state. It is shared by all workers in one DigitalOcean run.
type SSHKey struct{ private ed25519.PrivateKey }

func NewSSHKey(private ed25519.PrivateKey) (*SSHKey, error) {
	if len(private) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("invalid SSH private key")
	}
	return &SSHKey{private: append(ed25519.PrivateKey(nil), private...)}, nil
}

func (k *SSHKey) Signer() crypto.Signer { return k.private }
func (*SSHKey) String() string          { return "[REDACTED]" }
func (*SSHKey) GoString() string        { return "[REDACTED]" }

// Provider must reject deletion unless a fresh lookup proves current-run
// ownership. List must return only that run's workers, never all app workers.
type Provider interface {
	Validate(context.Context, Spec) error
	Create(context.Context, Spec) (Worker, error)
	Get(context.Context, string) (Worker, error)
	List(context.Context, string) ([]Worker, error)
	Delete(context.Context, Worker, string) error
}
