package updater

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
)

type fixtureTransport struct {
	base      *url.URL
	transport http.RoundTripper
}

const onlineFixtureVersion = "1.4.0"

// These package bytes model a stable archive for parser/transaction tests.
// They are fixtures, not a native release qualification receipt.
func onlinePackage(t *testing.T) (UpgradeClient, func(map[string]any, map[string]any), []byte) {
	t.Helper()
	var fields map[string]any
	file, sha := stableProofFixture(t, func(m map[string]any) {
		m["cliVersion"] = onlineFixtureVersion
		m["cliCommit"] = strings.Repeat("1", 40)
		m["nativeReceiptSha256"] = strings.Repeat("6", 64)
		m["releaseGateSha256"] = strings.Repeat("3", 64)
		fields = m
	})
	body, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	customize := func(_ map[string]any, p map[string]any) {
		p["sourceLockSha256"] = fields["sourceLockSha256"]
		a := p["artifacts"].([]map[string]any)[0]
		a["sha256"] = sha
		a["bytes"] = len(body)
		a["binarySha256"] = fields["binarySha256"]
	}
	return onlineFixture(t, customize, body), customize, body
}

func TestOnlineUpgradeInstallsVerifiedBytesAndUsesExistingRollback(t *testing.T) {
	c, customize, _ := onlinePackage(t)
	tool := filepath.Join(root(t), "tools")
	r, err := c.Run(context.Background(), UpgradeRequest{ToolRoot: tool})
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "installed" || r.Transaction == nil || r.Transaction.Kind != "program-update" {
		t.Fatalf("upgrade did not use program transaction: %+v", r)
	}
	b, err := os.ReadFile(filepath.Join(tool, fileName()))
	if err != nil || string(b) != "native-binary" {
		t.Fatalf("installed bytes: %q %v", b, err)
	}
	current := onlineFixture(t, customize, nil)
	r, err = current.Run(context.Background(), UpgradeRequest{ToolRoot: tool})
	if err != nil || r.Status != "unchanged" || r.Transaction != nil {
		t.Fatalf("already latest downloaded or wrote: %+v %v", r, err)
	}
	if _, err = Rollback(context.Background(), tool); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(tool, fileName())); !os.IsNotExist(err) {
		t.Fatal("program rollback failed")
	}
}

func (t fixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	copy := r.Clone(r.Context())
	u := *r.URL
	u.Scheme = t.base.Scheme
	u.Host = t.base.Host
	copy.URL = &u
	return t.transport.RoundTrip(copy)
}

func onlineFixture(t *testing.T, customize func(map[string]any, map[string]any), archive []byte) UpgradeClient {
	t.Helper()
	version := onlineFixtureVersion
	platform := runtime.GOOS + "/" + runtime.GOARCH
	ext := ".tar.gz"
	if runtime.GOOS == "windows" {
		ext = ".zip"
	}
	name := "yss_" + version + "_" + runtime.GOOS + "_" + runtime.GOARCH + ext
	base := "https://github.com/iloveZzz/yss-cli/releases/download/v" + version + "/"
	release := map[string]any{"tag_name": "v" + version, "draft": false, "prerelease": false, "html_url": "https://github.com/iloveZzz/yss-cli/releases/tag/v" + version, "assets": []map[string]any{{"name": "checksums.json", "browser_download_url": base + "checksums.json"}, {"name": name, "browser_download_url": base + name}}}
	proof := map[string]any{"schemaVersion": 2, "version": version, "cliCommit": strings.Repeat("1", 40), "sourceState": "committed", "stableReady": true, "pending": []string{}, "sourceLockSha256": strings.Repeat("2", 64), "releaseGateSha256": strings.Repeat("3", 64), "requiredPlatforms": []string{platform}, "artifacts": []map[string]any{{"platform": platform, "archive": name, "sha256": strings.Repeat("4", 64), "bytes": 4096, "nativeRuntimeVerified": true, "binarySha256": strings.Repeat("5", 64), "nativeReceiptSha256": strings.Repeat("6", 64)}}}
	if customize != nil {
		customize(release, proof)
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/repos/iloveZzz/yss-cli/releases/"):
			json.NewEncoder(w).Encode(release)
		case strings.HasSuffix(r.URL.Path, "/checksums.json"):
			json.NewEncoder(w).Encode(proof)
		default:
			if archive == nil {
				t.Errorf("--check downloaded archive: %s", r.URL.Path)
				http.Error(w, "unexpected download", 500)
				return
			}
			w.Write(archive)
		}
	}))
	t.Cleanup(s.Close)
	u, _ := url.Parse(s.URL)
	return UpgradeClient{HTTPClient: &http.Client{Transport: fixtureTransport{u, http.DefaultTransport}}}
}

