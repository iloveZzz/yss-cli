package updater

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/domain"
)

type releaseRoundTripFunc func(*http.Request) (*http.Response, error)

func (f releaseRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestUpgradeHTTP403Diagnostics(t *testing.T) {
	const source = "https://api.github.com/repos/iloveZzz/yss-cli/releases/tags/v2.7.1"
	const secret = "response-credential-must-not-be-recorded"
	requests := 0
	client := UpgradeClient{HTTPClient: &http.Client{Transport: releaseRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		if req.Method != http.MethodGet || req.URL.String() != source || req.Header.Get("Authorization") != "" {
			t.Fatalf("unexpected release request: %s %s", req.Method, req.URL)
		}
		return &http.Response{StatusCode: http.StatusForbidden, Header: http.Header{"Authorization": {secret}}, Body: io.NopCloser(strings.NewReader(secret))}, nil
	})}}
	_, err := client.Run(context.Background(), UpgradeRequest{Check: true, To: "2.7.1"})
	var failure *domain.Error
	if !errors.As(err, &failure) || failure.Code != "NETWORK" || failure.Exit != 1 {
		t.Fatalf("HTTP failure contract: %#v", err)
	}
	var diagnostic interface{ DiagnosticDetail() domain.ErrorDetail }
	if !errors.As(err, &diagnostic) {
		t.Fatalf("missing diagnostic detail: %v", err)
	}
	detail := diagnostic.DiagnosticDetail()
	wantContext := map[string]any{"source": source, "phase": "request", "httpStatus": http.StatusForbidden}
	if detail.ID != "RELEASE_HTTP_STATUS" || !reflect.DeepEqual(detail.Context, wantContext) {
		t.Fatalf("HTTP diagnostic: %#v", detail)
	}
	encoded, marshalErr := json.Marshal(detail)
	if marshalErr != nil || strings.Contains(string(encoded), secret) || strings.Contains(err.Error(), secret) {
		t.Fatalf("response credentials entered diagnostics: %s, %v", encoded, marshalErr)
	}
	if requests != 1 {
		t.Fatalf("HTTP failure retried: %d requests", requests)
	}
}

func TestUpgradeHTTPRateLimitHeaders(t *testing.T) {
	for _, safe := range []bool{true, false} {
		headers := http.Header{"Authorization": {"secret"}, "Set-Cookie": {"secret"}}
		if safe {
			headers.Set("X-RateLimit-Limit", "60")
			headers.Set("X-RateLimit-Remaining", "0")
			headers.Set("X-RateLimit-Reset", "1793099999")
			headers.Set("Retry-After", "30")
			headers.Set("X-GitHub-Request-Id", "ABC1:002F")
		} else {
			headers.Set("X-RateLimit-Remaining", "secret")
			headers.Set("X-GitHub-Request-Id", "response-secret")
		}
		client := UpgradeClient{HTTPClient: &http.Client{Transport: releaseRoundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusForbidden, Header: headers, Body: io.NopCloser(strings.NewReader("response-secret"))}, nil
		})}}
		_, err := client.get(context.Background(), githubAPI+"tags/v2.7.1")
		var diagnostic interface{ DiagnosticDetail() domain.ErrorDetail }
		if !errors.As(err, &diagnostic) {
			t.Fatal(err)
		}
		ctx := diagnostic.DiagnosticDetail().Context
		if safe && (ctx["githubRateLimit"] != "60" || ctx["githubRateRemaining"] != "0" || ctx["githubRateReset"] != "1793099999" || ctx["retryAfter"] != "30" || ctx["githubRequestId"] != "ABC1:002F") {
			t.Fatalf("missing bounded headers: %+v", ctx)
		}
		if !safe && len(ctx) != 3 {
			t.Fatalf("untrusted headers retained: %+v", ctx)
		}
		raw, _ := json.Marshal(ctx)
		if strings.Contains(string(raw), "secret") {
			t.Fatalf("response credentials leaked: %s", raw)
		}
	}
}
