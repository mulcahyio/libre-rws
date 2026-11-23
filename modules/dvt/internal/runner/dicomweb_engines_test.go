//go:build engines

// DICOMweb positive controls with the real Python worker against LibreDVT's
// Go DICOMweb test server (internal/fakepeer/web.go: net/http,
// mime/multipart, encoding/json; none of the worker's code). Each
// malformed-response control must fail the step; the correct server must
// pass. Runs in the LibreDVT test image only.

package runner

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mulcahyio/libre-rws/modules/dvt/internal/fakepeer"
	"github.com/mulcahyio/libre-rws/modules/dvt/internal/plan"
	"github.com/mulcahyio/libre-rws/modules/dvt/internal/report"
	"github.com/mulcahyio/libre-rws/modules/dvt/internal/result"
	"github.com/mulcahyio/libre-rws/modules/dvt/internal/store"
)

// The fixtures baked into the image (modules/dvt/worker/build_assets.py).
// Values read once with pydicom from the public pydicom samples and
// written here, so the test server never learns them from the worker.
var fixtureTable = []struct {
	file, ts, class, study, series, sop, pid, modality string
}{
	{"ct_explicit_le.dcm", "1.2.840.10008.1.2.1", "1.2.840.10008.5.1.4.1.1.2", "1.3.6.1.4.1.5962.1.2.1.20040119072730.12322", "1.3.6.1.4.1.5962.1.3.1.1.20040119072730.12322", "1.3.6.1.4.1.5962.1.1.1.1.1.20040119072730.12322", "1CT1", "CT"},
	{"ct_j2k.dcm", "1.2.840.10008.1.2.4.91", "1.2.840.10008.5.1.4.1.1.2", "1.2.276.0.7230010.3.1.2.296485376.1.1521713414.1800996", "1.2.276.0.7230010.3.1.3.296485376.1.1521713419.1802493", "1.2.826.0.1.3680043.2.1143.6234428899086018376578420169896863246", "CQ500-CT-310", "CT"},
	{"mr_jpeg_ls.dcm", "1.2.840.10008.1.2.4.80", "1.2.840.10008.5.1.4.1.1.4", "1.3.6.1.4.1.5962.1.2.4.20040826185059.5457", "1.3.6.1.4.1.5962.1.3.4.1.20040826185059.5457", "1.2.826.0.1.3680043.8.498.33398961699581875222868204465690831265", "4MR1", "MR"},
	{"mr_rle.dcm", "1.2.840.10008.1.2.5", "1.2.840.10008.5.1.4.1.1.4", "1.3.6.1.4.1.5962.1.2.4.20040826185059.5457", "1.3.6.1.4.1.5962.1.3.4.1.20040826185059.5457", "1.3.6.1.4.1.5962.1.1.4.1.1.20040826185059.5457", "4MR1", "MR"},
	{"sc_jpeg_baseline.dcm", "1.2.840.10008.1.2.4.50", "1.2.840.10008.5.1.4.1.1.7", "1.2.826.0.1.3680043.8.498.12406831542731051035295345080039845114", "1.2.826.0.1.3680043.8.498.16157229083793556332623330502397121062", "1.2.276.0.7230010.3.1.4.8323329.15150.1506363677.126194", "ID1", "OT"},
	{"sc_jpeg_lossless.dcm", "1.2.840.10008.1.2.4.70", "1.2.840.10008.5.1.4.1.1.7", "1.2.826.0.1.3680043.8.498.12406831542731051035295345080039845114", "1.2.826.0.1.3680043.8.498.16157229083793556332623330502397121062", "1.2.826.0.1.3680043.8.498.49043964482360854182530167603505525116", "ID1", "OT"},
}

const (
	ctStudy  = "1.3.6.1.4.1.5962.1.2.1.20040119072730.12322"
	ctSeries = "1.3.6.1.4.1.5962.1.3.1.1.20040119072730.12322"
	ctSOP    = "1.3.6.1.4.1.5962.1.1.1.1.1.20040119072730.12322"
	mrStudy  = "1.3.6.1.4.1.5962.1.2.4.20040826185059.5457"
	// Synthetic bearer token for the tests (never a real credential).
	webToken = "dvt-synthetic-web-token-0123456789abcdef" // gitleaks:allow (synthetic test value)
)

