package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/mulcahyio/libre-rws/modules/wt/internal/dcmclient"
	"github.com/mulcahyio/libre-rws/modules/wt/internal/fakedcm"
)

const (
	study1  = "1.2.826.0.1.3680043.10.1.1"
	study2  = "1.2.826.0.1.3680043.10.1.2"
	series1 = "1.2.826.0.1.3680043.10.1.1.1"
)

const notViewer = "Libre WT is not a diagnostic viewer"

// search is the study search form.
func search(csrf string, kv ...string) url.Values {
	v := url.Values{"csrf": {csrf}}
	for i := 0; i+1 < len(kv); i += 2 {
		v.Set(kv[i], kv[i+1])
	}
	return v
}

// lastQuery returns the last study query a fake node received.
func lastQuery(t *testing.T, n *fakedcm.Server) url.Values {
	t.Helper()
	q := n.Queries()
	if len(q) == 0 {
		t.Fatal("no study query reached the node")
	}
	return q[len(q)-1]
}

// The study search sends the values by POST, the person's name with the
// query, and the DICOM matching keys the form asks for: wildcards, a date
// range, a modality. A search without a value is refused and never sent.
// The search is in the Libre WT audit log; when the record can't be
// written, nothing is searched. LibreDCM's text is shown as text.
// Verifies: WT-SRS-025, WT-SRS-032, WT-SRS-033
func TestStudySearch(t *testing.T) {
	e := newEnv(t)
	e.users(t)
	b := e.as(t, "op1")
	csrf := b.csrf()
	code, body, _ := b.get("/i/default/studies")
	if code != 200 {
		t.Fatalf("search page = %d", code)
	}
	has(t, "search page", body, notViewer, `method="post" action="/i/default/studies/search"`)
	if len(e.dcm.Queries()) != 0 {
		t.Fatal("opening the empty form sent a query")
	}

	code, body, _ = b.post("/i/default/studies/search", search(csrf, "patient_id", "PAT-*"))
	if code != 200 {
		t.Fatalf("search = %d\n%s", code, body)
	}
	has(t, "results", body, notViewer, "2 studies", "<b>PAT-001</b>", "DOE JANE", "1970-01-01", "2026-11-01", "ACC-1001", "CT CHEST",
		`href="/i/default/studies/`+study1+`"`, "<b>PAT-002</b>", "Instances held as a patient conflict are not listed")
	q := lastQuery(t, e.dcm)
	if q.Get("operator") != "op1" || q.Get("PatientID") != "PAT-*" || q.Get("level") != "study" || q.Get("limit") != "50" {
		t.Fatalf("query = %v", q)
	}
	s := auditOf(t, e, "studies.search")
	if len(s) != 1 || s[0].User != "op1" || s[0].Instance != "default" || s[0].Target != "PatientID=PAT-*" || s[0].Outcome != "success" {
		t.Fatalf("audit of the search = %+v", s)
	}

	// Date range, lower-case modality, name with a wildcard.
	_, body, _ = b.post("/i/default/studies/search", search(csrf, "date_from", "2026-11-02", "date_to", "2026-11-30", "modality", "mr", "patient_name", "DOE^*"))
	q = lastQuery(t, e.dcm)
	if q.Get("StudyDate") != "20261102-20261130" || q.Get("ModalitiesInStudy") != "MR" || q.Get("PatientName") != "DOE^*" {
		t.Fatalf("query = %v", q)
	}
	has(t, "range", body, "1 study", "<b>PAT-002</b>")
	hasNot(t, "range", body, "<b>PAT-001</b>")
	_, _, _ = b.post("/i/default/studies/search", search(csrf, "date_from", "20261101", "date_to", "2026-11-01"))
	if q = lastQuery(t, e.dcm); q.Get("StudyDate") != "20261101" {
		t.Fatalf("one day = %v", q)
	}
	_, _, _ = b.post("/i/default/studies/search", search(csrf, "date_to", "2026-11-01"))
	if q = lastQuery(t, e.dcm); q.Get("StudyDate") != "-20261101" {
		t.Fatalf("open range = %v", q)
	}

	// Refused before anything is sent.
	before := len(e.dcm.Queries())
	for _, c := range []struct {
		form url.Values
		text string
	}{
		{search(csrf), "give at least one search value"},
		{search(csrf, "patient_id", "*", "patient_name", "**"), "give at least one search value"},
		{search(csrf, "date_from", "2026-13-01"), "From is not a date"},
		{search(csrf, "date_from", "2026-11-05", "date_to", "2026-11-01"), "the date From is after the date To"},
		{search(csrf, "patient_id", `P1\P2`), "can&#39;t be searched for"},
		{search(csrf, "modality", "C T"), "Modality must be"},
		{search(csrf, "study_uid", "1.2.x"), "Study Instance UID must be"},
		{search(csrf, "patient_id", strings.Repeat("A", 65)), "longer than 64"},
	} {
		code, body, _ := b.post("/i/default/studies/search", c.form)
		if code != http.StatusUnprocessableEntity || !strings.Contains(body, c.text) || !strings.Contains(body, "Nothing was searched") {
			t.Fatalf("%v = %d\n%s", c.form, code, body)
		}
	}
	if n := len(e.dcm.Queries()); n != before {
		t.Fatalf("%d refused search(es) reached the node", n-before)
	}

	// LibreDCM's refusal is shown as text.
	e.dcm.Set(func(f *fakedcm.Server) {
		f.Cfg.Fail["/control/v1/studies"] = fakedcm.Failure{Status: 429, Code: "busy", Msg: "too many <b>queries</b>"}
	})
	code, body, _ = b.post("/i/default/studies/search", search(csrf, "patient_id", "PAT-001"))
	if code != http.StatusBadGateway || !strings.Contains(body, "too many &lt;b&gt;queries&lt;/b&gt;") {
		t.Fatalf("refused search = %d\n%s", code, body)
	}
	if s := auditOf(t, e, "studies.search"); s[len(s)-1].Outcome != "failure" {
		t.Fatalf("refused search audited as %+v", s[len(s)-1])
	}
	e.dcm.Set(func(f *fakedcm.Server) { f.Cfg.Fail = map[string]fakedcm.Failure{} })

	// Another node answered: nothing of the answer is shown.
	e.dcm.Set(func(f *fakedcm.Server) { f.Cfg.OtherNode["/control/v1/studies"] = true })
	code, body, _ = b.post("/i/default/studies/search", search(csrf, "patient_id", "PAT-001"))
	if code != http.StatusConflict || strings.Contains(body, "DOE JANE") {
		t.Fatalf("answer of another node = %d\n%s", code, body)
	}
	e.dcm.Set(func(f *fakedcm.Server) { f.Cfg.OtherNode = map[string]bool{} })

	// No audit record, no search.
	before = len(e.dcm.Queries())
	e.breakAudit(t)
	code, body, _ = b.post("/i/default/studies/search", search(csrf, "patient_id", "PAT-001"))
	if code != http.StatusInternalServerError || strings.Contains(body, "PAT-001</b>") {
		t.Fatalf("search without an audit record = %d\n%s", code, body)
	}
	if len(e.dcm.Queries()) != before {
		t.Fatal("a search reached the node without an audit record")
	}
}

