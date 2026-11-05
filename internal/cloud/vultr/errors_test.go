package vultr

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/st3fan/masscanr/internal/cloud"
)

func TestReadableAPIErrorIncludesResponseAndCreateContext(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 429, 500, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			calls := 0
			client := fixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("X-Vultr-Request-ID", "request-example")
				w.WriteHeader(status)
				fmt.Fprintf(w, `{"error":"Instance request refused","status":%d,"details":{"field":"plan","allowed_instances":5}}`, status)
			})
			client.osID = 999
			_, err := client.Create(t.Context(), cloud.Spec{RunID: testRun, Shard: 5, Region: "yto", InstanceType: "vx1-g-2c-8g"})
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != status || apiErr.RequestID != "request-example" || calls != 1 {
				t.Fatal(err, calls)
			}
			for _, text := range []string{fmt.Sprintf("HTTP %d %s", status, http.StatusText(status)), "request: POST /instances", "request ID: request-example", "region=yto plan=vx1-g-2c-8g os_id=999", "Instance request refused", "\n  response:\n    {", "\"allowed_instances\": 5"} {
				if !strings.Contains(err.Error(), text) {
					t.Error("missing diagnostic", text, err)
				}
			}
			if errors.Is(err, cloud.ErrNotFound) != (status == 404) {
				t.Fatal("not-found contract changed", err)
			}
		})
	}
}

func TestAPIErrorRedactsJSONSecretsAndEscapedAPIKey(t *testing.T) {
	client := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		fmt.Fprint(w, `{"error":"echo test\u002dsecret","api_key":"different-api-value","nested":[{"default_password":"worker-password","Authorization":"Bearer other-value","private_key":"private-material"}],"user_data":"secret script","remaining":5}`)
	})
	err := client.request(t.Context(), "POST", "/instances", nil, nil)
	if err == nil {
		t.Fatal("missing error")
	}
	for _, secret := range []string{"test-secret", "different-api-value", "worker-password", "other-value", "private-material", "secret script"} {
		if strings.Contains(err.Error(), secret) {
			t.Error("credential leaked", secret)
		}
	}
	if !strings.Contains(err.Error(), "[REDACTED]") || !strings.Contains(err.Error(), "\"remaining\": 5") {
		t.Fatal(err)
	}
}

func TestAPIErrorPreservesTextAndRedactsAssignments(t *testing.T) {
	client := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		fmt.Fprint(w, "Account limit reached.\npassword=hidden password words\napi_key: test-secret\n\x1b[31mprovider detail")
	})
	err := client.request(t.Context(), "POST", "/instances", nil, nil)
	if err == nil {
		t.Fatal("missing error")
	}
	for _, secret := range []string{"test-secret", "hidden password words", "\x1b"} {
		if strings.Contains(err.Error(), secret) {
			t.Error("unsafe output", secret)
		}
	}
	if !strings.Contains(err.Error(), "Account limit reached.") || !strings.Contains(err.Error(), "provider detail") {
		t.Fatal(err)
	}
}

func TestAPIErrorBoundsResponseAndPreservesReadFailure(t *testing.T) {
	client := New("test-secret")
	req, _ := http.NewRequest("POST", "https://api.vultr.com/v2/instances", nil)
	response := &http.Response{StatusCode: 400, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(strings.Repeat("x", maxErrorResponse-5) + "test-secret" + strings.Repeat("x", 100)))}
	err := client.responseError(req, response)
	if !err.Truncated || len(err.Response) > maxErrorResponse || strings.Contains(err.Error(), "test-secret") || !strings.Contains(err.Error(), "response truncated") {
		t.Fatal("response limit/redaction failed")
	}
	failure := errors.New("read failed test-secret")
	response.Body = io.NopCloser(io.MultiReader(strings.NewReader("upstream failure"), iotest.ErrReader(failure)))
	err = client.responseError(req, response)
	if !errors.Is(err, failure) || !strings.Contains(err.Error(), "upstream failure") || !strings.Contains(err.Error(), "response read error") || strings.Contains(err.Error(), "test-secret") {
		t.Fatal(err)
	}
	response.Body = io.NopCloser(strings.NewReader(""))
	if err = client.responseError(req, response); !strings.Contains(err.Error(), "empty response body") {
		t.Fatal(err)
	}
}

func TestTransportCancellationRetainsEndpointAndCause(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	client := fixture(t, func(w http.ResponseWriter, r *http.Request) { t.Error("cancelled request reached local server") })
	client.BaseURL += "/v2"
	err := client.request(ctx, "GET", "/instances", nil, nil)
	if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "GET /v2/instances") {
		t.Fatal(err)
	}
}

func TestAvailabilityExplainsCatalogVersusRegionalResponse(t *testing.T) {
	for _, inCatalog := range []bool{false, true} {
		t.Run(fmt.Sprintf("inCatalog=%v", inCatalog), func(t *testing.T) {
			client := fixture(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/instances":
					if r.Method != "GET" {
						t.Error("unexpected provisioning")
					}
					fmt.Fprint(w, `{"instances":[]}`)
				case "/regions/yto/availability":
					fmt.Fprint(w, `{"available_plans":["vc2-1c-1gb"]}`)
				case "/plans":
					if inCatalog {
						fmt.Fprint(w, `{"plans":[{"id":"vx1-g-2c-8g","disk":0}]}`)
					} else {
						fmt.Fprint(w, `{"plans":[]}`)
					}
				default:
					t.Error("unexpected request", r.URL)
					http.NotFound(w, r)
				}
			})
			err := client.Validate(t.Context(), cloud.Spec{RunID: testRun, Region: "yto", InstanceType: "vx1-g-2c-8g"})
			if err == nil {
				t.Fatal("missing availability error")
			}
			if inCatalog {
				for _, text := range []string{"exists in Vultr's catalog", "current regional availability response", "/v2/regions/yto/availability", "1 plans returned", "list-instance-types --region yto"} {
					if !strings.Contains(err.Error(), text) {
						t.Error(text, err)
					}
				}
			} else if !strings.Contains(err.Error(), "not found in Vultr's global plan catalog") {
				t.Fatal(err)
			}
		})
	}
}

func ExampleAPIError() {
	err := &APIError{Method: "POST", Path: "/v2/instances", StatusCode: 400, RequestID: "request-example", Response: "{\n  \"error\": \"Provider's explanation\",\n  \"status\": 400\n}"}
	fmt.Println("masscanr:", fmt.Errorf("create shard 5: %w", err))
	// Output:
	// masscanr: create shard 5: Vultr API error: HTTP 400 Bad Request
	//   request: POST /v2/instances
	//   request ID: request-example
	//   response:
	//     {
	//       "error": "Provider's explanation",
	//       "status": 400
	//     }
}