func TestOnlineCheckShowsLatestNativeReleaseWithoutWrites(t *testing.T) {
	c := onlineFixture(t, nil, nil)
	tool := filepath.Join(root(t), "absent-installation")
	r, err := c.Run(context.Background(), UpgradeRequest{Check: true, ToolRoot: tool})
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "checked" || r.TargetVersion != onlineFixtureVersion || !r.UpdateAvailable || r.Platform != runtime.GOOS+"/"+runtime.GOARCH {
		t.Fatalf("unexpected check result: %+v", r)
	}
	if _, err := os.Stat(tool); !os.IsNotExist(err) {
		t.Fatal("check created installation or transaction")
	}
	if domain.Version == "" {
		t.Fatal("missing caller identity")
	}
}

func TestOnlineReleaseRefusesIncompleteQualificationAndUntrustedAssets(t *testing.T) {
	for name, change := range map[string]func(map[string]any, map[string]any){
		"draft":           func(r, p map[string]any) { r["draft"] = true },
		"prerelease":      func(r, p map[string]any) { r["prerelease"] = true },
		"missing-package": func(r, p map[string]any) { r["assets"] = r["assets"].([]map[string]any)[:1] },
		"untrusted-url": func(r, p map[string]any) {
			r["assets"].([]map[string]any)[0]["browser_download_url"] = "https://other.example/checksums.json"
		},
		"pending":                func(r, p map[string]any) { p["pending"] = []string{"native-smoke"} },
		"not-stable":             func(r, p map[string]any) { p["stableReady"] = false },
		"version-mismatch":       func(r, p map[string]any) { p["version"] = "1.1.0" },
		"wrong-platform":         func(r, p map[string]any) { p["artifacts"].([]map[string]any)[0]["platform"] = "unknown/arch" },
		"wrong-checksum":         func(r, p map[string]any) { p["artifacts"].([]map[string]any)[0]["sha256"] = "not-a-sha" },
		"oversize":               func(r, p map[string]any) { p["artifacts"].([]map[string]any)[0]["bytes"] = maxArchive + 1 },
		"not-qualified-platform": func(r, p map[string]any) { p["requiredPlatforms"] = []string{"unknown/arch"} },
	} {
		t.Run(name, func(t *testing.T) {
			c := onlineFixture(t, change, nil)
			if _, err := c.Run(context.Background(), UpgradeRequest{Check: true}); updateErrorCode(err) != "ARTIFACT" {
				t.Fatalf("wanted ARTIFACT refusal, got %v", err)
			}
		})
	}
}

func TestOnlineDownloadRefusesWrongBytesAndInconsistentPackageIdentity(t *testing.T) {
	_, customize, body := onlinePackage(t)
	for _, kind := range []string{"digest", "length", "manifest-version"} {
		t.Run(kind, func(t *testing.T) {
			payload := append([]byte(nil), body...)
			mutate := func(r, p map[string]any) {
				customize(r, p)
				if kind == "manifest-version" {
					p["cliCommit"] = strings.Repeat("9", 40)
				}
			}
			if kind == "digest" {
				payload[0] ^= 1
			}
			if kind == "length" {
				payload = payload[:len(payload)-1]
			}
			c := onlineFixture(t, mutate, payload)
			tool := filepath.Join(root(t), "tools")
			_, err := c.Run(context.Background(), UpgradeRequest{ToolRoot: tool})
			want := "ARTIFACT"
			if kind == "digest" {
				want = "DIGEST"
			}
			if updateErrorCode(err) != want {
				t.Fatalf("wanted %s, got %v", want, err)
			}
			if _, err = os.Stat(tool); !os.IsNotExist(err) {
				t.Fatal("rejected upgrade wrote installation")
			}
		})
	}
}

