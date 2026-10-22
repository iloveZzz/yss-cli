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
