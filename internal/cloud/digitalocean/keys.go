package digitalocean

import (
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/st3fan/masscanr/internal/cloud"
	"golang.org/x/crypto/ssh"
)

type accountKey struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Fingerprint string `json:"fingerprint"`
	PublicKey   string `json:"public_key"`
}

type runKey struct {
	private     *cloud.SSHKey
	public      string
	fingerprint string
	name        string
}

func runTag(runID string) (string, error) {
	digits := runID
	if len(runID) == 36 && runID[8] == '-' && runID[13] == '-' && runID[18] == '-' && runID[23] == '-' {
		digits = strings.ReplaceAll(runID, "-", "")
	}
	b, err := hex.DecodeString(digits)
	if err != nil || len(b) != 16 {
		return "", fmt.Errorf("invalid run identifier")
	}
	return "masscanr-run-" + runID, nil
}

func (c *Client) derive(runID string) (runKey, error) {
	tag, err := runTag(runID)
	if err != nil {
		return runKey{}, err
	}
	if c.runID != "" && c.runID != runID {
		return runKey{}, fmt.Errorf("DigitalOcean client is bound to another run")
	}
	seed, err := base64.StdEncoding.DecodeString(c.seed)
	if err != nil || len(seed) != ed25519.SeedSize {
		return runKey{}, fmt.Errorf("DIGITALOCEAN_SSH_SEED must be base64 for 32 random bytes")
	}
	mac := hmac.New(sha256.New, seed)
	mac.Write([]byte("masscanr-digitalocean-ed25519-v1\x00"))
	mac.Write([]byte(runID))
	private, err := cloud.NewSSHKey(ed25519.NewKeyFromSeed(mac.Sum(nil)))
	if err != nil {
		return runKey{}, err
	}
	public, err := ssh.NewPublicKey(private.Signer().Public())
	if err != nil {
		return runKey{}, fmt.Errorf("derive DigitalOcean SSH public key")
	}
	return runKey{private: private, public: strings.TrimSpace(string(ssh.MarshalAuthorizedKey(public))), fingerprint: ssh.FingerprintLegacyMD5(public), name: tag}, nil
}

func (c *Client) getKey(ctx context.Context, fingerprint string) (accountKey, error) {
	var response struct {
		Key accountKey `json:"ssh_key"`
	}
	err := c.request(ctx, http.MethodGet, "/account/keys/"+url.PathEscape(fingerprint), nil, &response)
	return response.Key, err
}

func verifyOwnedKey(actual accountKey, expected runKey) error {
	if actual.ID < 1 || actual.Name != expected.name || !strings.EqualFold(actual.Fingerprint, expected.fingerprint) {
		return fmt.Errorf("DigitalOcean SSH key ownership conflict")
	}
	actualFields, expectedFields := strings.Fields(actual.PublicKey), strings.Fields(expected.public)
	if len(actualFields) < 2 || len(expectedFields) < 2 || actualFields[0] != expectedFields[0] || actualFields[1] != expectedFields[1] {
		return fmt.Errorf("DigitalOcean SSH public key mismatch")
	}
	return nil
}

// ensureKey serializes registration across worker slots. The deterministic
// fingerprint reconciles registration after a timeout or service restart.
func (c *Client) ensureKey(ctx context.Context, runID string) (accountKey, *cloud.SSHKey, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	expected, err := c.derive(runID)
	if err != nil {
		return accountKey{}, nil, err
	}
	key, err := c.getKey(ctx, expected.fingerprint)
	if errors.Is(err, cloud.ErrNotFound) {
		var response struct {
			Key accountKey `json:"ssh_key"`
		}
		err = c.request(ctx, http.MethodPost, "/account/keys", map[string]string{"name": expected.name, "public_key": expected.public}, &response)
		if err == nil {
			key = response.Key
		} else {
			// POST may have succeeded despite a lost response. Never repeat it.
			postErr := err
			key, err = c.getKey(ctx, expected.fingerprint)
			if errors.Is(err, cloud.ErrNotFound) {
				err = postErr
			}
		}
	}
	if err != nil {
		return accountKey{}, nil, fmt.Errorf("register DigitalOcean SSH key: %w", err)
	}
	if err := verifyOwnedKey(key, expected); err != nil {
		return accountKey{}, nil, err
	}
	return key, expected.private, nil
}

// FinalizeRun is called only after worker reconciliation. It performs its own
// fresh owned-worker listing before deleting the run's account SSH key.
func (c *Client) FinalizeRun(ctx context.Context, runID string) error {
	expected, err := c.derive(runID)
	if err != nil {
		return err
	}
	workers, err := c.List(ctx, runID)
	if err != nil {
		return err
	}
	if len(workers) != 0 {
		return fmt.Errorf("retaining DigitalOcean SSH key: %d owned Droplets remain", len(workers))
	}
	key, err := c.getKey(ctx, expected.fingerprint)
	if errors.Is(err, cloud.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := verifyOwnedKey(key, expected); err != nil {
		return err
	}
	if err := c.request(ctx, http.MethodDelete, "/account/keys/"+strconv.FormatInt(key.ID, 10), nil, nil); err != nil && !errors.Is(err, cloud.ErrNotFound) {
		return err
	}
	for {
		_, err = c.getKey(ctx, expected.fingerprint)
		if errors.Is(err, cloud.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := pause(ctx, time.Second); err != nil {
			return fmt.Errorf("DigitalOcean SSH key deletion unconfirmed: %w", err)
		}
	}
}
