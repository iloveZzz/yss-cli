package tracker

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

type readCancellation struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b readCancellation) Read(p []byte) (int, error) {
	b.cancel()
	return b.ReadCloser.Read(p)
}

type cancellationTransport struct {
	method string
	cancel context.CancelFunc
}

func (rt cancellationTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err == nil && req.Method == rt.method {
		resp.Body = readCancellation{resp.Body, rt.cancel}
	}
	return resp, err
}

func TestResponseBodyCancellationKeepsCauseAndUnknownWrite(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		t.Run(method, func(t *testing.T) {
			root := project(t, true)
			gh := &fakeGitHub{labels: map[string][]string{"acme/app/issues/12": {"needs-triage"}}, updated: map[string]string{"acme/app/issues/12": "a"}}
			armed, writes := false, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					writes++
				}
				if armed && r.Method == method {
					w.WriteHeader(http.StatusOK)
					w.(http.Flusher).Flush()
					<-r.Context().Done()
					return
				}
				gh.ServeHTTP(w, r)
			}))
			defer server.Close()
			e := env(server)
			if _, err := Run(context.Background(), "link", root, map[string]string{"ticket": ".work/feat/parent-ticket.md", "issue": "acme/app#12", "apply": "true"}, Env{}); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(t.TempDir(), "plan.json")
			if _, err := Run(context.Background(), "sync", root, map[string]string{"plan": "true", "out": file}, e); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			client, err := NewClient(e.getenv)
			if err != nil {
				t.Fatal(err)
			}
			client.HTTP.Transport = cancellationTransport{method, cancel}
			armed = true
			out, err := syncApply(ctx, root, map[string]string{"plan-file": file, "confirm-remote": "github"}, client, e)
			if code(err) != "CANCELLED" || !errors.Is(err, context.Canceled) {
				t.Fatalf("body cancellation must preserve its code and cause: %v", err)
			}
			if method == http.MethodPost {
				result, ok := out.(map[string]any)
				if !ok || result["remote_state"] != "unknown" || result["local_recorded"] != false || writes != 1 || !writeUnknown(err) {
					t.Fatalf("cancelled write must retain unknown receipt without replay: %v %v writes=%d", out, err, writes)
				}
			} else if writes != 0 {
				t.Fatal("cancelled read must not write")
			}
		})
	}
}
