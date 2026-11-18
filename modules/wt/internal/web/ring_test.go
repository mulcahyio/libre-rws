package web

import (
	"encoding/csv"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mulcahyio/libre-rws/modules/wt/internal/fakedcm"
)

// The LibreDCM audit page (Admin) shows the recent records newest first
// with event, outcome and who, filters by event type, outcome and period,
// says that it shows only what the node keeps in memory and that the
// site's log collector holds the full record, and is itself audited (no
// record, no page). Filter values outside the fixed lists are not sent.
// Verifies: WT-SRS-029, WT-SRS-032, WT-SRS-033, WT-SRS-034
func TestLibreDCMAuditPage(t *testing.T) {
	e := newEnv(t)
	e.users(t)
	b := e.as(t, "admin1")
	code, body, _ := b.get("/i/default/audit")
	if code != 200 {
		t.Fatalf("audit page = %d\n%s", code, body)
	}
	has(t, "audit page", body, "only the most recent audit records", "The site's log collector holds the full audit record",
		"<td>SecurityAlert.association-rejected</td><td>minor-failure</td>", "<td>DICOMInstancesTransferred.received</td><td>success</td><td>AE CT1</td>",
		"<code>studies=[&#34;1.3.6.1.4.1.67686.9.2.1&#34;]</code>", "Export as CSV", `href="/i/default/audit.csv?period=24h&amp;limit=100"`,
		`<td>AE =HYPERLINK(&#34;http://x&#34;)</td>`)
	if strings.Index(body, "association-rejected") > strings.Index(body, "DICOMInstancesTransferred.received") {
		t.Fatal("records are not newest first")
	}
	q := e.dcm.Headers("/control/v1/audit")
	if q == nil {
		t.Fatal("no audit request")
	}
	v := auditOf(t, e, "ldcm-audit.view")
	if len(v) != 1 || v[0].User != "admin1" || v[0].Instance != "default" || v[0].Target != "period=24h limit=100" {
		t.Fatalf("audit of the view = %+v", v)
	}

	// Filters.
	_, body, _ = b.get("/i/default/audit?type=SecurityAlert&outcome=minor-failure&period=1h&limit=250")
	has(t, "filtered", body, `value="SecurityAlert"`, `<option value="minor-failure" selected>`, `<option value="1h" selected>`, `<option value="250" selected>`)
	if v := auditOf(t, e, "ldcm-audit.view"); v[len(v)-1].Target != "type=SecurityAlert outcome=minor-failure period=1h limit=250" {
		t.Fatalf("filter audited as %q", v[len(v)-1].Target)
	}
	// Wrong values fall back to the defaults.
	_, body, _ = b.get("/i/default/audit?type=%3Cscript%3E&outcome=all&period=99y&limit=100000")
	if v := auditOf(t, e, "ldcm-audit.view"); v[len(v)-1].Target != "period=24h limit=100" {
		t.Fatalf("invalid filter audited as %q", v[len(v)-1].Target)
	}
	hasNot(t, "invalid filter", body, "<script>")

	// audit_log_off.
	e.dcm.Set(func(f *fakedcm.Server) { f.Data.AuditLogOff = true })
	_, body, _ = b.get("/i/default/audit")
	has(t, "audit off", body, "audit_log_off", "Nothing matches")

	// No record, no page.
	e.breakAudit(t)
	code, body, _ = b.get("/i/default/audit")
	if code != http.StatusInternalServerError || strings.Contains(body, "SecurityAlert.association-rejected</td>") {
		t.Fatalf("audit page without an audit record = %d", code)
	}
	code, body, _ = b.get("/i/default/logs")
	if code != http.StatusInternalServerError || strings.Contains(body, "outbox publish retry") {
		t.Fatalf("logs page without an audit record = %d", code)
	}
}

// The filter reaches LibreDCM as the query of the ring endpoints.
// Verifies: WT-SRS-029, WT-SRS-030
func TestRingFilterQuery(t *testing.T) {
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	q := ringFilter{Type: "Query", Outcome: "success", Period: "1h", Limit: 250}.query(now)
	if q.Type != "Query" || q.Outcome != "success" || q.Limit != 250 || !q.Since.Equal(now.Add(-time.Hour)) {
		t.Fatalf("query = %+v", q)
	}
	if q := (ringFilter{Period: "all", Level: "warn", Limit: 200}).query(now); !q.Since.IsZero() || q.Level != "warn" {
		t.Fatalf("query = %+v", q)
	}
}

