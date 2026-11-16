package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/mulcahyio/libre-rws/modules/wt/internal/accounts"
	"github.com/mulcahyio/libre-rws/modules/wt/internal/dcmclient"
	"github.com/mulcahyio/libre-rws/modules/wt/internal/fakedcm"
)

// licencePaths are the licence and backup actions of the fake LibreDCM.
var licencePaths = []string{"/control/v1/license/ack", "/control/v1/license/request", "/control/v1/license/install",
	"/control/v1/license/activate", "/control/v1/backup"}

// licSent counts the licence and backup actions a fake node received.
func licSent(n *fakedcm.Server) int {
	total := 0
	for _, p := range licencePaths {
		total += len(n.Posts(p))
	}
	return total
}

// upload posts a multipart form with one file, as the licence page does.
func (b *browser) upload(path string, fields map[string]string, fileName string, content []byte) (int, string, http.Header) {
	b.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	if fileName != "" {
		fw, err := mw.CreateFormFile("license", fileName)
		if err != nil {
			b.t.Fatal(err)
		}
		_, _ = fw.Write(content)
	}
	_ = mw.Close()
	req, err := http.NewRequest(http.MethodPost, b.e.srv.URL+path, &buf)
	if err != nil {
		b.t.Fatal(err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := b.c.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body), resp.Header
}

// auditText is the whole Libre WT audit log as text.
func auditText(t *testing.T, e *env) string {
	t.Helper()
	log, err := e.acct.AuditLog(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	for _, a := range log {
		sb.WriteString(a.Action + " " + a.Target + " " + a.Outcome + " " + a.Detail + "\n")
	}
	return sb.String()
}

// Everyone sees the licence state of an instance: state, message, days
// left, expiry, customer, site, term, machine fingerprint and the
// acknowledgement, with the key shown as its last four characters only,
// also when LibreDCM sends more. Every page says DICOM services keep
// running whatever the licence state. Only an Admin sees licence actions;
// a source build offers none.
// Verifies: WT-SRS-027, WT-SRS-032, WT-SRS-034
func TestLicencePage(t *testing.T) {
	e := newEnv(t)
	e.users(t)
	for _, user := range []string{"viewer1", "op1", "admin1"} {
		code, body, _ := e.as(t, user).get("/i/default/license")
		if code != 200 {
			t.Fatalf("%s licence page = %d", user, code)
		}
		has(t, user+" licence page", body, "DICOM services keep running whatever the licence state", "<b>unlicensed</b>",
			"not licensed", "fp-synthetic-0001", "The unlicensed notice is not acknowledged", `<b class="iname">default</b>`)
		admin := user == "admin1"
		for _, ctl := range []string{"/license/ack/review", "/license/install/review", "/license/activate/review", "/license/request/review"} {
			if strings.Contains(body, ctl) != admin {
				t.Fatalf("%s: control %s shown=%v", user, ctl, !admin)
			}
		}
	}
	// The licence tab is there for every role.
	_, body, _ := e.as(t, "viewer1").get("/i/default/")
	has(t, "viewer dashboard", body, `href="/i/default/license"`)

	// A licensed node; LibreDCM sends a full key by mistake and markup in
	// its texts.
	e.dcm.Set(func(f *fakedcm.Server) {
		f.Cfg.Lic = dcmclient.LicenseDetail{Enforced: true, State: "valid", Licensed: true, DaysLeft: 200,
			Message: "licensed <b>until</b> 2027-06-01", Customer: "Example Clinic", Site: "Room <i>1</i>", Term: "annual",
			KeyLast4: "LK-FULLKEY-SYNTH-9Q7Z", Fingerprint: "fp-2"} // gitleaks:allow (synthetic test value)
	})
	_, body, _ = e.as(t, "admin1").get("/i/default/license")
	has(t, "licensed node", body, "<b>valid</b>", "licensed", "<td>200</td>", "Example Clinic", "Room &lt;i&gt;1&lt;/i&gt;",
		"licensed &lt;b&gt;until&lt;/b&gt; 2027-06-01", "<code>••••9Q7Z</code>", "only the last four characters")
	hasNot(t, "licensed node", body, "LK-FULLKEY", "FULLKEY-SYNTH", "<i>1</i>", "The unlicensed notice is not acknowledged")

	// A source build: nothing to do, no actions.
	e.dcm.Set(func(f *fakedcm.Server) {
		f.Cfg.Lic = dcmclient.LicenseDetail{Enforced: false, Note: "Source build: no licence is needed."}
	})
	_, body, _ = e.as(t, "admin1").get("/i/default/license")
	has(t, "source build", body, "Not enforced", "source build", "DICOM services keep running whatever the licence state")
	hasNot(t, "source build", body, "/license/ack/review", "/license/install/review")

	// A node without the licence functions gets a plain page.
	e.dcm.Set(func(f *fakedcm.Server) { f.Features = []string{"conflicts", "status"} })
	code, body, _ := e.as(t, "admin1").get("/i/default/license")
	if code != 200 || !strings.Contains(body, "Not available on this instance") {
		t.Fatalf("licence page without the function = %d", code)
	}
}

// Only an Admin acknowledges the unlicensed notice, through a warning page
// with the single-use confirmation, a reason, the approval box and their
// own user name typed as a signature. LibreDCM gets the logged-in user as
// operator, never the typed text. Review, approval and result are audited.
// Verifies: WT-SRS-027, WT-SRS-031, WT-SRS-033
func TestLicenceAckFlow(t *testing.T) {
	e := newEnv(t)
	e.users(t)
	b := e.as(t, "admin1")
	csrf := b.csrf()
	start := url.Values{"csrf": {csrf}}
	confirm, page := b.review("/i/default/license/ack/review", start)
	has(t, "ack warning page", page, "Review: Acknowledge the unlicensed notice", "DICOM services keep running whatever happens here",
		"under your user name <b>admin1</b>", `name="signature"`, "Instance <b>default</b>", "<code>node-default</code>")
	if licSent(e.dcm) != 0 {
		t.Fatal("the review sent the acknowledgement")
	}
	sign := func(confirm, name, reason string) url.Values {
		v := approval(csrf, confirm, reason)
		v.Set("signature", name)
		return v
	}
	refused := func(what string, form url.Values, wantCode int, wantText string) {
		t.Helper()
		code, body, _ := b.post("/i/default/license/ack/apply", form)
		if code != wantCode || !strings.Contains(body, wantText) || licSent(e.dcm) != 0 {
			t.Fatalf("%s = %d (want %d, %q), %d sent\n%s", what, code, wantCode, wantText, licSent(e.dcm), body)
		}
	}
	refused("no signature", approval(csrf, confirm, "seen"), http.StatusUnprocessableEntity, "Type your own user name (admin1)")
	confirm, _ = b.review("/i/default/license/ack/review", start)
	refused("someone else's name", sign(confirm, "op1", "seen"), http.StatusUnprocessableEntity, "Type your own user name")
	confirm, _ = b.review("/i/default/license/ack/review", start)
	noBox := sign(confirm, "admin1", "seen")
	noBox.Del("approve")
	refused("no approval box", noBox, http.StatusUnprocessableEntity, "Tick the approval box")
	confirm, _ = b.review("/i/default/license/ack/review", start)
	refused("no reason", sign(confirm, "admin1", " "), http.StatusUnprocessableEntity, "A reason is required")
	// The confirmation of another action does not approve this one.
	other, _ := b.review("/i/default/backup/review", start)
	refused("backup confirmation", sign(other, "admin1", "seen"), http.StatusConflict, "approval expired or was already used")

	confirm, _ = b.review("/i/default/license/ack/review", start)
	code, body, _ := b.post("/i/default/license/ack/apply", sign(confirm, " Admin1 ", "licence on order, PO 4711"))
	if code != 200 {
		t.Fatalf("ack = %d\n%s", code, body)
	}
	has(t, "ack result", body, "Done: Acknowledge the unlicensed notice", "recorded the acknowledgement on instance <b>default</b>",
		"by <b>admin1</b>", "DICOM services kept running")
	got := e.dcm.Posts("/control/v1/license/ack")
	if len(got) != 1 || got[0]["signature"] != nil {
		t.Fatalf("ack request = %v", got)
	}
	envelopeOK(t, "ack", got[0], "admin1", "licence on order, PO 4711", "node-default", "AE_DEFAULT")
	if code, _, _ := b.post("/i/default/license/ack/apply", sign(confirm, "admin1", "again")); code != http.StatusConflict || licSent(e.dcm) != 1 {
		t.Fatalf("ack confirmation used twice = %d", code)
	}
	_, body, _ = b.get("/i/default/license")
	hasNot(t, "licence page after the ack", body, "The unlicensed notice is not acknowledged")
	has(t, "licence page after the ack", body, "by <b>admin1</b>")

	ap, res := auditOf(t, e, "license.ack.approved"), auditOf(t, e, "license.ack")
	if len(ap) != 1 || ap[0].User != "admin1" || ap[0].Instance != "default" || !strings.Contains(ap[0].Detail, "reason: licence on order, PO 4711") {
		t.Fatalf("approval record = %+v", ap)
	}
	ok := 0
	for _, r := range res {
		if r.Outcome == "success" {
			ok++
			if !strings.Contains(r.Detail, "acknowledgement needed false") {
				t.Fatalf("result record = %+v", r)
			}
		}
	}
	if ok != 1 || len(auditOf(t, e, "license.ack.review")) != 5 {
		t.Fatalf("audit: %d successes, %d reviews", ok, len(auditOf(t, e, "license.ack.review")))
	}
}

// An Admin installs a licence file: the upload is size-limited and must be
// text; the warning page shows the file name, size and checksum; LibreDCM
// gets exactly the reviewed file and checks it. A file LibreDCM refuses is
// reported and the installed licence stays. The file content is in no page,
// no log line and no audit record.
// Verifies: WT-SRS-027, WT-SRS-031, WT-SRS-032
func TestLicenceInstallFlow(t *testing.T) {
	e := newEnv(t)
	e.users(t)
	b := e.as(t, "admin1")
	csrf := b.csrf()
	licence := []byte("-----BEGIN LIBREKEY LICENSE-----\nU1lOVEhFVElDLUxJQ0VOQ0UtQk9EWS0wMDAx\n-----END LIBREKEY LICENSE-----\n")
	sum := sha256.Sum256(licence)
	code, page, _ := b.upload("/i/default/license/install/review", map[string]string{"csrf": csrf}, `C:\Users\it\licence-2027.lic`, licence)
	if code != 200 {
		t.Fatalf("install review = %d\n%s", code, page)
	}
	has(t, "install warning page", page, "Review: Install a licence file", "<code>licence-2027.lic</code>", "<td>"+strconv.Itoa(len(licence))+" bytes</td>",
		hex.EncodeToString(sum[:]), "the installed licence stays as it is", "DICOM services keep running")
	hasNot(t, "install warning page", page, "U1lOVEhFVElDLUxJQ0VOQ0UtQk9EWS0wMDAx", `C:\Users`)
	confirm := find(t, confirmRE, page)
	if licSent(e.dcm) != 0 {
		t.Fatal("the review installed the licence")
	}
	code, body, _ := b.post("/i/default/license/install/apply", approval(csrf, confirm, "licence for 2027"))
	if code != 200 {
		t.Fatalf("install = %d\n%s", code, body)
	}
	has(t, "install result", body, "Done: Install a licence file", "checked and installed the licence file", "<b>valid</b>", "Example Clinic (synthetic)", "••••WXYZ")
	got := e.dcm.Posts("/control/v1/license/install")
	if len(got) != 1 || got[0]["license"] != string(licence) {
		t.Fatalf("install request = %v", got)
	}
	envelopeOK(t, "install", got[0], "admin1", "licence for 2027", "node-default", "AE_DEFAULT")

	// A refused licence: LibreDCM's reason, the installed licence stays.
	bad := []byte("-----BEGIN LIBREKEY LICENSE-----\nINVALID\n-----END LIBREKEY LICENSE-----\n")
	_, page, _ = b.upload("/i/default/license/install/review", map[string]string{"csrf": csrf}, "other.lic", bad)
	code, body, _ = b.post("/i/default/license/install/apply", approval(csrf, find(t, confirmRE, page), "try other file"))
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("refused licence = %d\n%s", code, body)
	}
	has(t, "refused licence", body, "Install a licence file: not done", "signature does not verify", "the installed licence is unchanged", "Nothing was changed")
	hasNot(t, "refused licence", body, "Done:")
	_, body, _ = b.get("/i/default/license")
	has(t, "licence page after a refused file", body, "<b>valid</b>", "Example Clinic (synthetic)")

	// Uploads that never reach LibreDCM.
	n := licSent(e.dcm)
	for _, c := range []struct {
		what     string
		name     string
		content  []byte
		code     int
		wantText string
	}{
		{"empty file", "a.lic", []byte(" \n"), http.StatusUnprocessableEntity, "The file is empty"},
		{"binary file", "a.lic", []byte("LIC\x00\xff\xfe"), http.StatusUnprocessableEntity, "a licence file is text"},
		{"no file", "", nil, http.StatusUnprocessableEntity, "Choose the licence file"},
		{"file over the limit", "a.lic", bytes.Repeat([]byte("A"), maxLicenceFile+1), http.StatusRequestEntityTooLarge, "larger than 128 KiB"},
		{"body over the limit", "a.lic", bytes.Repeat([]byte("A"), 5<<20), http.StatusRequestEntityTooLarge, "The upload is larger than 192 KiB"},
	} {
		code, body, _ := b.upload("/i/default/license/install/review", map[string]string{"csrf": csrf}, c.name, c.content)
		if code != c.code || !strings.Contains(body, c.wantText) || strings.Contains(body, `name="confirm"`) {
			t.Fatalf("%s = %d\n%s", c.what, code, body)
		}
	}
	// An upload without the CSRF token, and an upload to another address.
	if code, _, _ := b.upload("/i/default/license/install/review", nil, "a.lic", licence); code != http.StatusForbidden {
		t.Fatalf("upload without CSRF = %d", code)
	}
	if code, _, _ := b.upload("/i/default/license/activate/review", map[string]string{"csrf": csrf, "key": "LK-SYNTH-1"}, "a.lic", licence); code != http.StatusForbidden {
		t.Fatalf("multipart form on a non-upload address = %d", code)
	}
	if licSent(e.dcm) != n {
		t.Fatal("a refused upload reached LibreDCM")
	}
	if a := auditText(t, e); strings.Contains(a, "U1lOVEhFVElD") || strings.Contains(a, "BEGIN LIBREKEY") {
		t.Fatalf("licence content in the audit log:\n%s", a)
	}
	if strings.Contains(e.log.String(), "U1lOVEhFVElD") {
		t.Fatal("licence content in the log")
	}
	if ap := auditOf(t, e, "license.install.approved"); len(ap) != 2 || !strings.Contains(ap[0].Detail, hex.EncodeToString(sum[:])) {
		t.Fatalf("install approvals = %+v", ap)
	}
}

// Online activation and the activation request file: the key is typed into
// a password field, shown only as its last four characters, sent to
// LibreDCM once, and written to no page, log line or audit record, also
// when LibreDCM's error text repeats it. The request file is handed out as
// a download.
// Verifies: WT-SRS-027, WT-SRS-031, WT-SRS-032
func TestLicenceActivateAndRequest(t *testing.T) {
	const key = "LK-SYNTH-7Y3Q-K9D2-ABCD" // gitleaks:allow (synthetic test value)
	e := newEnv(t)
	e.users(t)
	b := e.as(t, "admin1")
	csrf := b.csrf()
	_, body, _ := b.get("/i/default/license")
	has(t, "licence page", body, `<input type="password" name="key"`)
	if n := strings.Count(body, `name="key"`); n != 2 || strings.Count(body, `<input type="password" name="key"`) != n {
		t.Fatalf("licence key fields: %d, not all password fields\n%s", n, body)
	}

	form := url.Values{"csrf": {csrf}, "key": {"  " + key + " "}, "site": {"Main campus"}}
	confirm, page := b.review("/i/default/license/activate/review", form)
	has(t, "activate warning page", page, "Review: Activate online", "<code>••••ABCD</code>", "Main campus", "nothing changes")
	hasNot(t, "activate warning page", page, key, "7Y3Q")
	code, body, _ := b.post("/i/default/license/activate/apply", approval(csrf, confirm, "new licence"))
	if code != 200 {
		t.Fatalf("activate = %d\n%s", code, body)
	}
	has(t, "activate result", body, "activated instance <b>default</b>", "••••ABCD", "<b>valid</b>")
	hasNot(t, "activate result", body, key)
	got := e.dcm.Posts("/control/v1/license/activate")
	if len(got) != 1 || got[0]["key"] != key || got[0]["site"] != "Main campus" {
		t.Fatalf("activate request = %v", got)
	}
	envelopeOK(t, "activate", got[0], "admin1", "new licence", "node-default", "AE_DEFAULT")

	// The licence server repeats the key in its refusal.
	e.dcm.Set(func(f *fakedcm.Server) {
		f.Cfg.Fail["/control/v1/license/activate"] = fakedcm.Failure{Status: 422, Code: "activation_failed",
			Msg: "activation failed; the installed licence is unchanged: key " + strings.ToLower(key) + " is revoked"}
	})
	confirm, _ = b.review("/i/default/license/activate/review", form)
	code, body, _ = b.post("/i/default/license/activate/apply", approval(csrf, confirm, "retry"))
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("refused activation = %d\n%s", code, body)
	}
	has(t, "refused activation", body, "the installed licence is unchanged", "key ••••ABCD is revoked", "Nothing was changed")
	hasNot(t, "refused activation", body, key, strings.ToLower(key))

	// Keys that can't be keys never reach LibreDCM.
	n := licSent(e.dcm)
	for _, k := range []string{"", "has space", strings.Repeat("K", 129)} {
		code, body, _ := b.post("/i/default/license/activate/review", url.Values{"csrf": {csrf}, "key": {k}})
		if code != http.StatusUnprocessableEntity || strings.Contains(body, `name="confirm"`) {
			t.Fatalf("key %q = %d", k, code)
		}
	}
	if code, _, _ := b.post("/i/default/license/request/review", url.Values{"csrf": {csrf}, "site": {"a\x07b"}}); code != http.StatusUnprocessableEntity {
		t.Fatalf("site with a control character = %d", code)
	}

	// The request file.
	confirm, page = b.review("/i/default/license/request/review", form)
	has(t, "request warning page", page, "Review: Make an activation request file", "<code>••••ABCD</code>", "Download the request file")
	code, body, h := b.post("/i/default/license/request/apply", approval(csrf, confirm, "offline site"))
	if code != 200 || !strings.HasPrefix(h.Get("Content-Type"), "text/plain") ||
		!strings.HasPrefix(h.Get("Content-Disposition"), `attachment; filename="libredcm-activation-request-default-`) ||
		!strings.Contains(body, "BEGIN LIBREKEY ACTIVATION REQUEST") {
		t.Fatalf("request file = %d %v\n%s", code, h, body)
	}
	if h.Get("Cache-Control") != "no-store" || h.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("request file headers = %v", h)
	}
	if licSent(e.dcm) != n+1 {
		t.Fatalf("%d actions sent", licSent(e.dcm)-n)
	}
	// Without a key: allowed, the request names the machine only.
	confirm, page = b.review("/i/default/license/request/review", url.Values{"csrf": {csrf}})
	has(t, "request without key", page, "none (the request names this machine only)")
	if code, _, _ := b.post("/i/default/license/request/apply", approval(csrf, confirm, "x")); code != 200 {
		t.Fatalf("request without key = %d", code)
	}
	reqs := e.dcm.Posts("/control/v1/license/request")
	if len(reqs) != 2 || reqs[1]["key"] != nil {
		t.Fatalf("request bodies = %v", reqs)
	}

	a := auditText(t, e)
	if strings.Contains(a, key) || strings.Contains(a, strings.ToLower(key)) || strings.Contains(a, "7Y3Q") || strings.Contains(a, "BEGIN LIBREKEY") {
		t.Fatalf("key or request text in the audit log:\n%s", a)
	}
	if !strings.Contains(a, "••••ABCD") {
		t.Fatalf("audit log without the masked key:\n%s", a)
	}
	if l := e.log.String(); strings.Contains(l, key) || strings.Contains(l, "7Y3Q") {
		t.Fatal("key in the log")
	}
}

