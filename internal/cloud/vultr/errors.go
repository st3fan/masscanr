package vultr

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"unicode"

	"github.com/st3fan/masscanr/internal/cloud"
)

const maxErrorResponse = 64 << 10

// APIError keeps provider diagnostics in readable, redacted form. It contains
// no request headers, request payload, API key or raw unredacted response.
type APIError struct {
	Method, Path        string
	StatusCode          int
	RequestID, Response string
	Truncated           bool
	readErr             error
	readMessage         string
}

func (e *APIError) Error() string {
	var out strings.Builder
	fmt.Fprintf(&out, "Vultr API error: HTTP %d %s\n  request: %s %s", e.StatusCode, http.StatusText(e.StatusCode), e.Method, e.Path)
	if e.RequestID != "" {
		fmt.Fprintf(&out, "\n  request ID: %s", e.RequestID)
	}
	out.WriteString("\n  response:\n")
	if e.Response == "" {
		out.WriteString("    (empty response body)")
	} else {
		for i, line := range strings.Split(e.Response, "\n") {
			if i > 0 {
				out.WriteByte('\n')
			}
			out.WriteString("    " + line)
		}
	}
	if e.Truncated {
		fmt.Fprintf(&out, "\n    [response truncated at %d bytes]", maxErrorResponse)
	}
	if e.readErr != nil {
		fmt.Fprintf(&out, "\n  response read error: %s", e.readMessage)
	}
	return out.String()
}

func (e *APIError) Is(target error) bool {
	return e.StatusCode == http.StatusNotFound && target == cloud.ErrNotFound
}
func (e *APIError) Unwrap() error { return e.readErr }

func (c *Client) responseError(req *http.Request, res *http.Response) *APIError {
	// Read enough beyond the display limit to redact a known key straddling
	// that boundary before truncating the printable response.
	limit := maxErrorResponse + len(c.key)
	data, readErr := io.ReadAll(io.LimitReader(res.Body, int64(limit+1)))
	truncated := len(data) > limit
	if truncated {
		data = data[:limit]
	}
	response := c.readableResponse(data)
	if len(response) > maxErrorResponse {
		response = strings.ToValidUTF8(response[:maxErrorResponse], "�")
		truncated = true
	}
	requestID := ""
	for _, header := range []string{"X-Request-ID", "X-Vultr-Request-ID", "X-Correlation-ID", "CF-Ray"} {
		if value := res.Header.Get(header); value != "" {
			requestID = strings.Join(strings.Fields(c.redactKey(value)), " ")
			break
		}
	}
	readMessage := ""
	if readErr != nil {
		readMessage = c.readableResponse([]byte(readErr.Error()))
	}
	return &APIError{Method: req.Method, Path: c.redactKey(req.URL.EscapedPath()), StatusCode: res.StatusCode, RequestID: requestID, Response: response, Truncated: truncated, readErr: readErr, readMessage: readMessage}
}

func (c *Client) redactKey(s string) string {
	if c.key != "" {
		s = strings.ReplaceAll(s, c.key, "[REDACTED]")
	}
	return s
}

func sensitiveField(name string) bool {
	name = strings.ToLower(strings.Map(func(r rune) rune {
		if r == '_' || r == '-' || r == ' ' {
			return -1
		}
		return r
	}, name))
	switch name {
	case "apikey", "authorization", "password", "defaultpassword", "privatekey", "sshprivatekey", "secret", "clientsecret", "token", "accesstoken", "refreshtoken", "credentials", "userdata":
		return true
	}
	return false
}

func (c *Client) redactJSON(value any) any {
	switch v := value.(type) {
	case map[string]any:
		for name, child := range v {
			if sensitiveField(name) {
				v[name] = "[REDACTED]"
			} else {
				v[name] = c.redactJSON(child)
			}
		}
	case []any:
		for i, child := range v {
			v[i] = c.redactJSON(child)
		}
	case string:
		return c.redactKey(v)
	}
	return value
}

var secretAssignment = regexp.MustCompile(`(?i)(["']?(?:default[_ -]?password|password|api[_ -]?key|access[_ -]?token|refresh[_ -]?token|token|authorization|private[_ -]?key|client[_ -]?secret|secret)["']?\s*[:=]\s*)(?:"(?:\\.|[^"\\])*"?|'[^']*'?|[^\r\n,;}\]]+)`)

func (c *Client) readableResponse(data []byte) string {
	var value any
	var out bytes.Buffer
	if json.Unmarshal(data, &value) == nil {
		encoder := json.NewEncoder(&out)
		encoder.SetIndent("", "  ")
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(c.redactJSON(value)); err == nil {
			data = bytes.TrimSpace(out.Bytes())
		}
	} else {
		data = []byte(c.redactKey(secretAssignment.ReplaceAllString(string(data), "${1}[REDACTED]")))
	}
	text := c.redactKey(string(data))
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || unicode.IsPrint(r) {
			return r
		}
		return '�'
	}, text))
}