// When more studies match than one page holds, the page says so and the
// next page carries LibreDCM's cursor with the same search.
// Verifies: WT-SRS-025
func TestStudySearchMore(t *testing.T) {
	e := newEnv(t)
	e.users(t)
	e.dcm.Set(func(f *fakedcm.Server) {
		base := f.Data.Studies[0]
		for i := 0; i < 60; i++ {
			st := fakedcm.Study{Row: map[string]string{}, Series: base.Series}
			for k, v := range base.Row {
				st.Row[k] = v
			}
			st.Row["StudyInstanceUID"] = base.Row["StudyInstanceUID"] + ".9." + itoa(int64(i))
			f.Data.Studies = append(f.Data.Studies, st)
		}
	})
	b := e.as(t, "op1")
	csrf := b.csrf()
	_, body, _ := b.post("/i/default/studies/search", search(csrf, "patient_id", "PAT-001"))
	has(t, "page 1", body, "50 studies", "More studies match than are shown (50 shown)", `name="cursor" value="c50"`, "Next page")
	_, body, _ = b.post("/i/default/studies/search", search(csrf, "patient_id", "PAT-001", "cursor", "c50"))
	has(t, "page 2", body, "11 studies (next page)")
	hasNot(t, "page 2", body, "More studies match")
	if q := lastQuery(t, e.dcm); q.Get("cursor") != "c50" || q.Get("PatientID") != "PAT-001" {
		t.Fatalf("next page query = %v", q)
	}
}

