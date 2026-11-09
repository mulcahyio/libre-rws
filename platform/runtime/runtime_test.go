package runtime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func get(t *testing.T, h http.Handler, path, token string) (int, string) {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w.Code, w.Body.String()
}

func newTestRuntime(cfg AdminConfig) *Runtime {
	cfg.LogFormat, cfg.LogLevel = "text", "error"
	r := New("test", "1.0", cfg)
	r.AddReadinessCheck("db", func(_ context.Context) error { return nil })
	return r
}

// Verifies: DCM-SRS-041
func TestAdminNoTokenIsOpen(t *testing.T) {
	h := newTestRuntime(DefaultAdmin()).adminMux()
	for _, p := range []string{"/healthz", "/readyz", "/version", "/metrics"} {
		if code, _ := get(t, h, p, ""); code != http.StatusOK {
			t.Errorf("%s = %d without a configured token", p, code)
		}
	}
	if _, body := get(t, h, "/readyz", ""); !strings.Contains(body, "checks") {
		t.Errorf("readyz details hidden without a configured token: %s", body)
	}
}

// Verifies: DCM-SRS-041
func TestAdminToken(t *testing.T) {
	cfg := DefaultAdmin()
	cfg.Token = "s3cret"
	h := newTestRuntime(cfg).adminMux()

	for _, p := range []string{"/healthz", "/readyz"} {
		if code, _ := get(t, h, p, ""); code != http.StatusOK {
			t.Errorf("probe %s = %d, must stay open", p, code)
		}
	}
	if _, body := get(t, h, "/readyz", ""); strings.Contains(body, "checks") {
		t.Errorf("readyz leaks check details without token: %s", body)
	}
	if _, body := get(t, h, "/readyz", "s3cret"); !strings.Contains(body, "checks") {
		t.Errorf("readyz details missing with token: %s", body)
	}
	for _, p := range []string{"/version", "/metrics"} {
		if code, _ := get(t, h, p, ""); code != http.StatusUnauthorized {
			t.Errorf("%s without token = %d, want 401", p, code)
		}
		if code, _ := get(t, h, p, "wrong"); code != http.StatusUnauthorized {
			t.Errorf("%s with wrong token = %d, want 401", p, code)
		}
		if code, _ := get(t, h, p, "s3cret"); code != http.StatusOK {
			t.Errorf("%s with token = %d, want 200", p, code)
		}
	}
}

// Verifies: DCM-SRS-041
func TestAdminTokenFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(f, []byte("fromfile\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultAdmin()
	cfg.TokenFile = f
	h := newTestRuntime(cfg).adminMux()
	if code, _ := get(t, h, "/version", "fromfile"); code != http.StatusOK {
		t.Errorf("token from file rejected: %d", code)
	}

	// Missing file fails closed.
	cfg.TokenFile = filepath.Join(t.TempDir(), "missing")
	h = newTestRuntime(cfg).adminMux()
	if code, _ := get(t, h, "/version", ""); code != http.StatusUnauthorized {
		t.Errorf("missing token file served /version: %d", code)
	}
}
