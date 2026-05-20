package bundle

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testSecret = "shhh"

func sign(body []byte) string {
	mac := hmac.New(sha256.New, []byte(testSecret))
	_, _ = mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestVerifyHMAC_Match(t *testing.T) {
	body := []byte(`{"x":1}`)
	if err := VerifyHMAC(sign(body), body, testSecret); err != nil {
		t.Errorf("VerifyHMAC valid: %v", err)
	}
}

func TestVerifyHMAC_Mismatch(t *testing.T) {
	body := []byte(`{"x":1}`)
	tampered := []byte(`{"x":2}`)
	if err := VerifyHMAC(sign(body), tampered, testSecret); !errors.Is(err, ErrInvalidSignature) {
		t.Errorf("tampered body: got %v want ErrInvalidSignature", err)
	}
}

func TestVerifyHMAC_MissingHeader(t *testing.T) {
	if err := VerifyHMAC("", []byte(`{}`), testSecret); !errors.Is(err, ErrSignatureHeaderMissing) {
		t.Errorf("missing header: got %v want ErrSignatureHeaderMissing", err)
	}
}

func TestVerifyHMAC_BadPrefix(t *testing.T) {
	if err := VerifyHMAC("md5=abcdef", []byte(`{}`), testSecret); !errors.Is(err, ErrInvalidSignature) {
		t.Errorf("bad prefix: got %v", err)
	}
}

func TestVerifyHMAC_BadHex(t *testing.T) {
	if err := VerifyHMAC("sha256=not-hex", []byte(`{}`), testSecret); !errors.Is(err, ErrInvalidSignature) {
		t.Errorf("bad hex: got %v", err)
	}
}

func TestVerifyHMAC_WrongSecret(t *testing.T) {
	body := []byte(`{"x":1}`)
	if err := VerifyHMAC(sign(body), body, "wrong"); !errors.Is(err, ErrInvalidSignature) {
		t.Errorf("wrong secret: got %v", err)
	}
}

func TestComputeSignature_RoundTrip(t *testing.T) {
	body := []byte(`{"hello":"world"}`)
	sig := ComputeSignature(body, testSecret)
	if !strings.HasPrefix(sig, "sha256=") {
		t.Errorf("sig prefix missing: %s", sig)
	}
	if err := VerifyHMAC(sig, body, testSecret); err != nil {
		t.Errorf("VerifyHMAC of own ComputeSignature failed: %v", err)
	}
}

func TestIngestPayload_Validate(t *testing.T) {
	cases := []struct {
		name string
		p    IngestPayload
		want string
	}{
		{"no tag", IngestPayload{Repo: "x/y", TarballURL: "https://a"}, "tag required"},
		{"no repo", IngestPayload{Tag: "v1", TarballURL: "https://a"}, "repo required"},
		{"no url", IngestPayload{Tag: "v1", Repo: "x/y"}, "tarball_url required"},
		{"http not https", IngestPayload{Tag: "v1", Repo: "x/y", TarballURL: "http://a"}, "https"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.p.Validate()
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error: got %q want substring %q", err.Error(), tc.want)
			}
		})
	}

	valid := IngestPayload{Tag: "v1.0.0", Repo: "x/y", TarballURL: "https://a"}
	if err := valid.Validate(); err != nil {
		t.Errorf("valid: %v", err)
	}
}

func TestIngestHandler_HappyPath(t *testing.T) {
	body := []byte(`{"tag":"v1.0.0","repo":"GoCodeAlone/site","tarball_url":"https://example.com/x.tgz"}`)
	var got IngestPayload
	h := &IngestHandler{
		Secret: testSecret,
		OnPayload: func(p IngestPayload) error {
			got = p
			return nil
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/ingest/release", bytes.NewReader(body))
	req.Header.Set(SignatureHeader, sign(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Errorf("status: got %d want 202", rec.Code)
	}
	if got.Tag != "v1.0.0" {
		t.Errorf("payload.Tag: got %q want v1.0.0", got.Tag)
	}
}

func TestIngestHandler_BadSignature401(t *testing.T) {
	body := []byte(`{"tag":"v1","repo":"x/y","tarball_url":"https://a"}`)
	h := &IngestHandler{Secret: testSecret}
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req.Header.Set(SignatureHeader, "sha256=00")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d want 401", rec.Code)
	}
}

func TestIngestHandler_MissingSignature401(t *testing.T) {
	body := []byte(`{"tag":"v1","repo":"x/y","tarball_url":"https://a"}`)
	h := &IngestHandler{Secret: testSecret}
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d want 401", rec.Code)
	}
}

func TestIngestHandler_BadJSON400(t *testing.T) {
	body := []byte(`not json`)
	h := &IngestHandler{Secret: testSecret}
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req.Header.Set(SignatureHeader, sign(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400", rec.Code)
	}
}

func TestIngestHandler_WrongMethod405(t *testing.T) {
	h := &IngestHandler{Secret: testSecret}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status: got %d want 405", rec.Code)
	}
}

func TestIngestHandler_VersionRegression409(t *testing.T) {
	body, _ := json.Marshal(IngestPayload{Tag: "v0.1.0", Repo: "x/y", TarballURL: "https://a"})
	h := &IngestHandler{
		Secret: testSecret,
		OnPayload: func(IngestPayload) error { return ErrVersionRegression },
	}
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req.Header.Set(SignatureHeader, sign(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Errorf("status: got %d want 409", rec.Code)
	}
}

func TestIngestHandler_OnPayloadError500(t *testing.T) {
	body, _ := json.Marshal(IngestPayload{Tag: "v1", Repo: "x/y", TarballURL: "https://a"})
	h := &IngestHandler{
		Secret: testSecret,
		OnPayload: func(IngestPayload) error { return errors.New("db down") },
	}
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req.Header.Set(SignatureHeader, sign(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d want 500", rec.Code)
	}
}

// --- Fetcher tests ---

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func mockHTTP(status int, body []byte, authCapture *string) *http.Client {
	return &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if authCapture != nil {
			*authCapture = r.Header.Get("Authorization")
		}
		return &http.Response{
			StatusCode: status,
			Body:       io.NopCloser(bytes.NewReader(body)),
			Header:     http.Header{},
		}, nil
	})}
}