// The study page shows the patient and the series; the series page lists
// the instances by identifier only. Both openings are audited. Only an
// Admin gets the send form; a study held as a patient conflict, or listed
// under two patients, gets none.
// Verifies: WT-SRS-025, WT-SRS-026, WT-SRS-033, WT-SRS-034
func TestStudyAndSeriesPages(t *testing.T) {
	e := newEnv(t)
	e.users(t)
	op := e.as(t, "op1")
	code, body, _ := op.get("/i/default/studies/" + study1)
	if code != 200 {
		t.Fatalf("study page = %d\n%s", code, body)
	}
	has(t, "study", body, notViewer, "<b>PAT-001</b>", "<b>DOE JANE</b>", "1970-01-01", "CT CHEST", "ACC-1001", "2 / 5",
		"AXIAL", "CORONAL", `href="/i/default/studies/`+study1+`/series/`+series1+`"`)
	hasNot(t, "study as operator", body, "/send/review", "Send to a destination")
	q := lastQuery(t, e.dcm)
	if q.Get("level") != "series" || q.Get("StudyInstanceUID") != study1 || q.Get("operator") != "op1" {
		t.Fatalf("series query = %v", q)
	}
	code, body, _ = op.get("/i/default/studies/" + study1 + "/series/" + series1)
	if code != 200 {
		t.Fatalf("series page = %d", code)
	}
	has(t, "series", body, notViewer, series1+".1", series1+".3", "1.2.840.10008.5.1.4.1.1.2", "1.2.840.10008.1.2.1")
	if strings.Count(body, "<img") != 1 { // the logo only
		t.Fatal("an image on the series page")
	}
	q = lastQuery(t, e.dcm)
	if q.Get("level") != "image" || q.Get("SeriesInstanceUID") != series1 || q.Get("StudyInstanceUID") != study1 {
		t.Fatalf("instance query = %v", q)
	}
	if v := auditOf(t, e, "studies.view"); len(v) != 2 || v[0].Target != study1 || v[1].Target != study1+" series "+series1 || v[0].User != "op1" {
		t.Fatalf("audit of the views = %+v", v)
	}
	for _, bad := range []string{"/i/default/studies/1.2.x", "/i/default/studies/" + study1 + "/series/abc"} {
		if code, _, _ := op.get(bad); code != http.StatusNotFound {
			t.Fatalf("%s = %d", bad, code)
		}
	}

	ad := e.as(t, "admin1")
	_, body, _ = ad.get("/i/default/studies/" + study1)
	has(t, "study as admin", body, "Send to a destination", `action="/i/default/studies/`+study1+`/send/review"`,
		`name="patient_id" value="PAT-001"`, `<option value="VIEWER1">VIEWER1: VIEWER1 at 10.0.0.20:104</option>`,
		`name="series" value="`+series1+`"`)

	// Held as a patient conflict (flagged visibility): no send form.
	e.dcm.Set(func(f *fakedcm.Server) {
		f.Data.Held = "flagged"
		f.Data.Studies[1].Row["ConflictID"] = "c9"
	})
	_, body, _ = ad.get("/i/default/studies/" + study2)
	has(t, "held study", body, "held: patient conflict", "can't be sent from the console", "Resolve it in Study Exceptions first")
	hasNot(t, "held study", body, "/send/review")
	// The same UID under two patients: no send form.
	e.dcm.Set(func(f *fakedcm.Server) {
		dup := fakedcm.Study{Row: map[string]string{}}
		for k, v := range f.Data.Studies[0].Row {
			dup.Row[k] = v
		}
		dup.Row["PatientID"] = "PAT-777"
		f.Data.Studies = append(f.Data.Studies, dup)
	})
	_, body, _ = ad.get("/i/default/studies/" + study1)
	has(t, "two patients", body, "under more than one patient", "lists this Study Instance UID 2 times")
	hasNot(t, "two patients", body, "/send/review")
	// A node without the send function: no send form.
	e.dcm.Set(func(f *fakedcm.Server) {
		f.Data.Studies = f.Data.Studies[:2]
		f.Features = []string{"conflicts", "status", "studies", "remote-aes"}
	})
	_, body, _ = ad.get("/i/default/studies/" + study1)
	hasNot(t, "no send function", body, "/send/review")
	// A node without the study function: a plain "not available" page.
	e.dcm.Set(func(f *fakedcm.Server) { f.Features = []string{"conflicts", "status"} })
	code, body, _ = ad.get("/i/default/studies")
	if code != 200 || !strings.Contains(body, "does not offer this function") {
		t.Fatalf("no study function = %d\n%s", code, body)
	}
}

