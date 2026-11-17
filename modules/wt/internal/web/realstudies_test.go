package web

import (
	"context"
	"encoding/csv"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mulcahyio/libre-rws/modules/wt/internal/accounts"
	"github.com/mulcahyio/libre-rws/modules/wt/internal/fleet"
)

// Libre WT against the real LibreDCM of this repository (built and run as a
// separate process, empty archive): an Operator's study search reaches
// LibreDCM, which records it under the Operator's name; the LibreDCM audit
// page and its CSV export show that record to an Admin; the log page
// loads; a send of a study the node does not hold is refused by LibreDCM
// with its reason, and no approval is issued.
// Verifies: WT-SRS-025, WT-SRS-026, WT-SRS-029, WT-SRS-030, WT-SRS-037
func TestStudiesAuditLogsAgainstRealLibreDCM(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs LibreDCM")
	}
	const token = "real-token-SECRET-4" // gitleaks:allow (synthetic test value)
	adminURL := startLibreDCM(t, "real-node-4", "REALDCM4", token, "LIBRE_DCM_REMOTE_AES=VIEWER1=127.0.0.1:9")
	acct, err := accounts.Open(context.Background(), filepath.Join(t.TempDir(), "wt.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = acct.Close() })
	members, err := fleet.Connect([]fleet.InstanceConfig{{Name: "real", URL: adminURL, Token: token}})
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

	op := e.as(t, "op1")
	code, body, _ := op.post("/i/real/studies/search", search(op.csrf(), "patient_id", "NOPE*", "date_from", "2026-01-01"))
	if code != 200 {
		t.Fatalf("real search = %d\n%s", code, body)
	}
	has(t, "real search", body, "0 studies", "No study matches", "Answered by instance <b>real</b>")
	found := false
	for _, r := range controlAudit(t, adminURL, token) {
		keys, _ := r["keys"].([]any)
		if r["event"] == "Query" && r["operator"] == "op1" && len(keys) == 2 && keys[0] == "PatientID=NOPE*" && keys[1] == "StudyDate=20260101-" {
			found = true
		}
	}
	if !found {
		t.Fatalf("LibreDCM has no Query record of op1: %v", controlAudit(t, adminURL, token))
	}
	code, body, _ = op.get("/i/real/logs")
	if code != 200 || !strings.Contains(body, "only the most recent log lines") {
		t.Fatalf("real logs = %d\n%s", code, body)
	}

	ad := e.as(t, "admin1")
	code, body, _ = ad.get("/i/real/audit?type=Query&period=1h")
	if code != 200 {
		t.Fatalf("real audit page = %d\n%s", code, body)
	}
	has(t, "real audit page", body, "<td>Query</td><td>success</td><td>op1</td>", "<code>keys=[&#34;PatientID=NOPE*&#34;,&#34;StudyDate=20260101-&#34;]</code>")
	code, body, _ = ad.get("/i/real/audit.csv?type=Query&period=1h")
	rows, err := csv.NewReader(strings.NewReader(body)).ReadAll()
	if code != 200 || err != nil || len(rows) < 2 || rows[1][1] != "Query" || rows[1][3] != "op1" {
		t.Fatalf("real audit export = %d %v %q", code, err, rows)
	}

	// A study the node does not hold.
	code, body, _ = ad.get("/i/real/studies/1.2.3.4.5")
	if code != 200 || !strings.Contains(body, "LibreDCM lists no study with this Study Instance UID") || strings.Contains(body, "/send/review") {
		t.Fatalf("real study page = %d\n%s", code, body)
	}
	code, body, _ = ad.post("/i/real/studies/1.2.3.4.5/send/review", sendForm(ad.csrf(), "P1", "VIEWER1"))
	if code != http.StatusNotFound || !strings.Contains(body, "Nothing was changed") || confirmRE.MatchString(body) {
		t.Fatalf("real send review of an unknown study = %d\n%s", code, body)
	}
}