// The log page (Operator and Admin) shows the recent lines newest first
// with the lowest level chosen (default info), says it shows only what the
// node keeps in memory, and is audited. Only an Admin gets the export.
// Verifies: WT-SRS-030, WT-SRS-033, WT-SRS-034
func TestLogsPage(t *testing.T) {
	e := newEnv(t)
	e.users(t)
	op := e.as(t, "op1")
	code, body, _ := op.get("/i/default/logs")
	if code != 200 {
		t.Fatalf("logs = %d\n%s", code, body)
	}
	has(t, "logs", body, "only the most recent log lines", "without audit records", "<td>WARN</td><td>outbox publish retry</td>",
		"<td>ERROR</td>", "<code>attempt=2</code>", `<option value="info" selected>`)
	hasNot(t, "logs", body, "association negotiated", "Export as CSV", "logs.csv")
	if strings.Index(body, "storage write failed") > strings.Index(body, "outbox publish retry") {
		t.Fatal("lines are not newest first")
	}
	_, body, _ = op.get("/i/default/logs?level=debug&period=all&limit=500")
	has(t, "debug", body, "association negotiated")
	_, body, _ = op.get("/i/default/logs?level=error")
	has(t, "error", body, "storage write failed")
	hasNot(t, "error", body, "outbox publish retry")
	if v := auditOf(t, e, "logs.view"); len(v) != 3 || v[0].User != "op1" || v[1].Target != "level=debug period=all limit=500" {
		t.Fatalf("audit of the views = %+v", v)
	}
	_, body, _ = e.as(t, "admin1").get("/i/default/logs")
	has(t, "logs as admin", body, "Export as CSV", `href="/i/default/logs.csv?period=24h&amp;limit=200&amp;level=info"`)
	// The tabs show the pages each role may open.
	_, body, _ = op.get("/i/default/")
	has(t, "operator tabs", body, `href="/i/default/studies"`, `href="/i/default/logs"`)
	hasNot(t, "operator tabs", body, `href="/i/default/audit"`)
	_, body, _ = e.as(t, "viewer1").get("/i/default/")
	hasNot(t, "viewer tabs", body, `href="/i/default/studies"`, `href="/i/default/logs"`, `href="/i/default/audit"`)
}

// The CSV export holds the entries of the filter, as a download, with
// every cell that a spreadsheet would run as a formula made harmless. The
// export is recorded before the file is sent; without the record there is
// no file.
// Verifies: WT-SRS-037, WT-SRS-033
func TestCSVExport(t *testing.T) {
	e := newEnv(t)
	e.users(t)
	b := e.as(t, "admin1")
	code, body, h := b.get("/i/default/audit.csv?period=all")
	if code != 200 || h.Get("Content-Type") != "text/csv; charset=utf-8" ||
		!strings.HasPrefix(h.Get("Content-Disposition"), `attachment; filename="librewt-default-audit-`) {
		t.Fatalf("audit export = %d %v\n%s", code, h, body)
	}
	rows, err := csv.NewReader(strings.NewReader(body)).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || strings.Join(rows[0], ",") != "time,event,outcome,who,details" || rows[1][1] != "SecurityAlert.association-rejected" ||
		rows[1][3] != `AE =HYPERLINK("http://x")` || rows[2][3] != "AE CT1" {
		t.Fatalf("audit csv = %q", rows)
	}
	ex := auditOf(t, e, "ldcm-audit.export")
	if len(ex) != 1 || ex[0].User != "admin1" || ex[0].Detail != "2 rows exported as CSV" || ex[0].Target != "period=all limit=100" {
		t.Fatalf("audit of the export = %+v", ex)
	}

	code, body, h = b.get("/i/default/logs.csv?level=debug&period=all")
	if code != 200 || !strings.HasPrefix(h.Get("Content-Disposition"), `attachment; filename="librewt-default-logs-`) {
		t.Fatalf("logs export = %d", code)
	}
	rows, _ = csv.NewReader(strings.NewReader(body)).ReadAll()
	if len(rows) != 5 || strings.Join(rows[0], ",") != "time,level,message,details" || rows[1][2] != `'+cmd|' /C calc'!A0 storage write failed` {
		t.Fatalf("logs csv = %q", rows)
	}
	if n := len(auditOf(t, e, "logs.export")); n != 1 {
		t.Fatalf("%d log export records", n)
	}

	e.breakAudit(t)
	code, body, h = b.get("/i/default/audit.csv")
	if code != http.StatusInternalServerError || h.Get("Content-Disposition") != "" || strings.Contains(body, "SecurityAlert") ||
		!strings.Contains(body, "nothing was exported") {
		t.Fatalf("export without an audit record = %d %v\n%s", code, h, body)
	}
}

// csvCell prefixes the cells a spreadsheet would run as a formula.
// Verifies: WT-SRS-037
func TestCSVCell(t *testing.T) {
	for in, want := range map[string]string{"=1+1": "'=1+1", "+1": "'+1", "-1": "'-1", "@SUM(A1)": "'@SUM(A1)", "\tx": "'\tx", "\rx": "'\rx",
		"": "", "a=1": "a=1", "PAT-001": "PAT-001"} {
		if got := csvCell(in); got != want {
			t.Fatalf("csvCell(%q) = %q, want %q", in, got, want)
		}
	}
}

// Opening the Study Exceptions list or one exception is audited: the
// pages show patient data. Without the record the page is not shown.
// Verifies: WT-SRS-033
func TestExceptionViewsAudited(t *testing.T) {
	e := newEnv(t)
	e.users(t)
	b := e.as(t, "viewer1")
	if code, _, _ := b.get("/i/default/exceptions"); code != 200 {
		t.Fatalf("exceptions = %d", code)
	}
	if code, _, _ := b.get("/i/default/exceptions/c1"); code != 200 {
		t.Fatalf("exception = %d", code)
	}
	if l, d := auditOf(t, e, "exceptions.view"), auditOf(t, e, "exception.view"); len(l) != 1 || len(d) != 1 || d[0].Target != "c1" || l[0].User != "viewer1" {
		t.Fatalf("audit: list %+v detail %+v", l, d)
	}
	e.breakAudit(t)
	for _, p := range []string{"/i/default/exceptions", "/i/default/exceptions/c1"} {
		code, body, _ := b.get(p)
		if code != http.StatusInternalServerError || strings.Contains(body, "ROE") {
			t.Fatalf("%s without an audit record = %d", p, code)
		}
	}
}