// The study and series pages show patient data: without an audit record
// of the opening, the page is not shown and LibreDCM is not asked.
// Verifies: WT-SRS-025, WT-SRS-033
func TestStudyPagesNeedAuditRecord(t *testing.T) {
	e := newEnv(t)
	e.users(t)
	b := e.as(t, "op1")
	e.breakAudit(t)
	for _, p := range []string{"/i/default/studies/" + study1, "/i/default/studies/" + study1 + "/series/" + series1} {
		code, body, _ := b.get(p)
		if code != http.StatusInternalServerError || strings.Contains(body, "DOE") || strings.Contains(body, series1+".1") {
			t.Fatalf("%s without an audit record = %d\n%s", p, code, body)
		}
	}
	if n := len(e.dcm.Queries()); n != 0 {
		t.Fatalf("%d queries without an audit record", n)
	}
}

// sendForm is the send form of the study page.
func sendForm(csrf, patient, dest string, series ...string) url.Values {
	v := url.Values{"csrf": {csrf}, "patient_id": {patient}, "destination": {dest}}
	for _, s := range series {
		v.Add("series", s)
	}
	return v
}

// The send: the warning page shows the patient (name, ID, birth date)
// prominently and more than once, the study, the series and instance
// counts and the destination's address, as LibreDCM planned them. The
// approval names the patient. The apply step sends exactly what was
// reviewed (form fields added at the approval are ignored) with the
// operator, reason, request ID, expected node and LibreDCM's plan digest.
// Review, approval (before the send) and result are in the audit log.
// Verifies: WT-SRS-026, WT-SRS-031, WT-SRS-033
func TestSendFlow(t *testing.T) {
	e := newEnv(t)
	e.users(t)
	b := e.as(t, "admin1")
	csrf := b.csrf()
	confirm, body := b.review("/i/default/studies/"+study1+"/send/review", sendForm(csrf, "PAT-001", "VIEWER1"))
	has(t, "send review", body, "Check the patient before you send", `<b class="diff">DOE JANE</b>`, `<b class="diff">PAT-001</b>`,
		"born 1970-01-01", "2026-11-01", "CT CHEST", "ACC-1001", study1, "the whole study", "2 series, 5 instance(s)",
		"<b>VIEWER1</b>, AE title <code>VIEWER1</code> at <code>10.0.0.20:104</code>", "<b>default</b>",
		"send 5 instance(s) of <b>DOE JANE</b> (Patient ID <b>PAT-001</b>) to VIEWER1", series1)
	// The patient stands out: name and ID appear in the warning, the
	// table and the approval box.
	if strings.Count(body, "DOE JANE") < 3 || strings.Count(body, "<b>PAT-001</b>") < 2 || strings.Count(body, "PAT-001") < 3 {
		t.Fatalf("the patient is not shown prominently:\n%s", body)
	}
	plans := e.dcm.Posts("/control/v1/send/plan")
	if len(plans) != 1 || plans[0]["patient_id"] != "PAT-001" || plans[0]["study_instance_uid"] != study1 || plans[0]["destination"] != "VIEWER1" ||
		plans[0]["expect_node_id"] != "node-default" || plans[0]["series_instance_uids"] != nil {
		t.Fatalf("plan request = %v", plans)
	}
	if len(e.dcm.Posts("/control/v1/send")) != 0 {
		t.Fatal("the review sent something")
	}
	// The approval form tries to change what is sent: ignored.
	form := approval(csrf, confirm, "radiologist at site B asked")
	form.Set("destination", "OTHER")
	form.Set("patient_id", "PAT-002")
	form.Set("study_instance_uid", study2)
	form.Set("series", "1.2.3")
	code, body, _ := b.post("/i/default/studies/"+study1+"/send/apply", form)
	if code != 200 {
		t.Fatalf("send = %d\n%s", code, body)
	}
	has(t, "send result", body, `<span class="state success">complete</span>`, "accepted all 5 instance(s)", "Patient ID <b>PAT-001</b>",
		"<b>VIEWER1</b>", "10.0.0.20:104", "Every other instance of the plan (5) was accepted")
	sends := e.dcm.Posts("/control/v1/send")
	if len(sends) != 1 {
		t.Fatalf("%d sends", len(sends))
	}
	s := sends[0]
	envelopeOK(t, "send", s, "admin1", "radiologist at site B asked", "node-default", "AE_DEFAULT")
	if s["destination"] != "VIEWER1" || s["patient_id"] != "PAT-001" || s["study_instance_uid"] != study1 || s["series_instance_uids"] != nil ||
		s["confirm"] != true || !strings.HasPrefix(s["plan_digest"].(string), "sha256:") {
		t.Fatalf("send request = %v", s)
	}
	rev, appr, res := auditOf(t, e, "send.review"), auditOf(t, e, "send.approved"), auditOf(t, e, "send")
	if len(rev) != 1 || len(appr) != 1 || len(res) != 1 || appr[0].ID >= res[0].ID || appr[0].Target != study1 ||
		!strings.Contains(appr[0].Detail, "patient PAT-001 (DOE^JANE)") || !strings.Contains(appr[0].Detail, "reason: radiologist at site B asked") ||
		res[0].Outcome != "success" || !strings.Contains(res[0].Detail, "5 completed, 0 failed") {
		t.Fatalf("audit: review %+v approved %+v result %+v", rev, appr, res)
	}

	// Chosen series only.
	confirm, body = b.review("/i/default/studies/"+study1+"/send/review", sendForm(csrf, "PAT-001", "VIEWER1", series1, series1))
	has(t, "series review", body, "the chosen series only", "1 series, 3 instance(s)")
	if code, _, _ := b.post("/i/default/studies/"+study1+"/send/apply", approval(csrf, confirm, "one series")); code != 200 {
		t.Fatalf("series send = %d", code)
	}
	sends = e.dcm.Posts("/control/v1/send")
	if got, _ := sends[1]["series_instance_uids"].([]any); len(got) != 1 || got[0] != series1 {
		t.Fatalf("series send = %v", sends[1])
	}
}

