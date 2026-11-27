package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const config = `[{"id":"demo","uriEnv":"GATEWAY_TEST_URI","databases":{"analytics":["orders"]}}]`

func TestGatewayHTTP(t *testing.T) {
	// A deliberately unreachable host makes these checks independent of a live database.
	t.Setenv("GATEWAY_TEST_URI", "mongodb://user:private-pass@127.0.0.1:1/?serverSelectionTimeoutMS=30")
	srv, err := Open(config, "v0.1.0", "fixture")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close(context.Background()) })
	for _, tc := range []struct {
		method, path, body string
		status             int
		want               string
	}{
		{"GET", "/health", "", 200, `{"status":"ok"}`},
		{"GET", "/version", "", 200, `{"version":"v0.1.0","revision":"fixture"}`},
		{"GET", "/api/v1/connections", "", 200, `{"connections":["demo"]}`},
		{"GET", "/api/v1/connections?uri=invalid", "", 400, ""},
		{"GET", "/api/v1/connections/demo/databases", "", 504, ""},
		{"GET", "/api/v1/connections/other/databases", "", 404, ""},
		{"GET", "/api/v1/collections?connectionId=demo&database=secrets", "", 404, ""},
		{"GET", "/api/v1/collections?connectionId=demo&database=analytics", "", 504, ""},
		{"GET", "/api/v1/collections?connectionId=demo", "", 400, ""},
		{"GET", "/api/v1/collections?connectionId=demo&database=analytics&database=x", "", 400, ""},
		{"POST", "/api/v1/schema", `{}`, 400, ""},
		{"POST", "/api/v1/schema", `{"connectionId":"demo","database":"other","collection":"orders"}`, 404, ""},
		{"POST", "/api/v1/query", `{"connectionId":"demo","database":"analytics","collection":"secrets"}`, 404, ""},
		{"POST", "/api/v1/query", `{"connectionId":"demo","database":"analytics","collection":"orders","uri":"mongodb://other"}`, 400, ""},
		{"POST", "/api/v1/query", `{"connectionId":"demo","connectionId":"unknown","database":"analytics","collection":"orders"}`, 400, ""},
		{"POST", "/api/v1/query", `{"connectionId":"demo","Database":"analytics","collection":"orders"}`, 400, ""},
		{"POST", "/api/v1/query", `{"connectionId":"demo","database":"analytics","collection":"orders","pipeline":"[]"}`, 400, ""},
		{"POST", "/api/v1/query", `{"connectionId":"demo","database":"analytics","collection":"orders"}`, 400, ""},
		{"POST", "/api/v1/schema", `{"connectionId":"demo","database":"analytics","collection":"orders","pipeline":[` +
			strings.Repeat(`{"$limit":1},`, 32) + `{"$limit":1}]}`, 413, ""},
		{"POST", "/api/v1/query", `{"connectionId":"demo","database":"analytics","collection":"orders"}{}`, 400, ""},
		{"POST", "/api/v1/query", strings.Repeat(" ", 300<<10) + "{}", 413, ""},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("got %d %s, want %d", w.Code, w.Body.String(), tc.status)
			}
			var got any
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if tc.want != "" {
				var want any
				if err := json.Unmarshal([]byte(tc.want), &want); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("got %v, want %v", got, want)
				}
			} else if tc.status >= 400 {
				var envelope struct {
					Error struct{ Code, Message string }
				}
				if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil || envelope.Error.Code == "" || envelope.Error.Message == "" {
					t.Fatalf("missing structured error: %s", w.Body.String())
				}
			}
			for _, secret := range []string{"private-pass", "127.0.0.1", "GATEWAY_TEST_URI"} {
				if strings.Contains(w.Body.String(), secret) {
					t.Fatalf("response exposed private connection details")
				}
			}
		})
	}
}

func TestInvalidURIErrorHidesValue(t *testing.T) {
	t.Setenv("GATEWAY_TEST_URI", "mongodb://private-pass@host:invalid")
	if _, err := Open(config, "dev", "unknown"); err == nil || strings.Contains(err.Error(), "private-pass") {
		t.Fatalf("expected sanitized configuration error, got %v", err)
	}
}

func TestOperationTimeoutCanExpireBeforeHTTPDeadline(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
	}{
		{context.DeadlineExceeded, 504},
		{errors.New("upstream authentication failure with private details"), 502},
	} {
		w := httptest.NewRecorder()
		operationError(w, context.Background(), tc.err)
		if w.Code != tc.status || strings.Contains(w.Body.String(), "private details") {
			t.Fatalf("unexpected upstream response: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestDiscoveryOmitsWireDeadlineAndKeepsRequestCancellation(t *testing.T) {
	parent, cancel := context.WithTimeout(context.Background(), time.Minute)
	ctx := discoveryContext{parent}
	if _, ok := ctx.Deadline(); ok {
		t.Fatal("Go driver would send unsupported maxTimeMS with discovery")
	}
	cancel()
	select {
	case <-ctx.Done():
		if !errors.Is(ctx.Err(), context.Canceled) {
			t.Fatal(ctx.Err())
		}
	default:
		t.Fatal("discovery outlived canceled caller")
	}
}

func TestDriverLogsNeverExposeConnectionOrCommandDetails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "driver.log")
	t.Setenv("MONGODB_LOG_ALL", "debug")
	t.Setenv("MONGODB_LOG_PATH", path)
	t.Setenv("GATEWAY_TEST_URI", "mongodb://user:private-pass@127.0.0.1:1/?serverSelectionTimeoutMS=30")
	srv, err := Open(config, "dev", "unknown")
	if err != nil {
		t.Fatal(err)
	}
	srv.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/v1/connections/demo/databases", nil))
	if err := srv.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(path); err == nil && len(data) != 0 {
		t.Fatal("driver logged private connection metadata")
	} else if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func TestInvalidConfigFailsClosed(t *testing.T) {
	t.Setenv("GATEWAY_TEST_URI", "mongodb://127.0.0.1:1")
	for _, raw := range []string{
		"", "null", "[]", config + " null",
		strings.Replace(config, `"uriEnv"`, `"uri"`, 1),
		strings.Replace(config, "GATEWAY_TEST_URI", "MISSING_URI_123", 1),
		strings.Replace(config, "orders", "", 1),
		strings.Replace(config, `["orders"]`, "null", 1),
		strings.Replace(config, "demo", "private/pass", 1),
		strings.TrimSuffix(config, "]") + "," + strings.TrimPrefix(config, "["),
	} {
		srv, err := Open(raw, "dev", "unknown")
		if err == nil {
			srv.Close(context.Background())
			t.Fatalf("accepted invalid config %q", raw)
		}
	}
}

func TestDateRangeEnvelope(t *testing.T) {
	const prefix = `{"connectionId":"demo","database":"analytics","collection":"orders","fields":["amount"],"dateRange":`
	for _, raw := range []string{
		`null`, `{}`, `[]`,
		`{"Field":"x","startDate":"2026-01-01","endDate":"2026-01-01"}`,
		`{"field":"x","field":"y","startDate":"2026-01-01","endDate":"2026-01-01"}`,
	} {
		if _, err := parseReport([]byte(prefix + raw + "}")); err == nil {
			t.Fatalf("accepted invalid date range: %s", raw)
		}
	}
	req, err := parseReport([]byte(prefix + `{"field":"x","startDate":"2026-01-01","endDate":"2026-01-01"}}`))
	if err != nil || req.dateRange == nil || req.dateRange.StartDate != "2026-01-01" {
		t.Fatal("lost date range in request")
	}
}