func fixtures(t *testing.T) []fakepeer.WebInstance {
	t.Helper()
	dir := filepath.Join(os.Getenv("LIBRE_DVT_TEST_ASSETS"), "plans", "fixtures")
	var out []fakepeer.WebInstance
	for _, f := range fixtureTable {
		b, err := os.ReadFile(filepath.Join(dir, f.file))
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, fakepeer.WebInstance{Study: f.study, Series: f.series, SOP: f.sop, Class: f.class, TS: f.ts, Bytes: b,
			Attrs: map[string]string{"00100020": f.pid, "00080060": f.modality, "00080020": "20260101"}})
	}
	return out
}

func startWeb(t *testing.T, cfg *tls.Config, s fakepeer.WebScript) *fakepeer.Web {
	t.Helper()
	w, err := fakepeer.StartWeb(cfg, s)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.Close)
	return w
}

func faults(names ...string) map[string]bool {
	m := map[string]bool{}
	for _, n := range names {
		m[n] = true
	}
	return m
}

func webTargetFor(t *testing.T, url, token string, tl *store.TLS) *store.Target {
	t.Helper()
	tg := &store.Target{Name: "web", Class: store.Lab, Protocol: store.DICOMweb, BaseURL: url, TLS: tl}
	if token != "" {
		f := filepath.Join(t.TempDir(), "token")
		if err := os.WriteFile(f, []byte(token+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		tg.Auth = &store.Auth{Type: store.AuthBearer, TokenFile: f}
	}
	if err := tg.Normalize(); err != nil {
		t.Fatal(err)
	}
	return tg
}

func runWeb(t *testing.T, src string, tg *store.Target) *result.Run {
	t.Helper()
	e := realEngines(t)
	e.Version = "0.2.0"
	p, err := plan.Parse([]byte(src), filepath.Join(os.Getenv("LIBRE_DVT_TEST_ASSETS"), "plans"))
	if err != nil {
		t.Fatal(err)
	}
	rn := &Runner{Engines: e, WorkDir: t.TempDir(), Version: "0.2.0"}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	r := rn.Run(ctx, "id", p, tg, nil)
	for _, s := range r.Steps {
		t.Logf("  %-12s %-8s %s", s.ID, s.Verdict, strings.Join(s.Messages, " | "))
	}
	return r
}

func stepVerdict(r *result.Run, id string) result.Verdict { return stepOf(r, id).Verdict }

const webHappyPlan = `
name: dicomweb happy path
target: web
timeout: 20s
steps:
  - {id: stow, type: stow, files: ["fixtures/*.dcm"], batch_size: 4, verify_retrieve: true}
  - {id: qido-studies, type: qido, level: studies, from_store: true, require_attributes: [PatientID, StudyDate]}
  - {id: qido-series, type: qido, level: series, from_store: true, require_attributes: [Modality]}
  - {id: qido-instances, type: qido, level: instances, from_store: true}
  - {id: qido-patient, type: qido, level: studies, query: {PatientID: 1CT1}, expect_uids: [` + ctStudy + `]}
  - {id: qido-paged, type: qido, level: studies, limit: 2, expect_uids: [` + ctStudy + `, ` + mrStudy + `]}
  - {id: qido-none, type: qido, level: studies, query: {PatientID: NOBODY}, expect_none: true}
  - {id: wado-store, type: wado, from_store: true, compare_pixels: true}
  - {id: wado-ts, type: wado, level: instance, study: ` + ctStudy + `, series: ` + ctSeries + `, instance: ` + ctSOP + `, files: [fixtures/ct_explicit_le.dcm], transfer_syntax: 1.2.840.10008.1.2.1, compare_pixels: true}
  - {id: wado-meta, type: wado, level: study, study: ` + mrStudy + `, files: [fixtures/mr_rle.dcm, fixtures/mr_jpeg_ls.dcm], metadata: true, compare_attributes: [PatientID, Modality]}
`

// Verifies: DVT-SRS-111, DVT-SRS-113, DVT-SRS-114, DVT-SRS-115, DVT-SRS-117, DVT-SRS-118, DVT-SRS-119, DVT-SRS-121, DVT-SRS-123, DVT-SRS-124, DVT-SRS-126, DVT-SRS-128, DVT-SRS-132, DVT-SRS-135, DVT-SRS-136, DVT-SRS-155
func TestRealDICOMwebHappyPath(t *testing.T) {
	fx := fixtures(t)
	w := startWeb(t, nil, fakepeer.WebScript{Known: fx})
	tg := webTargetFor(t, w.URL, webToken, nil)
	r := runWeb(t, webHappyPlan, tg)
	if r.Verdict != result.Pass {
		t.Fatalf("correct DICOMweb server: %s", r.Verdict)
	}
	reqs := w.Requests()
	// DVT-SRS-111: the token on every request (own origin).
	for i, q := range reqs {
		if q.Authorization != "Bearer "+webToken {
			t.Fatalf("request %d (%s %s) Authorization %q", i, q.Method, q.Path, q.Authorization)
		}
	}
	// DVT-SRS-128: byte-identical parts, 6 files in 2 requests of at most 4.
	var posts []fakepeer.WebRequest
	for _, q := range reqs {
		if q.Method == "POST" {
			posts = append(posts, q)
		}
	}
	if len(posts) != 2 || len(posts[0].PartSHA256) != 4 || len(posts[1].PartSHA256) != 2 {
		t.Fatalf("STOW requests: %+v", posts)
	}
	for i, f := range fx {
		sum := sha256.Sum256(f.Bytes)
		if got := append(posts[0].PartSHA256, posts[1].PartSHA256...)[i]; got != hex.EncodeToString(sum[:]) {
			t.Fatalf("part %d differs from %s", i, fixtureTable[i].file)
		}
	}
	// DVT-SRS-155: one request at a time.
	if w.MaxConcurrent() != 1 {
		t.Fatalf("max requests in progress %d, want 1", w.MaxConcurrent())
	}
	// DVT-SRS-118: exchange evidence with redacted Authorization.
	ev := stepOf(r, "qido-studies").Evidence
	x := ev.Exchanges[0]
	if x.HTTP == nil || x.HTTP.Status != 200 || x.HTTP.Body == nil || x.HTTP.Body.SHA256 == "" || x.HTTP.RequestHeaders["Authorization"][0] != result.Redacted || x.DurationMS < 0 {
		t.Fatalf("exchange evidence: %+v", x.HTTP)
	}
	// DVT-SRS-126, -136: per-part and per-file records.
	if n := len(stepOf(r, "wado-store").Evidence.Instances); n != 6 {
		t.Fatalf("wado instances recorded: %d", n)
	}
	st := stepOf(r, "stow").Evidence
	results := 0
	for _, in := range st.Instances {
		if in.RemoteResult == "success" && in.SHA256 != "" && in.File != "" {
			results++
		}
	}
	if results < 6 {
		t.Fatalf("stow per-file records: %+v", st.Instances)
	}
}

const qidoControlPlan = `
name: qido controls
target: web
timeout: 10s
limits: {max_pages: 3, max_json_bytes: 65536, max_response_bytes: 1048576}
steps:
  - {id: q, type: qido, level: studies, query: {PatientID: 1CT1}, expect_uids: [` + ctStudy + `], require_attributes: [PatientID]}
`

const qidoPagedPlan = `
name: qido paging
target: web
timeout: 10s
limits: {max_pages: 3}
steps:
  - {id: q, type: qido, level: studies, limit: 2, expect_uids: [` + ctStudy + `]}
`

// Verifies: DVT-SRS-114, DVT-SRS-115, DVT-SRS-116, DVT-SRS-117, DVT-SRS-150, DVT-SRS-151, DVT-SRS-154
func TestRealQidoMalformedResponsesFail(t *testing.T) {
	fx := fixtures(t)
	other := startWeb(t, nil, fakepeer.WebScript{})
	cases := []struct {
		fault string
		plan  string
		want  result.Verdict
		msg   string
	}{
		{"", qidoControlPlan, result.Pass, ""},
		{"", qidoPagedPlan, result.Pass, ""},
		{fakepeer.FQidoDropFirst, qidoControlPlan, result.Fail, "qido.uid.present"},
		{fakepeer.FQidoCTypeJSON, qidoControlPlan, result.Fail, "qido.content_type"},
		{fakepeer.FQidoBadJSON, qidoControlPlan, result.Fail, "qido.json"},
		{fakepeer.FQidoHTML, qidoControlPlan, result.Fail, "qido.json"},
		{fakepeer.FQido204, qidoControlPlan, result.Fail, "qido.status"},
		{fakepeer.FQido500, qidoControlPlan, result.Fail, "qido.status"},
		{fakepeer.FQidoNoUniqueKey, qidoControlPlan, result.Fail, "qido.object.unique_key"},
		{fakepeer.FQidoWrongPatient, qidoControlPlan, result.Fail, "qido.object.match"},
		{fakepeer.FQidoIgnoreLimit, qidoPagedPlan, result.Fail, "qido.page.size"},
		{fakepeer.FQidoEndless, qidoPagedPlan, result.Fail, "limits.max_pages"},
		{fakepeer.FQidoHugeJSON, qidoControlPlan, result.Fail, "limit error"},
		{fakepeer.FQidoStreamBig, qidoControlPlan, result.Fail, "limit error"},
		{fakepeer.FRedirect, qidoControlPlan, result.Fail, "was not followed"},
		{fakepeer.FHTTPMalformed, qidoControlPlan, result.Fail, "protocol error"},
		{fakepeer.FTruncatedBody, qidoControlPlan, result.Fail, "truncated"},
	}
	for _, c := range cases {
		t.Run(c.fault+"/"+strings.Fields(c.plan)[1], func(t *testing.T) {
			w := startWeb(t, nil, fakepeer.WebScript{Instances: fx, Known: fx, Faults: faults(c.fault), Other: other.URL})
			r := runWeb(t, c.plan, webTargetFor(t, w.URL, "", nil))
			got := stepVerdict(r, "q")
			msg := strings.Join(stepOf(r, "q").Messages, " | ")
			if got != c.want || !strings.Contains(msg, c.msg) {
				t.Fatalf("%s: %s, want %s with %q", c.fault, got, c.want, c.msg)
			}
		})
	}
	if n := len(other.Requests()); n != 0 {
		t.Fatalf("the redirect target received %d request(s)", n)
	}
}

// Verifies: DVT-SRS-120, DVT-SRS-121, DVT-SRS-122, DVT-SRS-123, DVT-SRS-124, DVT-SRS-125, DVT-SRS-127, DVT-SRS-150, DVT-SRS-154
func TestRealWadoMalformedResponsesFail(t *testing.T) {
	fx := fixtures(t)
	wadoCT := func(extra string) string {
		return "name: wado\ntarget: web\ntimeout: 10s\nlimits: {max_parts: 2, max_part_bytes: 1048576}\nsteps:\n  - {id: w, type: wado, level: study, study: " + ctStudy + ", files: [fixtures/ct_explicit_le.dcm]" + extra + "}\n"
	}
	partLimit := "name: wado\ntarget: web\ntimeout: 10s\nlimits: {max_part_bytes: 1024}\nsteps:\n  - {id: w, type: wado, level: study, study: " + ctStudy + ", files: [fixtures/ct_explicit_le.dcm]}\n"
	meta := "name: meta\ntarget: web\ntimeout: 10s\nsteps:\n  - {id: w, type: wado, level: study, study: " + mrStudy + ", files: [fixtures/mr_rle.dcm, fixtures/mr_jpeg_ls.dcm], metadata: true, compare_attributes: [PatientID]}\n"
	cases := []struct {
		fault, plan string
		want        result.Verdict
		msg         string
	}{
		{"", wadoCT(", compare_pixels: true"), result.Pass, ""},
		{"", wadoCT(", transfer_syntax: 1.2.840.10008.1.2.1, compare_pixels: true"), result.Pass, ""},
		{"", meta, result.Pass, ""},
		{fakepeer.FWadoBoundaryData, wadoCT(", compare_pixels: true"), result.Pass, ""},
		{fakepeer.FWadoTruncated, wadoCT(""), result.Fail, "no closing boundary delimiter"},
		{fakepeer.FWadoBadBoundary, wadoCT(""), result.Fail, "does not contain the boundary"},
		{fakepeer.FWadoOctet, wadoCT(""), result.Fail, "wado.part.content_type"},
		{fakepeer.FWadoGarbagePart, wadoCT(""), result.Fail, "not a DICOM Part 10 object"},
		{fakepeer.FWadoDropOne, wadoCT(""), result.Fail, "missing"},
		{fakepeer.FWadoExtraOne, wadoCT(""), result.Fail, "unexpected instance"},
		{fakepeer.FWadoDuplicate, wadoCT(""), result.Fail, "received 2 times"},
		{fakepeer.FWadoIgnoreTS, wadoCT(", transfer_syntax: 1.2.840.10008.1.2.4.70"), result.Fail, "wado.part.transfer_syntax"},
		{fakepeer.FWadoTSHeader, wadoCT(""), result.Fail, "wado.part.ts_header"},
		{fakepeer.FWadoAlterPixel, wadoCT(", compare_pixels: true"), result.Fail, "wado.pixels"},
		{fakepeer.FWado206, wadoCT(""), result.Fail, "wado.status"},
		{fakepeer.FWado404, wadoCT(""), result.Fail, "wado.status"},
		{fakepeer.FWado503, wadoCT(""), result.Fail, "wado.status"},
		{fakepeer.FWadoNotMultipart, wadoCT(""), result.Fail, "multipart"},
		{fakepeer.FWadoManyParts, wadoCT(""), result.Fail, "limit error"},
		{"", partLimit, result.Fail, "limit error"},
		{fakepeer.FMetaDropOne, meta, result.Fail, "wado.metadata.instance"},
		{fakepeer.FMetaWrongPatient, meta, result.Fail, "wado.metadata.attribute"},
	}
	for i, c := range cases {
		t.Run(c.fault+"/"+string(rune('a'+i)), func(t *testing.T) {
			w := startWeb(t, nil, fakepeer.WebScript{Instances: fx, Known: fx, Faults: faults(c.fault)})
			r := runWeb(t, c.plan, webTargetFor(t, w.URL, "", nil))
			got := stepVerdict(r, "w")
			msg := strings.Join(stepOf(r, "w").Messages, " | ")
			if got != c.want || !strings.Contains(msg, c.msg) {
				t.Fatalf("%s: %s, want %s with %q", c.fault, got, c.want, c.msg)
			}
		})
	}
	// DVT-SRS-127: a served instance without a Type 1 attribute fails
	// validation; the original passes.
	broken := filepath.Join(t.TempDir(), "broken.dcm")
	src := filepath.Join(os.Getenv("LIBRE_DVT_TEST_ASSETS"), "plans", "fixtures", "ct_explicit_le.dcm")
	if out, err := exec.Command("python3", "-c", "import pydicom,sys; d=pydicom.dcmread(sys.argv[1]); del d.Modality; d.save_as(sys.argv[2])", src, broken).CombinedOutput(); err != nil {
		t.Fatalf("make broken copy: %v %s", err, out)
	}
	bb, _ := os.ReadFile(broken)
	for name, data := range map[string][]byte{"original": fx[0].Bytes, "missing Modality": bb} {
		served := append([]fakepeer.WebInstance(nil), fx...)
		served[0].Bytes = data
		w := startWeb(t, nil, fakepeer.WebScript{Instances: served, Known: fx})
		r := runWeb(t, wadoCT(", validate: true"), webTargetFor(t, w.URL, "", nil))
		want := result.Pass
		if name != "original" {
			want = result.Fail
		}
		if got := stepVerdict(r, "w"); got != want {
			t.Fatalf("validate %s: %s, want %s", name, got, want)
		}
	}
}

// Verifies: DVT-SRS-129, DVT-SRS-130, DVT-SRS-131, DVT-SRS-132, DVT-SRS-133, DVT-SRS-134, DVT-SRS-151
func TestRealStowControls(t *testing.T) {
	fx := fixtures(t)
	other := startWeb(t, nil, fakepeer.WebScript{})
	two := "name: stow\ntarget: web\ntimeout: 10s\nsteps:\n  - {id: s, type: stow, files: [fixtures/ct_explicit_le.dcm, fixtures/mr_rle.dcm]%s}\n"
	plain := strings.Replace(two, "%s", "", 1)
	cases := []struct {
		fault, plan string
		want        result.Verdict
		msg         string
	}{
		{"", plain, result.Pass, ""},
		{fakepeer.FStowFailOne, plain, result.Fail, "Failure Reason 0xA700"},
		{fakepeer.FStow409, plain, result.Fail, "409"},
		{fakepeer.FStowOmitOne, plain, result.Fail, "stow.instance.referenced"},
		{fakepeer.FStowExtraRef, plain, result.Fail, "stow.instance.extra"},
		{fakepeer.FStowWrongClass, plain, result.Fail, "referenced with SOP Class"},
		{fakepeer.FStow200Failure, plain, result.Fail, "stow.instance.failed"},
		{fakepeer.FStowWarning, plain, result.Fail, "stow.instance.warning"},
		{fakepeer.FStowWarning, strings.Replace(two, "%s", ", allow_warnings: true", 1), result.PassWithWarnings, ""},
		{fakepeer.FStowEmpty200, plain, result.Fail, "not a DICOM JSON object"},
		{fakepeer.FStowXML, plain, result.Fail, "not a DICOM JSON object"},
		{fakepeer.FStowJSONArray, plain, result.Fail, "not a DICOM JSON object"},
		{fakepeer.FStowBadJSON, plain, result.Fail, "not a DICOM JSON object"},
		{fakepeer.FStowOtherOrigin, plain, result.PassWithWarnings, ""},
		{fakepeer.FStow307, plain, result.Fail, "was not followed"},
		{fakepeer.FStowAlterStored, strings.Replace(two, "%s", ", verify_retrieve: true", 1), result.Fail, "stow.verify.pixels"},
	}
	for _, c := range cases {
		t.Run(c.fault+"/"+c.plan[len(c.plan)-25:len(c.plan)-2], func(t *testing.T) {
			w := startWeb(t, nil, fakepeer.WebScript{Known: fx, Faults: faults(c.fault), Other: other.URL})
			r := runWeb(t, c.plan, webTargetFor(t, w.URL, "", nil))
			got := stepVerdict(r, "s")
			msg := strings.Join(stepOf(r, "s").Messages, " | ")
			if got != c.want || !strings.Contains(msg, c.msg) {
				t.Fatalf("%s: %s, want %s with %q", c.fault, got, c.want, c.msg)
			}
			if c.fault == fakepeer.FStow307 {
				n := 0
				for _, q := range w.Requests() {
					if q.Method == "POST" {
						n++
					}
				}
				if n != 1 {
					t.Fatalf("307: %d POSTs, want 1 (not re-sent)", n)
				}
			}
		})
	}
	if n := len(other.Requests()); n != 0 {
		t.Fatalf("another origin received %d request(s) (Retrieve URL followed or redirect re-sent)", n)
	}
	// DVT-SRS-133: a non-DICOM file fails the step and nothing is sent.
	junk := filepath.Join(t.TempDir(), "junk.dcm")
	_ = os.WriteFile(junk, []byte("not DICOM at all"), 0o600)
	w := startWeb(t, nil, fakepeer.WebScript{Known: fx})
	r := runWeb(t, "name: s\ntarget: web\nsteps:\n  - {id: s, type: stow, files: [fixtures/ct_explicit_le.dcm, "+junk+"]}\n", webTargetFor(t, w.URL, "", nil))
	if stepVerdict(r, "s") != result.Fail || len(w.Requests()) != 0 || w.Connections() != 0 {
		t.Fatalf("non-DICOM file: %s, %d requests", stepVerdict(r, "s"), len(w.Requests()))
	}
	// DVT-SRS-134: production and unclassified targets get nothing.
	for _, class := range []string{store.Production, ""} {
		tg := webTargetFor(t, w.URL, "", nil)
		tg.Class = class
		r := runWeb(t, webHappyPlan, tg)
		if r.Verdict != result.Fail || w.Connections() != 0 {
			t.Fatalf("class %q: %s, %d connections", class, r.Verdict, w.Connections())
		}
	}
}

// Verifies: DVT-SRS-100, DVT-SRS-101, DVT-SRS-102, DVT-SRS-104, DVT-SRS-105, DVT-SRS-106, DVT-SRS-107
func TestRealDICOMwebOverTLS(t *testing.T) {
	fx := fixtures(t)
	pki, err := fakepeer.NewPKI(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server, err := pki.Issue("server", fakepeer.LeafOpts{Issuer: pki.Root, CN: "web", IPs: []net.IP{net.ParseIP("127.0.0.1")}})
	if err != nil {
		t.Fatal(err)
	}
	client, err := pki.Issue("client", fakepeer.LeafOpts{Issuer: pki.Root, CN: "libredvt", Client: true})
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(pki.Root.Cert)
	cfg := func(min, max uint16, auth tls.ClientAuthType) *tls.Config {
		return &tls.Config{Certificates: []tls.Certificate{server.TLS}, MinVersion: min, MaxVersion: max, ClientAuth: auth, ClientCAs: pool} // #nosec G402 -- test server with old versions on purpose
	}
	plan := "name: tls\ntarget: web\ntimeout: 10s\nsteps:\n  - {id: q, type: qido, level: studies, query: {PatientID: 1CT1}, expect_uids: [" + ctStudy + "]}\n"
	probe := func(mode string) string {
		if mode == "tls_version" {
			return "name: p\ntarget: web\ntimeout: 10s\nsteps:\n  - {id: p, type: tls_reject, mode: tls_version, tls_version: \"1.1\"}\n"
		}
		return "name: p\ntarget: web\ntimeout: 10s\nsteps:\n  - {id: p, type: tls_reject, mode: " + mode + "}\n"
	}
	trust := &store.TLS{CAFile: pki.Root.File}
	mtls := &store.TLS{CAFile: pki.Root.File, ClientCertFile: client.CertFile, ClientKeyFile: client.KeyFile}

	// A verified TLS 1.2+ connection passes and its facts are recorded.
	w := startWeb(t, cfg(tls.VersionTLS12, 0, tls.NoClientCert), fakepeer.WebScript{Instances: fx})
	r := runWeb(t, plan, webTargetFor(t, w.URL, webToken, trust))
	x := stepOf(r, "q").Evidence.Exchanges[0]
	if r.Verdict != result.Pass || x.TLS == nil || !x.TLS.Verified || x.TLS.Peer == nil || x.TLS.Peer.SHA256 == "" || x.TLS.Version == "" {
		t.Fatalf("TLS qido: %s %+v", r.Verdict, x.TLS)
	}
	// Wrong host (server_name not in the certificate) and unknown CA fail
	// before any request reaches the server.
	for name, tl := range map[string]*store.TLS{
		"wrong host": {CAFile: pki.Root.File, ServerName: "other.test"},
		"unknown CA": {CAFile: pki.OtherCA.File},
	} {
		before := len(w.Requests())
		r := runWeb(t, plan, webTargetFor(t, w.URL, webToken, tl))
		if r.Verdict != result.Fail || len(w.Requests()) != before {
			t.Fatalf("%s: %s, server saw %d request(s)", name, r.Verdict, len(w.Requests())-before)
		}
	}
	// Mutual TLS.
	wm := startWeb(t, cfg(tls.VersionTLS12, 0, tls.RequireAndVerifyClientCert), fakepeer.WebScript{Instances: fx})
	if r := runWeb(t, plan, webTargetFor(t, wm.URL, "", mtls)); r.Verdict != result.Pass {
		t.Fatalf("mutual TLS: %s", r.Verdict)
	}
	if r := runWeb(t, plan, webTargetFor(t, wm.URL, "", trust)); r.Verdict != result.Fail {
		t.Fatalf("no client certificate against a server that requires one: %s", r.Verdict)
	}
	// Refusal probes: correct servers refuse (pass), weak ones accept (fail).
	weak11 := startWeb(t, cfg(tls.VersionTLS11, tls.VersionTLS11, tls.NoClientCert), fakepeer.WebScript{Instances: fx})
	plainBehindHTTPS := startWeb(t, nil, fakepeer.WebScript{Instances: fx})
	for _, c := range []struct {
		name, url, mode string
		tl              *store.TLS
		want            result.Verdict
	}{
		{"plaintext refused", w.URL, "plaintext", trust, result.Pass},
		{"plaintext accepted", strings.Replace(plainBehindHTTPS.URL, "http://", "https://", 1), "plaintext", trust, result.Fail},
		{"TLS 1.1 refused", w.URL, "tls_version", trust, result.Pass},
		{"TLS 1.1 accepted", weak11.URL, "tls_version", trust, result.Fail},
		{"no client cert refused", wm.URL, "no_client_cert", mtls, result.Pass},
		{"no client cert accepted", w.URL, "no_client_cert", trust, result.Fail},
	} {
		r := runWeb(t, probe(c.mode), webTargetFor(t, c.url, webToken, c.tl))
		if got := stepVerdict(r, "p"); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
	// DVT-SRS-106: probes carry no token and no client certificate.
	for _, srv := range []*fakepeer.Web{w, wm, plainBehindHTTPS} {
		for _, q := range srv.Requests() {
			if q.Method == "GET" && strings.HasSuffix(q.Path, "/studies") && q.RawQuery == "limit=1" && q.Authorization != "" {
				t.Fatalf("a probe sent Authorization")
			}
		}
	}
}

// Verifies: DVT-SRS-152, DVT-SRS-153
func TestRealDICOMwebSecretsNeverInArtefacts(t *testing.T) {
	fx := fixtures(t)
	forms := []string{webToken, base64.StdEncoding.EncodeToString([]byte(webToken)), base64.RawURLEncoding.EncodeToString([]byte(webToken))}
	for _, fault := range []string{"", fakepeer.FEchoAuth} {
		w := startWeb(t, nil, fakepeer.WebScript{Known: fx, Instances: fx, Faults: faults(fault)})
		tg := webTargetFor(t, w.URL, webToken, nil)
		r := runWeb(t, webHappyPlan, tg)
		a, hits, err := report.Render(r, SecretsFor(tg, ""))
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range [][]byte{a.JSON, a.HTML, a.JUnit} {
			for _, f := range forms {
				if strings.Contains(string(b), f) {
					t.Fatalf("%q: secret form found in an artefact", fault)
				}
			}
		}
		if fault == fakepeer.FEchoAuth {
			// The server echoed the token in its error page: caught,
			// redacted, and the run fails.
			if r.Verdict != result.Fail || !strings.Contains(string(a.JSON), "secret value(s) found") {
				t.Fatalf("echoed token: verdict %s, hits %d", r.Verdict, hits)
			}
		} else if r.Verdict != result.Pass || hits != 0 {
			t.Fatalf("clean run: %s, hits %d", r.Verdict, hits)
		}
		// The server did receive the token: the scan had something to find.
		if len(w.Requests()) == 0 || w.Requests()[0].Authorization != "Bearer "+webToken {
			t.Fatal("token not sent")
		}
	}
}