// The wrong-patient guard. A Patient ID that is not the study's is refused
// by LibreDCM and no approval is issued. A plan about another patient,
// study or destination than the one chosen is refused by Libre WT. The
// approval is bound to the plan: when the study changes after the review,
// LibreDCM refuses the send and nothing is sent. The approval works once,
// only for its instance and its study, only with the box ticked and a
// reason.
// Verifies: WT-SRS-026, WT-SRS-031
func TestSendWrongPatientGuard(t *testing.T) {
	e := newFleetEnv(t, "a", "b")
	e.users(t)
	n := e.nodes["a"]
	ad := e.as(t, "admin1")
	csrf := ad.csrf()
	sendPath := "/i/a/studies/" + study1 + "/send/"
	noConfirm := func(what, body string) {
		t.Helper()
		if confirmRE.MatchString(body) {
			t.Fatalf("%s: an approval was issued:\n%s", what, body)
		}
	}
	// Another patient's ID for this study.
	code, body, _ := ad.post(sendPath+"review", sendForm(csrf, "PAT-002", "VIEWER1"))
	if code != http.StatusUnprocessableEntity || !strings.Contains(body, "not indexed under this Patient ID") || !strings.Contains(body, "Nothing was changed") {
		t.Fatalf("patient mismatch = %d\n%s", code, body)
	}
	noConfirm("patient mismatch", body)
	// A destination that is not configured.
	code, body, _ = ad.post(sendPath+"review", sendForm(csrf, "PAT-001", "NOWHERE"))
	if code != http.StatusNotFound || !strings.Contains(body, "no destination with this name") {
		t.Fatalf("unknown destination = %d\n%s", code, body)
	}
	noConfirm("unknown destination", body)
	// The plan names another patient, study or destination than the one
	// chosen, has no digest, holds no instance, or comes from another
	// node: no approval.
	for _, c := range []struct {
		what string
		set  func(*fakedcm.Server)
		code int
		text string
	}{
		{"another patient", func(f *fakedcm.Server) { f.Data.PlanPatient = "PAT-999" }, http.StatusConflict, "names another patient, study or destination"},
		{"another study", func(f *fakedcm.Server) { f.Data.PlanStudy = study2 }, http.StatusConflict, "names another patient, study or destination"},
		{"another destination", func(f *fakedcm.Server) { f.Data.PlanDest = "VIEWER9" }, http.StatusConflict, "names another patient, study or destination"},
		{"no digest", func(f *fakedcm.Server) { f.Data.NoDigest = true }, http.StatusBadGateway, "plan without a digest"},
		{"no instance", func(f *fakedcm.Server) {
			for i := range f.Data.Studies[0].Series {
				f.Data.Studies[0].Series[i].Instances = nil
			}
		}, http.StatusUnprocessableEntity, "holds no instance"},
		{"another node", func(f *fakedcm.Server) { f.Cfg.OtherNode["/control/v1/send/plan"] = true }, http.StatusConflict, "Another node answered"},
	} {
		saved := fakedcm.Data{}
		n.Set(func(f *fakedcm.Server) {
			saved = f.Data
			saved.Studies = append([]fakedcm.Study(nil), f.Data.Studies...)
			saved.Studies[0].Series = append([]fakedcm.Series(nil), f.Data.Studies[0].Series...)
			c.set(f)
		})
		code, body, _ = ad.post(sendPath+"review", sendForm(csrf, "PAT-001", "VIEWER1"))
		if code != c.code || !strings.Contains(body, c.text) || !strings.Contains(body, "Nothing was sent") {
			t.Fatalf("%s = %d\n%s", c.what, code, body)
		}
		noConfirm(c.what, body)
		n.Set(func(f *fakedcm.Server) { f.Data, f.Cfg.OtherNode = saved, map[string]bool{} })
	}
	// Missing values.
	for _, f := range []url.Values{sendForm(csrf, "", "VIEWER1"), sendForm(csrf, "PAT-001", ""), sendForm(csrf, "PAT-001", "VIEWER1", "1.2.x")} {
		code, body, _ := ad.post(sendPath+"review", f)
		if code != http.StatusUnprocessableEntity {
			t.Fatalf("%v = %d", f, code)
		}
		noConfirm("missing value", body)
	}
	if len(n.Posts("/control/v1/send")) != 0 {
		t.Fatal("a send reached the node")
	}

	// The study changes after the review: LibreDCM's plan digest no longer
	// fits; nothing is sent.
	confirm, _ := ad.review(sendPath+"review", sendForm(csrf, "PAT-001", "VIEWER1"))
	n.Set(func(f *fakedcm.Server) {
		f.Data.Studies[0].Series[0].Instances = append(f.Data.Studies[0].Series[0].Instances, map[string]string{"SOPInstanceUID": series1 + ".99"})
	})
	code, body, _ = ad.post(sendPath+"apply", approval(csrf, confirm, "x"))
	if code != http.StatusConflict || !strings.Contains(body, "Nothing was changed") || strings.Contains(body, "complete") {
		t.Fatalf("send after the study changed = %d\n%s", code, body)
	}
	if res := auditOf(t, e, "send"); len(res) != 1 || res[0].Outcome != "failure" {
		t.Fatalf("audit = %+v", res)
	}

	// Once only; on its own instance and study only; box and reason.
	completed := func() int {
		t.Helper()
		return len(auditOf(t, e, "send.approved"))
	}
	start := completed()
	confirm, _ = ad.review(sendPath+"review", sendForm(csrf, "PAT-001", "VIEWER1"))
	if code, body, _ := ad.post("/i/b/studies/"+study1+"/send/apply", approval(csrf, confirm, "x")); code != http.StatusConflict ||
		!strings.Contains(body, "expired or was already used") {
		t.Fatalf("approval on another instance = %d", code)
	}
	confirm, _ = ad.review(sendPath+"review", sendForm(csrf, "PAT-001", "VIEWER1"))
	if code, body, _ := ad.post("/i/a/studies/"+study2+"/send/apply", approval(csrf, confirm, "x")); code != http.StatusConflict ||
		!strings.Contains(body, "given for another study") {
		t.Fatalf("approval on another study = %d\n%s", code, body)
	}
	confirm, _ = ad.review(sendPath+"review", sendForm(csrf, "PAT-001", "VIEWER1"))
	f := approval(csrf, confirm, "x")
	f.Del("approve")
	if code, _, _ := ad.post(sendPath+"apply", f); code != http.StatusUnprocessableEntity {
		t.Fatalf("send without the box = %d", code)
	}
	confirm, _ = ad.review(sendPath+"review", sendForm(csrf, "PAT-001", "VIEWER1"))
	if code, _, _ := ad.post(sendPath+"apply", approval(csrf, confirm, "  ")); code != http.StatusUnprocessableEntity {
		t.Fatalf("send without a reason = %d", code)
	}
	if completed() != start {
		t.Fatal("a refused approval was recorded as approved")
	}
	confirm, _ = ad.review(sendPath+"review", sendForm(csrf, "PAT-001", "VIEWER1"))
	if code, _, _ := ad.post(sendPath+"apply", approval(csrf, confirm, "x")); code != 200 {
		t.Fatalf("send = %d", code)
	}
	if code, body, _ := ad.post(sendPath+"apply", approval(csrf, confirm, "x")); code != http.StatusConflict || !strings.Contains(body, "already used") {
		t.Fatalf("second use = %d", code)
	}
	if len(n.Posts("/control/v1/send")) != 2 || len(e.nodes["b"].Posts("/control/v1/send")) != 0 {
		t.Fatalf("sends: a=%d b=%d", len(n.Posts("/control/v1/send")), len(e.nodes["b"].Posts("/control/v1/send")))
	}

	// An Operator can neither review nor send.
	op := e.as(t, "op1")
	ocsrf := op.csrf()
	if code, _, _ := op.post(sendPath+"review", sendForm(ocsrf, "PAT-001", "VIEWER1")); code != http.StatusForbidden {
		t.Fatalf("operator review = %d", code)
	}
	// While another node answers, nothing is reviewed.
	n.Set(func(f *fakedcm.Server) { f.NodeID = "node-imposter" })
	code, body, _ = ad.post(sendPath+"review", sendForm(csrf, "PAT-001", "VIEWER1"))
	if code != http.StatusConflict {
		t.Fatalf("review on another node = %d\n%s", code, body)
	}
	noConfirm("another node", body)
}

