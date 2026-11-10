//go:build engines

// Network positive controls against a fake archive whose misbehaviour is
// chosen per test (modules/dvt/worker/testdata/fake_scp.py), with the real
// pynetdicom worker. Runs in the LibreDVT test image only.

package runner

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mulcahyio/libre-rws/modules/dvt/internal/engine"
	"github.com/mulcahyio/libre-rws/modules/dvt/internal/plan"
	"github.com/mulcahyio/libre-rws/modules/dvt/internal/result"
	"github.com/mulcahyio/libre-rws/modules/dvt/internal/store"
)

func freePort(t *testing.T) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func realEngines(t *testing.T) engine.Config {
	assets := os.Getenv("LIBRE_DVT_TEST_ASSETS")
	if assets == "" {
		t.Skip("LIBRE_DVT_TEST_ASSETS not set")
	}
	return engine.Config{DciodvfyBin: "dciodvfy", DcentvfyBin: "dcentvfy", Python: "python3",
		Worker:       filepath.Join(assets, "worker", "libredvt_worker.py"),
		StandardPath: filepath.Join(assets, "standard"), Edition: "2026d", Timeout: time.Minute}
}

func startFake(t *testing.T, mode string, scpPort int) int {
	t.Helper()
	port := freePort(t)
	cmd := exec.Command("python3", "../../worker/testdata/fake_scp.py", "--port", fmt.Sprint(port), "--ae", "FAKEPACS",
		"--mode", mode, "--dest", fmt.Sprintf("LIBREDVT=127.0.0.1:%d", scpPort))
	out, _ := cmd.StdoutPipe()
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	ready := make(chan bool)
	go func() { s := bufio.NewScanner(out); ready <- s.Scan() && s.Text() == "ready" }()
	select {
	case ok := <-ready:
		if !ok {
			t.Fatal("fake SCP did not start")
		}
	case <-time.After(20 * time.Second):
		t.Fatal("fake SCP start timeout")
	}
	return port
}

const netPlan = `
name: positive controls
target: fake
timeout: 10s
steps:
  - {id: echo, type: echo}
  - {id: reject, type: reject, called_aet: NOT-FAKEPACS}
  - {id: store, type: store, files: ["fixtures/*.dcm"]}
  - {id: find, type: find, from_store: true}
  - {id: move, type: move, from_store: true, compare_pixels: true}
`

func runAgainst(t *testing.T, mode string, class string, targetPort int, scpPort int) *result.Run {
	t.Helper()
	assets := os.Getenv("LIBRE_DVT_TEST_ASSETS")
	p, err := plan.Parse([]byte(netPlan), filepath.Join(assets, "plans"))
	if err != nil {
		t.Fatal(err)
	}
	tg := &store.Target{Name: "fake", Host: "127.0.0.1", Port: targetPort, CalledAET: "FAKEPACS", CallingAET: "LIBREDVT", Class: class}
	rn := &Runner{Engines: realEngines(t), SCP: engine.SCP{AETitle: "LIBREDVT", Port: scpPort}, WorkDir: t.TempDir(), Version: "test"}
	r := rn.Run(context.Background(), "id", p, tg, nil)
	for _, s := range r.Steps {
		t.Logf("[%s] %-7s %-8s %s", mode, s.ID, s.Status, strings.Join(s.Messages, "; "))
	}
	return r
}

func status(r *result.Run, id string) result.StepStatus {
	for _, s := range r.Steps {
		if s.ID == id {
			return s.Status
		}
	}
	return ""
}

// Verifies: DVT-SRS-021, DVT-SRS-022, DVT-SRS-023, DVT-SRS-024, DVT-SRS-025, DVT-SRS-026, DVT-SRS-027
func TestNetworkPositiveControls(t *testing.T) {
	realEngines(t)
	type want map[string]result.StepStatus
	P, F := result.StepPassed, result.StepFailed
	cases := []struct {
		mode string
		want want
	}{
		{"ok", want{"echo": P, "reject": P, "store": P, "find": P, "move": P}},
		{"refuse-store", want{"echo": P, "store": F, "find": F, "move": F}},
		{"drop-one", want{"store": P, "find": P, "move": F}},
		{"alter-pixels", want{"store": P, "move": F}},
		{"accept-any", want{"echo": P, "reject": F}},
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			scp := freePort(t)
			port := startFake(t, tc.mode, scp)
			r := runAgainst(t, tc.mode, store.Lab, port, scp)
			for step, w := range tc.want {
				if got := status(r, step); got != w {
					t.Errorf("step %s: %s, want %s", step, got, w)
				}
			}
			if tc.mode == "ok" && r.Verdict != result.Pass {
				t.Errorf("ok mode verdict %s", r.Verdict)
			}
			if tc.mode != "ok" && r.Verdict != result.Fail {
				t.Errorf("%s: verdict %s, want fail", tc.mode, r.Verdict)
			}
		})
	}
}

// Verifies: DVT-SRS-028, DVT-SRS-029
func TestNetworkTargetDownAndProductionGuard(t *testing.T) {
	realEngines(t)
	// Nothing listens on this port: every network step fails within the timeout.
	start := time.Now()
	r := runAgainst(t, "down", store.Lab, freePort(t), freePort(t))
	if r.Verdict != result.Fail || status(r, "echo") != result.StepFailed || time.Since(start) > 3*time.Minute {
		t.Fatalf("down target: verdict %s echo %s in %s", r.Verdict, status(r, "echo"), time.Since(start))
	}
	// A working archive that is classed production must not receive stores.
	scp := freePort(t)
	port := startFake(t, "ok", scp)
	r = runAgainst(t, "production", store.Production, port, scp)
	if status(r, "store") != result.StepFailed || status(r, "echo") != result.StepPassed {
		t.Fatalf("production guard: store %s echo %s", status(r, "store"), status(r, "echo"))
	}
}