// Operators and Admins start an index backup through a warning page with
// a reason and the approval box; the result shows file, size and checksum;
// the backups page lists the node's files and the history from the Libre
// WT audit log. There is no path or file name field, no download and no
// delete; the page says the backup holds the index only. A node without a
// backup directory offers nothing to approve.
// Verifies: WT-SRS-028, WT-SRS-031, WT-SRS-033
func TestBackupFlow(t *testing.T) {
	e := newEnv(t)
	e.users(t)
	op := e.as(t, "op1")
	csrf := op.csrf()
	code, body, _ := op.get("/i/default/backups")
	if code != 200 {
		t.Fatalf("backups page = %d", code)
	}
	has(t, "backups page", body, "The backup holds the index only", "Images are not included", "<code>/var/lib/libredcm/backups</code>",
		"<code>index-node-1-20261101T020000Z.db</code>", "4.0 KiB", "/backup/review", "No backup was started from Libre WT")
	for _, s := range []string{`name="path"`, `name="file"`, `name="dir"`, "/download", "/delete"} {
		hasNot(t, "backups page", body, s)
	}
	confirm, page := op.review("/i/default/backup/review", url.Values{"csrf": {csrf}, "path": {"/etc/passwd"}})
	has(t, "backup warning page", page, "Review: backup of the index", "Images are not included", "No file is replaced",
		"<code>/var/lib/libredcm/backups</code>", "Instance <b>default</b>")
	if licSent(e.dcm) != 0 {
		t.Fatal("the review started a backup")
	}
	if code, _, _ := op.post("/i/default/backup/apply", approval(csrf, confirm, "")); code != http.StatusUnprocessableEntity || licSent(e.dcm) != 0 {
		t.Fatalf("backup without a reason = %d", code)
	}
	confirm, _ = op.review("/i/default/backup/review", url.Values{"csrf": {csrf}})
	form := approval(csrf, confirm, "before Windows update")
	form.Set("path", "/etc/passwd")
	code, body, _ = op.post("/i/default/backup/apply", form)
	if code != 200 {
		t.Fatalf("backup = %d\n%s", code, body)
	}
	has(t, "backup result", body, "Done: backup of the index", "<code>index-node-default-", "8.0 KiB (8192 bytes)",
		"5f3c0b8e1d2a4c6e8f0a1b3c5d7e9f1a2b4c6d8e0f1a3b5c7d9e1f2a4b6c8d0e", "The backup holds the index only")
	got := e.dcm.Posts("/control/v1/backup")
	if len(got) != 1 || len(got[0]) != 5 { // the envelope only: operator, reason, request_id, expect_*
		t.Fatalf("backup request = %v", got)
	}
	envelopeOK(t, "backup", got[0], "op1", "before Windows update", "node-default", "AE_DEFAULT")
	if code, _, _ := op.post("/i/default/backup/apply", approval(csrf, confirm, "again")); code != http.StatusConflict || licSent(e.dcm) != 1 {
		t.Fatalf("backup confirmation used twice = %d", code)
	}
	// The history comes from the audit log.
	_, body, _ = e.as(t, "admin1").get("/i/default/backups")
	has(t, "history", body, "<td>op1</td>", "<td>success</td>", "5f3c0b8e1d2a4c6e8f0a1b3c5d7e9f1a2b4c6d8e0f1a3b5c7d9e1f2a4b6c8d0e",
		"reason: before Windows update", "8192 bytes")
	hist, err := e.acct.AuditActionLog(t.Context(), "default", "backup", 10)
	if err != nil || len(hist) != 3 || hist[1].Outcome != "success" || hist[0].Outcome != "failure" {
		t.Fatalf("backup history = %+v, %v", hist, err)
	}

	// Busy and refused: LibreDCM's reason, never success.
	e.dcm.Set(func(f *fakedcm.Server) {
		f.Cfg.Fail["/control/v1/backup"] = fakedcm.Failure{Status: 429, Code: "busy", Msg: "another backup is running"}
	})
	confirm, _ = op.review("/i/default/backup/review", url.Values{"csrf": {csrf}})
	code, body, _ = op.post("/i/default/backup/apply", approval(csrf, confirm, "x"))
	if code != http.StatusTooManyRequests || !strings.Contains(body, "another backup is running") || strings.Contains(body, "Done:") {
		t.Fatalf("busy = %d\n%s", code, body)
	}
	e.dcm.Set(func(f *fakedcm.Server) {
		f.Cfg.Fail["/control/v1/backup"] = fakedcm.Failure{Status: 422, Code: "insufficient_space", Msg: "not enough free space in the backup directory; no backup was started"}
	})
	confirm, _ = op.review("/i/default/backup/review", url.Values{"csrf": {csrf}})
	code, body, _ = op.post("/i/default/backup/apply", approval(csrf, confirm, "x"))
	if code != http.StatusUnprocessableEntity || !strings.Contains(body, "not enough free space") {
		t.Fatalf("no space = %d\n%s", code, body)
	}

	// No backup directory: said so, no button, nothing to approve.
	e.dcm.Set(func(f *fakedcm.Server) { f.Cfg.BackupDir = "" })
	_, body, _ = op.get("/i/default/backups")
	has(t, "backups off", body, "Backups can't be made from the console on this instance", "LIBRE_DCM_CONTROL_BACKUP_DIR")
	hasNot(t, "backups off", body, "/backup/review")
	n := licSent(e.dcm)
	code, body, _ = op.post("/i/default/backup/review", url.Values{"csrf": {csrf}})
	if code != http.StatusUnprocessableEntity || strings.Contains(body, `name="confirm"`) || licSent(e.dcm) != n {
		t.Fatalf("backup review without a directory = %d", code)
	}
}