// A send of which part, or nothing, arrived is shown per instance and
// never as complete; without an answer the outcome is unknown. Without
// an audit record of the approval, nothing is sent.
// Verifies: WT-SRS-026, WT-SRS-033
func TestSendResults(t *testing.T) {
	e := newFleetEnv(t, "default", "mute-1")
	e.users(t)
	b := e.as(t, "admin1")
	csrf := b.csrf()
	path := "/i/default/studies/" + study1 + "/send/"
	e.dcm.Set(func(f *fakedcm.Server) { f.Data.FailUIDs = map[string]bool{series1 + ".2": true} })
	confirm, _ := b.review(path+"review", sendForm(csrf, "PAT-001", "VIEWER1"))
	code, body, _ := b.post(path+"apply", approval(csrf, confirm, "x"))
	if code != 200 {
		t.Fatalf("partial = %d", code)
	}
	has(t, "partial", body, `<span class="state open">partial</span>`, "Only part of the study arrived: 4 accepted, 1 failed",
		"<code class=\"uid\">"+series1+".2</code></td><td>failed</td>", "Every other instance of the plan (4)")
	hasNot(t, "partial", body, "complete")
	if r := auditOf(t, e, "send"); r[len(r)-1].Outcome != "partial" || !strings.Contains(r[len(r)-1].Detail, series1+".2") {
		t.Fatalf("audit = %+v", r[len(r)-1])
	}
	e.dcm.Set(func(f *fakedcm.Server) {
		f.Data.FailUIDs = map[string]bool{}
		for _, s := range f.Data.Studies[0].Series {
			for _, in := range s.Instances {
				f.Data.FailUIDs[in["SOPInstanceUID"]] = true
			}
		}
	})
	confirm, _ = b.review(path+"review", sendForm(csrf, "PAT-001", "VIEWER1"))
	code, body, _ = b.post(path+"apply", approval(csrf, confirm, "x"))
	if code != http.StatusBadGateway {
		t.Fatalf("failed send = %d", code)
	}
	has(t, "failed", body, `<span class="state open">failed</span>`, "No instance was accepted by the destination (5 failed)", series1+".1")
	if r := auditOf(t, e, "send"); r[len(r)-1].Outcome != "failure" {
		t.Fatalf("audit = %+v", r[len(r)-1])
	}

	// No answer: the outcome is not known.
	confirm, _ = b.review("/i/mute-1/studies/"+study1+"/send/review", sendForm(csrf, "PAT-001", "VIEWER1"))
	code, body, _ = b.post("/i/mute-1/studies/"+study1+"/send/apply", approval(csrf, confirm, "x"))
	if code != http.StatusGatewayTimeout || !strings.Contains(body, "Send: outcome not known") || !strings.Contains(body, "request ID") {
		t.Fatalf("send without an answer = %d\n%s", code, body)
	}
	if r := auditOf(t, e, "send"); r[len(r)-1].Outcome != "unknown" {
		t.Fatalf("audit = %+v", r[len(r)-1])
	}

	// No audit record of the approval: nothing is sent.
	sent := len(e.dcm.Posts("/control/v1/send"))
	confirm, _ = b.review(path+"review", sendForm(csrf, "PAT-001", "VIEWER1"))
	e.breakAudit(t)
	code, body, _ = b.post(path+"apply", approval(csrf, confirm, "x"))
	if code != http.StatusInternalServerError || !strings.Contains(body, "nothing was sent") {
		t.Fatalf("send without an audit record = %d\n%s", code, body)
	}
	if len(e.dcm.Posts("/control/v1/send")) != sent {
		t.Fatal("a send without an audit record")
	}
}