// makeTarGz builds a gzipped tar archive with the given file contents.
func makeTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		hdr := &tar.Header{
			Name: name,
			Mode: 0o644,
			Size: int64(len(content)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestFetcher_AuthHeaderSent(t *testing.T) {
	tgz := makeTarGz(t, map[string]string{"index.html": "OK"})
	var gotAuth string
	f := &Fetcher{
		BundleRoot: t.TempDir(),
		Token:      "secret-token",
		HTTPClient: mockHTTP(200, tgz, &gotAuth),
	}
	_, err := f.Activate(context.Background(), "tenant", "v1.0.0", "https://example.com/x.tgz")
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if gotAuth != "Bearer secret-token" {
		t.Errorf("Authorization header: got %q want %q (V24)", gotAuth, "Bearer secret-token")
	}
}

func TestFetcher_ExtractsAndSwapsSymlink(t *testing.T) {
	tgz := makeTarGz(t, map[string]string{
		"index.html":       "INDEX",
		"assets/style.css": "body{}",
	})
	root := t.TempDir()
	f := &Fetcher{
		BundleRoot: root,
		Token:      "tok",
		HTTPClient: mockHTTP(200, tgz, nil),
	}
	sha, err := f.Activate(context.Background(), "tenant", "v1.0.0", "https://example.com/x.tgz")
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if sha == "" {
		t.Error("Activate did not return sha")
	}
	indexBytes, err := os.ReadFile(filepath.Join(root, "tenant", "current", "index.html"))
	if err != nil {
		t.Fatalf("ReadFile index.html: %v", err)
	}
	if string(indexBytes) != "INDEX" {
		t.Errorf("index.html: got %q", string(indexBytes))
	}
}

func TestFetcher_VersionRegression(t *testing.T) {
	tgz := makeTarGz(t, map[string]string{"x": "1"})
	root := t.TempDir()
	f := &Fetcher{BundleRoot: root, Token: "t", HTTPClient: mockHTTP(200, tgz, nil)}
	if _, err := f.Activate(context.Background(), "tenant", "v1.0.0", "https://e/x.tgz"); err != nil {
		t.Fatal(err)
	}
	tgz2 := makeTarGz(t, map[string]string{"x": "2"})
	f2 := &Fetcher{BundleRoot: root, Token: "t", HTTPClient: mockHTTP(200, tgz2, nil)}
	_, err := f2.Activate(context.Background(), "tenant", "v1.0.0", "https://e/x.tgz")
	if !errors.Is(err, ErrVersionRegression) {
		t.Errorf("expected ErrVersionRegression, got %v", err)
	}
}

func TestFetcher_RejectsNonHTTPS(t *testing.T) {
	f := &Fetcher{BundleRoot: t.TempDir(), Token: "t"}
	_, err := f.Activate(context.Background(), "tenant", "v1", "http://insecure")
	if err == nil || !strings.Contains(err.Error(), "https") {
		t.Errorf("expected https error, got %v", err)
	}
}

func TestFetcher_NoTokenRejected(t *testing.T) {
	f := &Fetcher{BundleRoot: t.TempDir()}
	_, err := f.Activate(context.Background(), "tenant", "v1", "https://e/x.tgz")
	if err == nil || !strings.Contains(err.Error(), "Token") {
		t.Errorf("expected Token error (V24), got %v", err)
	}
}

func TestFetcher_HTTPNon200(t *testing.T) {
	f := &Fetcher{
		BundleRoot: t.TempDir(),
		Token:      "t",
		HTTPClient: mockHTTP(404, []byte("not found"), nil),
	}
	_, err := f.Activate(context.Background(), "tenant", "v1", "https://e/x.tgz")
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("expected 404 error, got %v", err)
	}
}

func TestExtractTarGz_RejectsZipSlip(t *testing.T) {
	// Construct a tar with an explicit absolute path entry.
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	hdr := &tar.Header{Name: "/etc/passwd", Mode: 0o644, Size: 3}
	_ = tw.WriteHeader(hdr)
	_, _ = tw.Write([]byte("bad"))
	_ = tw.Close()
	_ = gz.Close()

	dest := t.TempDir()
	err := extractTarGz(bytes.NewReader(buf.Bytes()), dest, 1<<20)
	if err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Errorf("zip-slip absolute path should be rejected; got %v", err)
	}
}
