package web

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mulcahyio/libre-rws/modules/wt/internal/accounts"
	"github.com/mulcahyio/libre-rws/modules/wt/internal/dcmclient"
	"github.com/mulcahyio/libre-rws/modules/wt/internal/fleet"
)

func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}

// repoRoot walks up to the directory that holds go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}

// startLibreDCM builds the real LibreDCM from this repository and runs it as
// a separate process with an empty archive. Libre WT does not import
// LibreDCM (TestWTDoesNotImportDCM), so the test talks to it the way an
// installation does: over the control API only.
func startLibreDCM(t *testing.T, nodeID, aeTitle, token string, extraEnv ...string) (adminURL string) {
	t.Helper()
	root, dir := repoRoot(t), t.TempDir()
	bin := filepath.Join(dir, "LibreDCM")
	build := exec.Command("go", "build", "-o", bin, "./modules/dicom/cmd/libredcm")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building LibreDCM: %v\n%s", err, out)
	}
	admin, dicom := freePort(t), freePort(t)
	cmd := exec.Command(bin)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"LIBRE_DCM_ADMIN_ADDR="+admin, "LIBRE_DCM_LISTEN="+dicom, "LIBRE_DCM_ADMIN_TOKEN="+token,
		"LIBRE_DCM_NODE_ID="+nodeID, "LIBRE_DCM_AE_TITLE="+aeTitle,
		"LIBRE_DCM_STORAGE_PATH="+filepath.Join(dir, "blobs"), "LIBRE_DCM_INDEX_DSN=file:"+filepath.Join(dir, "index.db"),
		"LIBRE_DCM_CONTROL_OVERLAY_FILE="+filepath.Join(dir, "overlay.yaml"), "LIBRE_DCM_LOG_LEVEL=warn")
	cmd.Env = append(cmd.Env, extraEnv...)
	logFile, err := os.Create(filepath.Join(dir, "libredcm.log"))
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Signal(os.Interrupt)
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
		}
		_ = logFile.Close()
	})
	adminURL = "http://" + admin
	deadline := time.Now().Add(30 * time.Second)
	for {
		resp, err := http.Get(adminURL + "/readyz")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == 200 {
				return adminURL
			}
		}
		if time.Now().After(deadline) {
			out, _ := os.ReadFile(logFile.Name())
			t.Fatalf("LibreDCM did not become ready: %v\n%s", err, out)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// Libre WT against the real LibreDCM of this repository (built and run as a
// separate process): the fleet page, dashboard, status page and about page
// show what the node reports; Study Exceptions answer; the node refuses a
// request that names another node; a second connection with a wrong token is
// unreachable and shows no values.
// Verifies: WT-SRS-013, WT-SRS-015, WT-SRS-016, WT-SRS-017, WT-SRS-018, WT-SRS-035
func TestAgainstRealLibreDCM(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs LibreDCM")
	}
	const token = "real-token-SECRET-1" // gitleaks:allow (synthetic test value)
	adminURL := startLibreDCM(t, "real-node-1", "REALDCM", token)

	acct, err := accounts.Open(context.Background(), filepath.Join(t.TempDir(), "wt.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = acct.Close() })
	members, err := fleet.Connect([]fleet.InstanceConfig{
		{Name: "real", Cluster: "site-r", URL: adminURL, Token: token},
		{Name: "badtoken", Cluster: "site-r", URL: adminURL, Token: "not-the-token"},
	})
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	reg, err := fleet.New(members, acct, time.Nanosecond, logger)
	if err != nil {
		t.Fatal(err)
	}
	w, err := New(Config{Version: "wt-test"}, acct, reg, logger)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(w.Handler())
	t.Cleanup(srv.Close)
	e := &env{srv: srv, acct: acct, web: w}
	e.users(t)
	b := e.as(t, "viewer1")

	// What the real node says, through the real client.
	snap, _ := reg.Snapshot(context.Background(), "real")
	if snap.State != fleet.Reachable || snap.Legacy || snap.Status == nil || snap.Reported != (dcmclient.Identity{NodeID: "real-node-1", AETitle: "REALDCM"}) {
		t.Fatalf("snapshot = %+v", snap)
	}
	for _, f := range []string{"conflicts", "status", "config", "remote-aes", "allow-list", "audit", "logs", "echo", "reload", "license", "backup", "studies", "send", "config-edit"} {
		if !snap.Supports(f) {
			t.Errorf("the real LibreDCM does not list %q", f)
		}
	}
	st := snap.Status
	if !st.Ready || len(st.Checks) == 0 || st.Backends.Storage != "fs" || st.Backends.Index != "sqlite" || st.Instance.Version == "" ||
		!strings.HasPrefix(st.Instance.ConfigDigest, "sha256:") || st.License.Enforced || st.Counters.ConflictsOpen == nil || *st.Counters.ConflictsOpen != 0 ||
		st.Time.IsZero() || st.DICOM.Listen == "" {
		t.Fatalf("status = %+v", st)
	}

	code, body, _ := b.get("/")
	if code != 200 {
		t.Fatalf("fleet = %d", code)
	}
	has(t, "fleet", body, "Cluster <b>site-r</b>", "REALDCM · real-node-1", `<span class="state reachable">ready</span>`, st.Instance.Version,
		"not enforced (source build)", `<a href="/i/real/exceptions">0</a>`,
		`<span class="state unreachable">unreachable</span>`, "HTTP 401", "none since Libre WT started")
	hasNot(t, "fleet", body, "SECRET", "not-the-token")

	code, body, _ = b.get("/i/real/")
	if code != 200 {
		t.Fatalf("dashboard = %d", code)
	}
	has(t, "dashboard", body, `<b class="iname">real</b>`, "<code>REALDCM</code>", "<code>real-node-1</code>", "<b>ready</b>", "<span>Stored</span><b>0</b>",
		`<a href="/i/real/status">Status</a>`)

	code, body, _ = b.get("/i/real/status")
	if code != 200 {
		t.Fatalf("status = %d", code)
	}
	has(t, "status", body, `Readiness: <span class="state reachable">ready</span>`, "<dd>fs</dd>", "<dd>sqlite</dd>", st.Instance.ConfigDigest,
		"<td>C-FIND</td><td>total</td><td>0</td>", "<td>Study Exceptions</td><td>open</td><td>0</td>", "does not start, stop or restart")
	for _, c := range st.Checks {
		has(t, "status check", body, fmt.Sprintf(`<td>%s</td><td><span class="state reachable">ok</span></td>`, c.Name))
	}

	code, body, _ = b.get("/about")
	has(t, "about", body, "<td><code>real-node-1</code></td>", "<td><code>REALDCM</code></td>", "<td>"+st.Instance.Version+"</td>", "unknown (not reachable)")

	code, body, _ = b.get("/i/real/exceptions")
	if code != 200 || !strings.Contains(body, "No exceptions in this view") {
		t.Fatalf("exceptions = %d %s", code, body)
	}
	// The node with the wrong token: unreachable, no values, no identity.
	code, body, _ = b.get("/i/badtoken/status")
	has(t, "status with a wrong token", body, "This instance can't be reached", "HTTP 401")
	hasNot(t, "status with a wrong token", body, "Readiness:", "REALDCM")

	// The real node refuses a request that names another node, and accepts
	// the same request with its own identity (then: no such conflict).
	client, _ := reg.Client("real")
	ctx := context.Background()
	if _, err := client.Plan(ctx, "no-such-conflict", "reject", dcmclient.Identity{NodeID: "another-node", AETitle: "REALDCM"}); !dcmclient.InstanceMismatch(err) {
		t.Fatalf("plan naming another node: %v", err)
	}
	if _, err := client.Plan(ctx, "no-such-conflict", "reject", dcmclient.Identity{NodeID: "real-node-1", AETitle: "OTHERAE"}); !dcmclient.InstanceMismatch(err) {
		t.Fatalf("plan naming another AE title: %v", err)
	}
	if _, err := client.Resolve(ctx, "no-such-conflict", "reject", "admin1", "test", dcmclient.Identity{NodeID: "another-node"}); !dcmclient.InstanceMismatch(err) {
		t.Fatalf("resolve naming another node: %v", err)
	}
	if _, err := client.Plan(ctx, "no-such-conflict", "reject", snap.Expect()); !dcmclient.NotFound(err) {
		t.Fatalf("plan with the right identity: %v", err)
	}
}
