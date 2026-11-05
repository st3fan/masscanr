package vultr

import (
	"net/url"
	"testing"
)

func TestOpaqueCursorKeepsEndpointAndOwnershipFilter(t *testing.T) {
	const path = "/instances?per_page=100&tag=masscanr-run-example&cursor=previous"
	for _, cursor := range []string{"bmV4dF9f+/==", "https://untrusted.invalid/other?cursor=token&tag=wrong", "%+/?=&opaque"} {
		next, err := nextPage(path, cursor)
		if err != nil {
			t.Fatal(err)
		}
		u, err := url.Parse(next)
		if err != nil {
			t.Fatal(err)
		}
		if u.IsAbs() || u.Host != "" || u.Path != "/instances" || u.Query().Get("cursor") != cursor || u.Query().Get("tag") != "masscanr-run-example" || u.Query().Get("per_page") != "100" {
			t.Fatal("cursor changed endpoint, ownership or token", next)
		}
	}
	if next, err := nextPage(path, ""); err != nil || next != "" {
		t.Fatal(next, err)
	}
	if _, err := nextPage(path, "previous"); err == nil {
		t.Fatal("accepted non-advancing cursor")
	}
}