func (mute) Send(context.Context, dcmclient.SendRequest, string, dcmclient.Envelope) (dcmclient.SendResult, error) {
	return dcmclient.SendResult{}, errMute
}

// The search form turns into LibreDCM matching keys.
// Verifies: WT-SRS-025
func TestSearchKeys(t *testing.T) {
	f := searchForm{PatientID: " P1 ", PatientName: "DOE^J*", DateFrom: "2026-01-02", Modality: "ct", Accession: "*", StudyUID: "1.2.3"}
	k, err := f.keys()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"PatientID": "P1", "PatientName": "DOE^J*", "StudyDate": "20260102-", "ModalitiesInStudy": "CT", "StudyInstanceUID": "1.2.3"}
	if len(k) != len(want) {
		t.Fatalf("keys = %v", k)
	}
	for kw, v := range want {
		if k[kw] != v {
			t.Fatalf("keys = %v", k)
		}
	}
	if keysText(k) != "ModalitiesInStudy=CT PatientID=P1 PatientName=DOE^J* StudyDate=20260102- StudyInstanceUID=1.2.3" {
		t.Fatalf("text = %q", keysText(k))
	}
	for _, bad := range []searchForm{{}, {PatientID: "***"}, {DateFrom: "2026-02-30"}, {PatientName: "A\tB"}} {
		if _, err := bad.keys(); err == nil {
			t.Fatalf("%+v accepted", bad)
		}
	}
}
