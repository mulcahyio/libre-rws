package engine

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mulcahyio/libre-rws/modules/dvt/internal/redact"
)

const synthSecret = "dvt-synthetic-bearer-engine-0123456789" // gitleaks:allow (synthetic test value)

// recordingWorker writes its argument vector and environment next to
// itself and answers like a successful echo.
const recordingWorker = `
d=$(dirname "$0")
printf '%s\n' "$@" > "$d/argv"
env > "$d/env"
echo '{"associated":true,"status":0}'
`

// Verifies: DVT-SRS-153
func TestWorkerNeverGetsSecretsOnArgvOrEnv(t *testing.T) {
	dir := t.TempDir()
	c := Config{Python: "/bin/sh", Worker: script(t, dir, "worker.sh", recordingWorker), Timeout: 5 * time.Second, Version: "test"}
	secrets := &redact.Set{}
	secrets.AddToken(synthSecret)
	ctx := WithSecrets(context.Background(), secrets)

	// A normal command runs, and its argv and environment hold no secret.
	r, _, err := c.Echo(ctx, Peer{Host: "h", Port: 104, CalledAET: "A", CallingAET: "B", Timeout: time.Second})
	if err != nil || !r.Associated {
		t.Fatalf("clean echo refused: %v", err)
	}
	for _, f := range []string{"argv", "env"} {
		b, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			t.Fatal(err)
		}
		if secrets.Contains(string(b)) {
			t.Fatalf("secret in worker %s", f)
		}
	}
	// A secret (or an encoding of it) in the arguments or the environment
	// refuses the start: the worker never runs.
	starts := map[string]func(t *testing.T) error{
		"raw argument": func(*testing.T) error {
			_, err := c.WorkerCall(ctx, time.Second, &struct{}{}, "echo", "--token", synthSecret)
			return err
		},
		"base64 argument": func(*testing.T) error {
			_, err := c.WorkerCall(ctx, time.Second, &struct{}{}, "echo", "--auth=Basic "+base64.StdEncoding.EncodeToString([]byte(synthSecret)))
			return err
		},
		"environment": func(t *testing.T) error {
			t.Setenv("LIBRE_DVT_TEST_TOKEN", synthSecret)
			_, err := c.WorkerCall(ctx, time.Second, &struct{}{}, "echo")
			return err
		},
	}
	for name, start := range starts {
		t.Run(name, func(t *testing.T) {
			_ = os.Remove(filepath.Join(dir, "argv"))
			err := start(t)
			if !errors.Is(err, errSecretInArgs) {
				t.Fatalf("not refused: %v", err)
			}
			if strings.Contains(err.Error(), synthSecret) {
				t.Fatal("error names the secret")
			}
			if _, statErr := os.Stat(filepath.Join(dir, "argv")); statErr == nil {
				t.Fatal("worker was started")
			}
		})
	}
	// Without configured secrets nothing is checked (v0.1 behaviour).
	if _, err := c.WorkerCall(context.Background(), time.Second, &struct{}{}, "echo", "--token", synthSecret); err != nil {
		t.Fatalf("no secrets configured: %v", err)
	}
}
