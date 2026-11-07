package backend

import (
	"encoding/hex"
	"fmt"
	"strings"
)

// BinaryBuild combines the signed package identity and observed executable
// digest. Both are compared across workers before the scan barrier opens.
func BinaryBuild(installed, checksum string) (string, error) {
	fields := strings.Fields(checksum)
	if len(fields) != 2 || len(fields[0]) != 64 {
		return "", fmt.Errorf("invalid executable checksum")
	}
	if _, err := hex.DecodeString(fields[0]); err != nil {
		return "", fmt.Errorf("invalid executable checksum")
	}
	return installed + " sha256:" + fields[0], nil
}