// A backup without an answer in time is not reported as done or failed:
// the page and the audit record say the outcome is not known, give the
// request ID and point to the list of backups.
// Verifies: WT-SRS-028, WT-SRS-033
func TestBackupWithoutAnswer(t *testing.T) {
	e := newFleetEnv(t, "mute-1")
	e.users(t)
	b := e.as(t, "op1")
	csrf := b.csrf()
	confirm, _ := b.review("/i/mute-1/backup/review", url.Values{"csrf": {csrf}})
	code, body, _ := b.post("/i/mute-1/backup/apply", approval(csrf, confirm, "x"))
	if code != http.StatusGatewayTimeout {
		t.Fatalf("backup without an answer = %d", code)
	}
	has(t, "no answer", body, "Backup: outcome not known", "It is not known whether the action was done", "request ID")
	hasNot(t, "no answer", body, "Done:", "Nothing was changed")
	res := auditOf(t, e, "backup")
	if len(res) != 1 || res[0].Outcome != "unknown" {
		t.Fatalf("audit = %+v", res)
	}
}

func (mute) Backup(context.Context, dcmclient.Envelope) (dcmclient.BackupResult, error) {
	return dcmclient.BackupResult{}, errMute
}

// Every licence and backup address is refused below its role with a valid
// form, and refused without the session's CSRF token; nothing reaches
// LibreDCM then. Pages show no control the role can't use.
// Verifies: WT-SRS-004, WT-SRS-031, WT-SRS-034
func TestLicenceBackupRolesAndCSRF(t *testing.T) {
	e := newEnv(t)
	e.users(t)
	O, A := accounts.Operator, accounts.Admin
	rows := []struct {
		path string
		min  accounts.Role
		form url.Values
	}{
		{"/license/ack/review", A, url.Values{}},
		{"/license/ack/apply", A, approval("", "x", "r")},
		{"/license/request/review", A, url.Values{"key": {"LK-SYNTH-1"}}},
		{"/license/request/apply", A, approval("", "x", "r")},
		{"/license/install/apply", A, approval("", "x", "r")},
		{"/license/activate/review", A, url.Values{"key": {"LK-SYNTH-1"}}},
		{"/license/activate/apply", A, approval("", "x", "r")},
		{"/backup/review", O, url.Values{}},
		{"/backup/apply", O, approval("", "x", "r")},
	}
	users := map[accounts.Role]string{accounts.Viewer: "viewer1", O: "op1", A: "admin1"}
	for _, role := range []accounts.Role{accounts.Viewer, O, A} {
		b := e.as(t, users[role])
		csrf := b.csrf()
		for _, rw := range rows {
			allowed := rank(role) >= rank(rw.min)
			form := url.Values{}
			for k, v := range rw.form {
				form[k] = v
			}
			form.Set("csrf", csrf)
			code, body, _ := b.post("/i/default"+rw.path, form)
			if refused := code == http.StatusForbidden && strings.Contains(body, "can&#39;t open this page"); refused == allowed {
				t.Errorf("POST %s as %s: HTTP %d, allowed=%v", rw.path, role, code, allowed)
			}
			for _, token := range []string{"", "0123456789abcdef", e.as(t, users[role]).csrf()} {
				form.Set("csrf", token)
				if code, _, _ := b.post("/i/default"+rw.path, form); code != http.StatusForbidden {
					t.Errorf("POST %s as %s with CSRF token %q: HTTP %d", rw.path, role, token, code)
				}
			}
		}
		// The upload address, with a file.
		code, body, _ := b.upload("/i/default/license/install/review", map[string]string{"csrf": csrf}, "a.lic", []byte("LICENCE"))
		if refused := code == http.StatusForbidden; refused == (role == A) {
			t.Errorf("upload as %s = %d\n%s", role, code, body)
		}
		// Backup page and tab.
		code, _, _ = b.get("/i/default/backups")
		if (code == 200) != (role != accounts.Viewer) {
			t.Errorf("backups page as %s = %d", role, code)
		}
		_, body, _ = b.get("/i/default/")
		if strings.Contains(body, `href="/i/default/backups"`) != (role != accounts.Viewer) {
			t.Errorf("%s: backup tab shown=%v", role, role == accounts.Viewer)
		}
	}
	if licSent(e.dcm) != 0 {
		t.Fatalf("%d action(s) sent by the role walk", licSent(e.dcm))
	}
}

