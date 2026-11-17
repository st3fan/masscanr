package cloud

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestSSHKeyIsNotFormattedOrSerialized(t *testing.T) {
	seed := bytes.Repeat([]byte{0x42}, ed25519.SeedSize)
	key, err := NewSSHKey(ed25519.NewKeyFromSeed(seed))
	if err != nil {
		t.Fatal(err)
	}
	w := Worker{ID: "fixture", Key: key}
	formatted := fmt.Sprintf("%v %+v %#v", w, w, w)
	encoded, err := json.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(formatted, "[REDACTED]") || strings.Contains(formatted, fmt.Sprint([]byte(key.private))) || strings.Contains(string(encoded), "Key") || !w.HasAuth() {
		t.Fatal("SSH key was not redacted")
	}
}