func TestOnlineNetworkFailureAndCancellationAreExplicit(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !cancelled {
				http.Error(w, "network unavailable", 503)
				return
			}
			<-r.Context().Done()
		}))
		u, _ := url.Parse(s.URL)
		c := UpgradeClient{HTTPClient: &http.Client{Transport: fixtureTransport{u, http.DefaultTransport}}}
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		_, err := c.Run(ctx, UpgradeRequest{Check: true})
		cancel()
		s.Close()
		want := "NETWORK"
		if cancelled {
			want = "CANCELLED"
		}
		if updateErrorCode(err) != want {
			t.Fatalf("wanted %s, got %v", want, err)
		}
	}
}

func TestOnlineAutomaticRootResolvesSymlinksAndRejectsUnmanagedCopies(t *testing.T) {
	c := onlineFixture(t, nil, nil)
	copyPath := filepath.Join(root(t), fileName())
	if err := os.WriteFile(copyPath, []byte("bare copy"), 0755); err != nil {
		t.Fatal(err)
	}
	c.Executable = func() (string, error) { return copyPath, nil }
	if _, err := c.Run(context.Background(), UpgradeRequest{}); updateErrorCode(err) != "INSTALLATION" {
		t.Fatalf("wanted unmanaged installation refusal, got %v", err)
	}
	c, customize, _ := onlinePackage(t)
	tool := filepath.Join(root(t), "tool")
	if _, err := c.Run(context.Background(), UpgradeRequest{ToolRoot: tool}); err != nil {
		t.Fatal(err)
	}
	// Match the caller version to a fixture installation for OS-root inference.
	// Source binding is verified separately by real fixed-binary installation.
	b, err := os.ReadFile(filepath.Join(tool, receiptRef))
	if err != nil {
		t.Fatal(err)
	}
	var r receipt
	if err = json.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	r.CLIVersion = domain.Version
	if err = os.WriteFile(filepath.Join(tool, receiptRef), encode(r), 0644); err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(filepath.Join(tool, "release-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m Manifest
	if err = json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	m.CLIVersion = domain.Version
	if err = os.WriteFile(filepath.Join(tool, "release-manifest.json"), encode(m), 0644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root(t), fileName())
	if err = os.Symlink(filepath.Join(tool, fileName()), link); err != nil {
		t.Skip("symlink unavailable: " + err.Error())
	}
	c = onlineFixture(t, customize, []byte("wrong body must be rejected"))
	c.Executable = func() (string, error) { return link, nil }
	res, err := c.Run(context.Background(), UpgradeRequest{})
	if res.ToolRoot != tool || updateErrorCode(err) != "ARTIFACT" {
		t.Fatalf("symlink root was not resolved: %+v %v", res, err)
	}
	if actual, err := os.Readlink(link); err != nil || actual != filepath.Join(tool, fileName()) {
		t.Fatal("upgrade changed PATH link")
	}
}

func TestOnlineUpgradeRefusesCustomizationAndDowngrade(t *testing.T) {
	c, _, _ := onlinePackage(t)
	tool := filepath.Join(root(t), "tools")
	if _, err := c.Run(context.Background(), UpgradeRequest{ToolRoot: tool}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tool, "README.md"), []byte("user change"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Run(context.Background(), UpgradeRequest{ToolRoot: tool}); updateErrorCode(err) != "CONFLICT" {
		t.Fatalf("wanted conflict refusal, got %v", err)
	}
	if err := os.WriteFile(filepath.Join(tool, "README.md"), []byte("readme"), 0644); err != nil {
		t.Fatal(err)
	}
	c = onlineFixture(t, func(r, p map[string]any) {
		r["tag_name"] = "v1.1.0"
		r["html_url"] = releaseBase + "tag/v1.1.0"
		p["version"] = "1.1.0"
		for _, a := range r["assets"].([]map[string]any) {
			a["name"] = strings.ReplaceAll(a["name"].(string), onlineFixtureVersion, "1.1.0")
			a["browser_download_url"] = strings.ReplaceAll(a["browser_download_url"].(string), onlineFixtureVersion, "1.1.0")
		}
		p["artifacts"].([]map[string]any)[0]["archive"] = strings.ReplaceAll(p["artifacts"].([]map[string]any)[0]["archive"].(string), onlineFixtureVersion, "1.1.0")
	}, nil)
	if _, err := c.Run(context.Background(), UpgradeRequest{ToolRoot: tool, To: "1.1.0"}); updateErrorCode(err) != "VERSION" {
		t.Fatalf("wanted downgrade refusal, got %v", err)
	}
}

func TestOnlineUpgradeRefusesInterruptedTransactionBeforeNetworking(t *testing.T) {
	tool := filepath.Join(root(t), "tools")
	interruptProgram(t, tool)
	c := onlineFixture(t, nil, nil)
	if _, err := c.Run(context.Background(), UpgradeRequest{ToolRoot: tool}); updateErrorCode(err) != "STATE" {
		t.Fatalf("wanted STATE for interrupted update, got %v", err)
	}
	if _, err := Recover(context.Background(), tool); err != nil {
		t.Fatal(err)
	}
	if _, err := Recover(context.Background(), tool); err != nil {
		t.Fatalf("repeat recovery: %v", err)
	}
}

// The optional release inputs are produced by the native release gate. This
// test exercises those actual bytes over HTTP; parser fixtures above do not
// count as native release qualification.
func TestOnlineFinalReleasePackage(t *testing.T) {
	archivePath, proofPath := os.Getenv("YSS_ONLINE_RELEASE_ARCHIVE"), os.Getenv("YSS_ONLINE_RELEASE_CHECKSUMS")
	if archivePath == "" && proofPath == "" {
		t.Skip("final native release inputs are supplied during release acceptance")
	}
	body, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	proofBytes, err := os.ReadFile(proofPath)
	if err != nil {
		t.Fatal(err)
	}
	var proof onlineProof
	if err = parseJSON(proofBytes, &proof); err != nil {
		t.Fatal(err)
	}
	if proof.Version != domain.Version {
		t.Fatal("release version differs from test source")
	}
	var artifact onlineArtifact
	for _, a := range proof.Artifacts {
		if a.Platform == runtime.GOOS+"/"+runtime.GOARCH {
			artifact = a
		}
	}
	if artifact.SHA256 != safefs.Digest(body) {
		t.Fatal("release bytes differ from checksum")
	}
	tag := "v" + proof.Version
	base := releaseBase + "download/" + tag + "/"
	release := githubRelease{Tag: tag, URL: releaseBase + "tag/" + tag, Assets: []githubAsset{{Name: "checksums.json", URL: base + "checksums.json", Size: int64(len(proofBytes))}, {Name: artifact.Archive, URL: base + artifact.Archive, Size: int64(len(body))}}}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/repos/iloveZzz/yss-cli/releases/"):
			json.NewEncoder(w).Encode(release)
		case strings.HasSuffix(r.URL.Path, "/checksums.json"):
			w.Write(proofBytes)
		default:
			w.Write(body)
		}
	}))
	defer s.Close()
	u, _ := url.Parse(s.URL)
	c := UpgradeClient{HTTPClient: &http.Client{Transport: fixtureTransport{u, http.DefaultTransport}}}
	tool := filepath.Join(root(t), "managed")
	if oldArchive := os.Getenv("YSS_ONLINE_PREVIOUS_ARCHIVE"); oldArchive != "" {
		old, err := os.ReadFile(oldArchive)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := Build(tool, oldArchive, safefs.Digest(old))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = Apply(context.Background(), plan); err != nil {
			t.Fatal(err)
		}
	}
	r, err := c.Run(context.Background(), UpgradeRequest{ToolRoot: tool})
	if err != nil || r.Transaction == nil || r.Transaction.Kind != "program-update" {
		t.Fatalf("real release install: %+v %v", r, err)
	}
	link := filepath.Join(root(t), fileName())
	if err = os.Symlink(filepath.Join(tool, fileName()), link); err != nil {
		t.Fatal(err)
	}
	actual, err := exec.Command(link, "version", "--json").Output()
	if err != nil || !strings.Contains(string(actual), `"version":"`+domain.Version+`"`) {
		t.Fatalf("installed native version: %s %v", actual, err)
	}
	c.Executable = func() (string, error) { return link, nil }
	r, err = c.Run(context.Background(), UpgradeRequest{})
	if err != nil || r.Status != "unchanged" || r.Transaction != nil || r.ToolRoot != tool {
		t.Fatalf("real release already latest: %+v %v", r, err)
	}
	if _, err = Rollback(context.Background(), tool); err != nil {
		t.Fatal(err)
	}
	if oldArchive := os.Getenv("YSS_ONLINE_PREVIOUS_ARCHIVE"); oldArchive != "" {
		actual, err = exec.Command(link, "version", "--json").Output()
		if err != nil || !strings.Contains(string(actual), `"version":"1.0.0"`) {
			t.Fatalf("rollback to previous stable: %s %v", actual, err)
		}
	}
}