// No licence or backup action is sent when the audit log can't be written,
// while the node is not the remembered one, or when LibreDCM's answer comes
// from another node; a node that refuses with "instance mismatch" is
// reported as such.
// Verifies: WT-SRS-015, WT-SRS-031, WT-SRS-033
func TestLicenceBackupGuards(t *testing.T) {
	e := newEnv(t)
	e.users(t)
	b := e.as(t, "admin1")
	csrf := b.csrf()
	start := url.Values{"csrf": {csrf}}
	b.get("/i/default/") // the identity is remembered
	review := func() map[string]string {
		t.Helper()
		tok := map[string]string{}
		tok["/license/ack/apply"], _ = b.review("/i/default/license/ack/review", start)
		tok["/license/activate/apply"], _ = b.review("/i/default/license/activate/review", url.Values{"csrf": {csrf}, "key": {"LK-SYNTH-2222"}})
		tok["/license/request/apply"], _ = b.review("/i/default/license/request/review", start)
		_, page, _ := b.upload("/i/default/license/install/review", map[string]string{"csrf": csrf}, "a.lic", []byte("LICENCE"))
		tok["/license/install/apply"] = find(t, confirmRE, page)
		tok["/backup/apply"], _ = b.review("/i/default/backup/review", start)
		return tok
	}
	form := func(path, tok string) url.Values {
		v := approval(csrf, tok, "approved")
		if path == "/license/ack/apply" {
			v.Set("signature", "admin1")
		}
		return v
	}

	// The node changes after the warning pages.
	tokens := review()
	e.dcm.Set(func(f *fakedcm.Server) { f.NodeID = "node-replaced" })
	for p, tok := range tokens {
		code, body, _ := b.post("/i/default"+p, form(p, tok))
		if code != http.StatusConflict || !strings.Contains(body, "not the node Libre WT remembers") {
			t.Fatalf("%s with a changed node = %d\n%s", p, code, body)
		}
	}
	for _, p := range []string{"/license/ack/review", "/backup/review"} {
		if code, _, _ := b.post("/i/default"+p, start); code != http.StatusConflict {
			t.Fatalf("%s with a changed node = %d", p, code)
		}
	}
	_, body, _ := b.get("/i/default/license")
	has(t, "licence page with a changed node", body, "is not the node Libre WT remembers", "<button disabled>Acknowledge…</button>")
	e.dcm.Set(func(f *fakedcm.Server) { f.NodeID = "node-default" })

	// LibreDCM refuses: not the node it is.
	tokens = review()
	e.dcm.Set(func(f *fakedcm.Server) { f.MismatchOnPost = true })
	for p, tok := range tokens {
		code, body, _ := b.post("/i/default"+p, form(p, tok))
		if code != http.StatusConflict || !strings.Contains(body, "is not the node Libre WT expects for this instance. Nothing was changed") {
			t.Fatalf("%s refused by LibreDCM = %d\n%s", p, code, body)
		}
	}
	e.dcm.Set(func(f *fakedcm.Server) { f.MismatchOnPost = false })

	// Another node answers the reads of the pages.
	for page, path := range map[string]string{"/license": "/control/v1/license", "/backups": "/control/v1/backups"} {
		e.dcm.Set(func(f *fakedcm.Server) { f.Cfg.OtherNode = map[string]bool{path: true} })
		code, body, _ := b.get("/i/default" + page)
		if code != http.StatusConflict || !strings.Contains(body, "Another node answered") {
			t.Fatalf("%s answered by another node = %d", page, code)
		}
		hasNot(t, page, body, "fp-synthetic-0001", "index-node-1-")
	}
	// ... and the reads a review makes: no confirmation is issued.
	for review, path := range map[string]string{"/license/ack/review": "/control/v1/license", "/backup/review": "/control/v1/backups"} {
		e.dcm.Set(func(f *fakedcm.Server) { f.Cfg.OtherNode = map[string]bool{path: true} })
		code, body, _ := b.post("/i/default"+review, start)
		if code != http.StatusConflict || !strings.Contains(body, "Another node answered") || strings.Contains(body, `name="confirm"`) {
			t.Fatalf("%s with another node's answer = %d\n%s", review, code, body)
		}
	}
	e.dcm.Set(func(f *fakedcm.Server) { f.Cfg.OtherNode = map[string]bool{} })

	// No audit record, no action.
	tokens = review()
	before := licSent(e.dcm)
	e.breakAudit(t)
	for p, tok := range tokens {
		code, body, _ := b.post("/i/default"+p, form(p, tok))
		if code != http.StatusInternalServerError || !strings.Contains(body, "audit log can&#39;t be written, so nothing was sent") {
			t.Fatalf("%s without an audit record = %d\n%s", p, code, body)
		}
	}
	if licSent(e.dcm) != before {
		t.Fatalf("%d action(s) sent without an audit record", licSent(e.dcm)-before)
	}
}
