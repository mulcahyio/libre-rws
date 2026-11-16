package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mulcahyio/libre-rws/modules/wt/internal/accounts"
	"github.com/mulcahyio/libre-rws/modules/wt/internal/fleet"
)

// Libre WT against the real LibreDCM of this repository (a source build,
// run as a separate process with a backup directory): the licence page
// says the licence is not enforced and that DICOM keeps running; a licence
// action is refused by LibreDCM with its reason and nothing changes; an
// Operator makes an index backup whose file, size and SHA-256 on the page
// are those of the file LibreDCM wrote; the backup list and history show
// it; LibreDCM's own audit record names the Libre WT user and the reason.
// Verifies: WT-SRS-027, WT-SRS-028, WT-SRS-031
func TestLicenceAndBackupAgainstRealLibreDCM(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs LibreDCM")
	}
	const token = "real-token-SECRET-3" // gitleaks:allow (synthetic test value)
	backups := filepath.Join(t.TempDir(), "backups")
	adminURL := startLibreDCM(t, "real-node-3", "REALDCM3", token, "LIBRE_DCM_CONTROL_BACKUP_DIR="+backups)
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

	// Licence: a source build.
	ad := e.as(t, "admin1")
	acsrf := ad.csrf()
	code, body, _ := ad.get("/i/real/license")
	if code != 200 {
		t.Fatalf("real licence page = %d\n%s", code, body)
	}
	has(t, "real licence page", body, "Not enforced", "DICOM services keep running whatever the licence state")
	hasNot(t, "real licence page", body, "/license/ack/review")
	// Posted anyway: LibreDCM refuses, with its reason.
	confirm, _ := ad.review("/i/real/license/ack/review", url.Values{"csrf": {acsrf}})
	form := approval(acsrf, confirm, "test on a source build")
	form.Set("signature", "admin1")
	code, body, _ = ad.post("/i/real/license/ack/apply", form)
	if code != http.StatusUnprocessableEntity || !strings.Contains(body, "source build") || !strings.Contains(body, "Nothing was changed") {
		t.Fatalf("real ack on a source build = %d\n%s", code, body)
	}

	// Backup as an Operator.
	op := e.as(t, "op1")
	csrf := op.csrf()
	_, body, _ = op.get("/i/real/backups")
	has(t, "real backups page", body, "<code>"+backups+"</code>", "No backup file is in the backup directory", "/backup/review")
	confirm, _ = op.review("/i/real/backup/review", url.Values{"csrf": {csrf}})
	code, body, _ = op.post("/i/real/backup/apply", approval(csrf, confirm, "before maintenance"))
	if code != 200 {
		t.Fatalf("real backup = %d\n%s", code, body)
	}
	m := regexp.MustCompile(`<code>(index-real-node-3-\d{8}T\d{6}Z\.db)</code>`).FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no backup file name on the result page:\n%s", body)
	}
	data, err := os.ReadFile(filepath.Join(backups, m[1]))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	has(t, "real backup result", body, hex.EncodeToString(sum[:]), "("+strconv.Itoa(len(data))+" bytes)", "The backup holds the index only")
	_, body, _ = op.get("/i/real/backups")
	has(t, "real backups after", body, "<code>"+m[1]+"</code>", "<td>op1</td>", hex.EncodeToString(sum[:]), "reason: before maintenance")

	recs := controlAudit(t, adminURL, token)
	found := false
	for _, r := range recs {
		if r["operator"] == "op1" && r["reason"] == "before maintenance" && r["phase"] == "result" && r["sha256"] == hex.EncodeToString(sum[:]) {
			found = true
		}
	}
	if !found {
		t.Fatalf("no LibreDCM record of the backup by op1: %v", recs)
	}
}
