package vultr

import (
	"fmt"
	"net/url"
)

type pageMeta struct {
	Links struct {
		Next string `json:"next"`
	} `json:"links"`
}

// nextPage passes meta.links.next unchanged as an opaque cursor, matching
// govultr's ListOptions.Cursor contract. Only our current query is updated;
// response data can never change the API origin, endpoint or ownership filter.
func nextPage(path, cursor string) (string, error) {
	if cursor == "" {
		return "", nil
	}
	pinned, err := url.Parse(path)
	if err != nil {
		return "", err
	}
	query := pinned.Query()
	if query.Get("cursor") == cursor {
		return "", fmt.Errorf("Vultr pagination cursor did not advance")
	}
	query.Set("cursor", cursor)
	pinned.RawQuery = query.Encode()
	return pinned.String(), nil
}
