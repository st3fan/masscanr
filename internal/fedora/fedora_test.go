package fedora

import "testing"

func TestCheckRelease(t *testing.T) {
	for _, tc := range []struct {
		text string
		ok   bool
	}{
		{`NAME="Fedora Linux"
ID=fedora
VERSION_ID=44
`, true},
		{`ID="fedora"
VERSION_ID="44"
`, true},
		{`ID=centos
VERSION_ID=10
`, false},
		{`ID=fedora
VERSION_ID=45
`, false},
		{`ID=fedora
`, false},
	} {
		if err := CheckRelease(tc.text); (err == nil) != tc.ok {
			t.Fatalf("release %q: %v", tc.text, err)
		}
	}
}
