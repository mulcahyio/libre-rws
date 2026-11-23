package runner

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mulcahyio/libre-rws/modules/dvt/internal/engine"
	"github.com/mulcahyio/libre-rws/modules/dvt/internal/result"
	"github.com/mulcahyio/libre-rws/modules/dvt/internal/store"
)

// Unit tests of the DICOMweb assertions: the worker's facts are scripted,
// Go decides (evidence-model.md §2). Real-engine controls against the Go
// DICOMweb test server are in dicomweb_engines_test.go.

const base = "https://pacs.lab.example/dicom-web"

func xh(seq int, method, path string, status int, ctype string) result.Exchange {
	u := base + path
	return result.Exchange{Seq: seq, Protocol: "http", Operation: method, Endpoint: u, Outcome: result.ExchangeCompleted,
		HTTP: &result.HTTPInfo{Method: method, URL: u, Status: status, Body: &result.BodyInfo{ContentType: ctype}}}
}

func obj(tags map[string]string) engine.QidoObject {
	o := engine.QidoObject{Values: map[string][]string{}}
	for k, v := range tags {
		o.Present = append(o.Present, k)
		if v != "" {
			o.Values[k] = []string{v}
		}
	}
	return o
}

func qidoResult(status int, ctype, js string, objs ...engine.QidoObject) engine.QidoResult {
	return engine.QidoResult{
		Exchanges: []result.Exchange{xh(1, "GET", "/studies", status, ctype)},
		Keywords:  map[string]string{"PatientID": "00100020", "StudyDate": "00080020"},
		Query:     []engine.QidoQuery{{Key: "PatientID", Tag: "00100020", VR: "LO", Value: "P1", Exact: true}},
		Pages:     []engine.QidoPage{{Exchange: 1, Status: status, ContentType: ctype, JSON: js, Objects: objs}},
	}
}

func verdictOf(r *result.Run) (result.Verdict, string) {
	return r.Steps[len(r.Steps)-1].Verdict, strings.Join(r.Steps[len(r.Steps)-1].Messages, " | ")
}

const qidoPlan = "name: q\ntarget: web\nsteps:\n  - {id: q, type: qido, level: studies, query: {PatientID: P1}, expect_uids: [1.2.3], require_attributes: [PatientID, StudyDate]}\n"

// Verifies: DVT-SRS-114, DVT-SRS-115, DVT-SRS-116, DVT-SRS-117, DVT-SRS-151
func TestQidoPositiveControls(t *testing.T) {
	good := obj(map[string]string{"0020000D": "1.2.3", "00100020": "P1", "00080020": "20260101"})
	cases := []struct {
		name string
		res  engine.QidoResult
		want result.Verdict
		msg  string
	}{
		{"match", qidoResult(200, "application/dicom+json", "array", good), result.Pass, ""},
		{"charset parameter", qidoResult(200, "application/dicom+json; charset=utf-8", "array", good), result.Pass, ""},
		{"missing expected UID", qidoResult(200, "application/dicom+json", "array", obj(map[string]string{"0020000D": "1.2.4", "00100020": "P1", "00080020": "x"})), result.Fail, "qido.uid.present 1.2.3"},
		{"wrong content type", qidoResult(200, "application/json", "array", good), result.Fail, "qido.content_type"},
		{"malformed JSON", qidoResult(200, "application/dicom+json", "invalid"), result.Fail, "qido.json"},
		{"HTML error page with 200", qidoResult(200, "text/html", "invalid"), result.Fail, "qido.json"},
		{"204 when results expected", qidoResult(204, "", "empty"), result.Fail, "qido.status"},
		{"500", qidoResult(500, "text/plain", "invalid"), result.Fail, "qido.status"},
		{"no unique key", qidoResult(200, "application/dicom+json", "array", obj(map[string]string{"00100020": "P1", "00080020": "x"})), result.Fail, "qido.object.unique_key"},
		{"required attribute missing", qidoResult(200, "application/dicom+json", "array", obj(map[string]string{"0020000D": "1.2.3", "00100020": "P1"})), result.Fail, "StudyDate (00080020) present"},
		{"other Patient ID", qidoResult(200, "application/dicom+json", "array", obj(map[string]string{"0020000D": "1.2.3", "00100020": "P2", "00080020": "x"})), result.Fail, "qido.object.match"},
	}
	redirect := qidoResult(302, "", "empty")
	redirect.Exchanges[0].HTTP.ResponseHeaders = map[string][]string{"Location": {"https://elsewhere.example/x"}}
	cases = append(cases, struct {
		name string
		res  engine.QidoResult
		want result.Verdict
		msg  string
	}{"redirect", redirect, result.Fail, `redirect to "https://elsewhere.example/x" was not followed`})
	limit := qidoResult(200, "application/dicom+json", "array", good)
	limit.Exchanges[0].Outcome = result.ExchangeError
	limit.Exchanges[0].Error = &result.Problem{Kind: result.ProblemLimit, Message: "response body larger than the limit"}
	cases = append(cases, struct {
		name string
		res  engine.QidoResult
		want result.Verdict
		msg  string
	}{"limit", limit, result.Fail, "limit error"})
	for _, c := range cases {
		f := &fake{qidoFn: func(engine.Web, engine.QidoRequest) engine.QidoResult { return c.res }}
		r := run(t, f, setup(t, qidoPlan), webTarget(store.Lab, ""))
		v, msg := verdictOf(r)
		if v != c.want || !strings.Contains(msg, c.msg) {
			t.Errorf("%s: %s, want %s with %q: %s", c.name, v, c.want, c.msg, msg)
		}
	}
	// The worker got the plan's query, keywords and the limits.
	f := &fake{qidoFn: func(engine.Web, engine.QidoRequest) engine.QidoResult {
		return qidoResult(200, "application/dicom+json", "array", good)
	}}
	run(t, f, setup(t, qidoPlan), webTarget(store.Lab, ""))
	if len(f.webs) != 1 || f.webs[0].Limits.MaxPages != 100 || f.webs[0].BaseURL != base {
		t.Fatalf("web options: %+v", f.webs)
	}
}

// Verifies: DVT-SRS-114, DVT-SRS-117
func TestQidoNoMatchAndPaging(t *testing.T) {
	none := "name: q\ntarget: web\nsteps:\n  - {id: q, type: qido, level: studies, query: {PatientID: P9}, expect_none: true}\n"
	for name, c := range map[string]struct {
		res  engine.QidoResult
		want result.Verdict
	}{
		"204":         {qidoResult(204, "", "empty"), result.Pass},
		"200 empty":   {qidoResult(200, "application/dicom+json", "array"), result.Pass},
		"200 object":  {qidoResult(200, "application/dicom+json", "array", obj(map[string]string{"0020000D": "1.2"})), result.Fail},
		"500":         {qidoResult(500, "text/plain", "invalid"), result.Fail},
		"html 200":    {qidoResult(200, "text/html", "invalid"), result.Fail},
		"no response": {engine.QidoResult{Exchanges: []result.Exchange{{Seq: 1, Protocol: "http", Operation: "GET", Endpoint: base, Outcome: result.ExchangeError, Error: &result.Problem{Kind: result.ProblemConnectionClosed, Message: "refused"}, HTTP: &result.HTTPInfo{Method: "GET", URL: base}}}, Pages: []engine.QidoPage{{Exchange: 1}}}, result.Fail},
	} {
		f := &fake{qidoFn: func(engine.Web, engine.QidoRequest) engine.QidoResult { return c.res }}
		if v, msg := verdictOf(run(t, f, setup(t, none), webTarget(store.Lab, ""))); v != c.want {
			t.Errorf("expect_none %s: %s, want %s: %s", name, v, c.want, msg)
		}
	}
	paged := "name: q\ntarget: web\nsteps:\n  - {id: q, type: qido, level: studies, limit: 2, expect_uids: [1.1, 1.2, 1.3]}\n"
	page := func(seq int, uids ...string) (result.Exchange, engine.QidoPage) {
		var os []engine.QidoObject
		for _, u := range uids {
			os = append(os, obj(map[string]string{"0020000D": u}))
		}
		return xh(seq, "GET", fmt.Sprintf("/studies?limit=2&offset=%d", 2*(seq-1)), 200, "application/dicom+json"),
			engine.QidoPage{Exchange: seq, Status: 200, ContentType: "application/dicom+json", JSON: "array", Objects: os}
	}
	build := func(over bool, pages ...[]string) engine.QidoResult {
		r := engine.QidoResult{Keywords: map[string]string{}}
		for i, u := range pages {
			x, p := page(i+1, u...)
			r.Exchanges, r.Pages = append(r.Exchanges, x), append(r.Pages, p)
		}
		r.PageLimitReached = over
		return r
	}
	for name, c := range map[string]struct {
		res  engine.QidoResult
		want result.Verdict
	}{
		"two pages hold all":  {build(false, []string{"1.1", "1.2"}, []string{"1.3"}), result.Pass},
		"page above limit":    {build(false, []string{"1.1", "1.2", "1.3"}), result.Fail},
		"never stops paging":  {build(true, []string{"1.1", "1.2"}, []string{"1.3", "1.4"}), result.Fail},
		"one missing at last": {build(false, []string{"1.1", "1.2"}, []string{}), result.Fail},
	} {
		f := &fake{qidoFn: func(_ engine.Web, q engine.QidoRequest) engine.QidoResult {
			if q.Limit != 2 || q.Offset != 0 {
				t.Errorf("limit/offset %d/%d", q.Limit, q.Offset)
			}
			return c.res
		}}
		if v, msg := verdictOf(run(t, f, setup(t, paged), webTarget(store.Lab, ""))); v != c.want {
			t.Errorf("paging %s: %s, want %s: %s", name, v, c.want, msg)
		}
	}
}

// stowOK answers a stow with every file referenced.
func stowOK(status int, mutate func(*engine.StowRequestRecord)) func(engine.Web, string, int, []string) engine.StowResult {
	return func(_ engine.Web, _ string, _ int, files []string) engine.StowResult {
		r := engine.StowResult{Exchanges: []result.Exchange{xh(1, "POST", "/studies", status, "application/dicom+json")}}
		q := engine.StowRequestRecord{Exchange: 1, Status: status, JSON: "object"}
		for i, f := range files {
			uid := "1.2." + filepath.Base(f)
			r.Files = append(r.Files, engine.StowFile{File: f, SOPInstanceUID: uid, SOPClassUID: "1.2.840.10008.5.1.4.1.1.2",
				StudyInstanceUID: "9.9", SeriesInstanceUID: "9.9.1", TransferSyntax: "1.2.840.10008.1.2.1", Size: 1, SHA256: "s"})
			q.Files = append(q.Files, i)
			q.Referenced = append(q.Referenced, engine.StowRef{SOPClassUID: "1.2.840.10008.5.1.4.1.1.2", SOPInstanceUID: uid, RetrieveURL: base + "/studies/9.9"})
		}
		if mutate != nil {
			mutate(&q)
		}
		r.Requests = []engine.StowRequestRecord{q}
		return r
	}
}

const stowPlan = "name: s\ntarget: web\nsteps:\n  - {id: s, type: stow, files: [\"fx/*.dcm\"]}\n"

// Verifies: DVT-SRS-129, DVT-SRS-130, DVT-SRS-131, DVT-SRS-132, DVT-SRS-136
func TestStowPositiveControls(t *testing.T) {
	type c struct {
		status int
		mut    func(*engine.StowRequestRecord)
		plan   string
		want   result.Verdict
		msg    string
	}
	warn := func(q *engine.StowRequestRecord) { q.Referenced[0].WarningReason = "0xB000" }
	cases := map[string]c{
		"all stored": {200, nil, stowPlan, result.Pass, ""},
		"202 with failed": {202, func(q *engine.StowRequestRecord) {
			q.Failed = []engine.StowFailed{{SOPInstanceUID: q.Referenced[0].SOPInstanceUID, FailureReason: "0xA700"}}
			q.Referenced = q.Referenced[1:]
		}, stowPlan, result.Fail, "Failure Reason 0xA700"},
		"409":         {409, func(q *engine.StowRequestRecord) { q.Referenced = nil }, stowPlan, result.Fail, "stow.status"},
		"UID missing": {200, func(q *engine.StowRequestRecord) { q.Referenced = q.Referenced[1:] }, stowPlan, result.Fail, "stow.instance.referenced"},
		"extra UID": {200, func(q *engine.StowRequestRecord) {
			q.Referenced = append(q.Referenced, engine.StowRef{SOPInstanceUID: "7.7"})
		}, stowPlan, result.Fail, "stow.instance.extra"},
		"SOP class mismatch": {200, func(q *engine.StowRequestRecord) { q.Referenced[0].SOPClassUID = "1.2.840.10008.5.1.4.1.1.4" }, stowPlan, result.Fail, "referenced with SOP Class"},
		"200 also lists failed": {200, func(q *engine.StowRequestRecord) {
			q.Failed = []engine.StowFailed{{SOPInstanceUID: q.Referenced[0].SOPInstanceUID, FailureReason: "0xC000"}}
		}, stowPlan, result.Fail, "stow.instance.failed"},
		"warning not allowed":  {202, warn, stowPlan, result.Fail, "stow.instance.warning"},
		"200 with a warning":   {200, warn, stowPlan, result.Fail, "stow.instance.warning"},
		"other type with refs": {200, func(q *engine.StowRequestRecord) { q.JSON = "other_type" }, stowPlan, result.Fail, "not a DICOM JSON object"},
		"warning allowed":      {202, warn, "name: s\ntarget: web\nsteps:\n  - {id: s, type: stow, files: [\"fx/*.dcm\"], allow_warnings: true}\n", result.PassWithWarnings, ""},
		"top-level warning":    {202, func(q *engine.StowRequestRecord) { q.WarningReason = "0xB007" }, stowPlan, result.Fail, "Warning Reason 0xB007"},
		"empty body 200":       {200, func(q *engine.StowRequestRecord) { q.JSON, q.Referenced = "empty", nil }, stowPlan, result.Fail, "not a DICOM JSON object"},
		"XML body":             {200, func(q *engine.StowRequestRecord) { q.JSON, q.Referenced = "other_type", nil }, stowPlan, result.Fail, "not a DICOM JSON object"},
		"JSON array":           {200, func(q *engine.StowRequestRecord) { q.JSON, q.Referenced = "not_object", nil }, stowPlan, result.Fail, "not a DICOM JSON object"},
		"malformed JSON":       {200, func(q *engine.StowRequestRecord) { q.JSON, q.Referenced = "invalid", nil }, stowPlan, result.Fail, "not a DICOM JSON object"},
		"failed reported as success": {200, func(q *engine.StowRequestRecord) {
			q.Failed = []engine.StowFailed{{SOPInstanceUID: q.Referenced[1].SOPInstanceUID, FailureReason: "0xA700"}}
		}, stowPlan, result.Fail, "stow.instance.failed"},
		"retrieve URL other origin": {200, func(q *engine.StowRequestRecord) { q.Referenced[0].RetrieveURL = "https://evil.example/x" }, stowPlan, result.PassWithWarnings, ""},
	}
	for name, cc := range cases {
		f := &fake{stowFn: stowOK(cc.status, cc.mut)}
		r := run(t, f, setup(t, cc.plan), webTarget(store.Lab, ""))
		v, msg := verdictOf(r)
		if v != cc.want || !strings.Contains(msg, cc.msg) {
			t.Errorf("%s: %s, want %s with %q: %s", name, v, cc.want, cc.msg, msg)
		}
		if strings.Contains(strings.Join(f.calls, ","), "wado") {
			t.Errorf("%s: a Retrieve URL was followed: %v", name, f.calls)
		}
		ev := r.Steps[0].Evidence
		if ev == nil || len(ev.Instances) < 2 {
			t.Errorf("%s: per-file records missing: %+v", name, ev)
		}
	}
}

// Verifies: DVT-SRS-133
func TestStowRefusesNonDICOMWithoutSending(t *testing.T) {
	f := &fakeProbe{fake: fake{stowFn: stowOK(200, nil)}, notDICOM: "b.dcm"}
	rn := &Runner{Engines: f, WorkDir: t.TempDir(), Version: "test"}
	r := rn.Run(context.Background(), "id", setup(t, stowPlan), webTarget(store.Lab, ""), nil)
	v, msg := verdictOf(r)
	if v != result.Fail || strings.Contains(strings.Join(f.calls, ","), "stow") || !strings.Contains(msg, "nothing was sent") {
		t.Fatalf("non-DICOM file: %s calls %v: %s", v, f.calls, msg)
	}
}

// fakeProbe reports one file as not DICOM.
type fakeProbe struct {
	fake
	notDICOM string
}

func (f *fakeProbe) Probe(ctx context.Context, files []string) ([]engine.ProbeFile, error) {
	out, err := f.fake.Probe(ctx, files)
	for i := range out {
		if filepath.Base(out[i].File) == f.notDICOM {
			out[i].DICOM, out[i].Error = false, "not a DICOM file"
		}
	}
	return out, err
}

// Verifies: DVT-SRS-121, DVT-SRS-122, DVT-SRS-123, DVT-SRS-125, DVT-SRS-126, DVT-SRS-127, DVT-SRS-135
func TestStowThenQidoWadoFromStore(t *testing.T) {
	src := `name: s
target: web
steps:
  - {id: s, type: stow, files: ["fx/*.dcm"]}
  - {id: q, type: qido, level: instances, from_store: true}
  - {id: w, type: wado, from_store: true, compare_pixels: true, transfer_syntax: 1.2.840.10008.1.2.1}
`
	part := func(i int, sop, ts, tsParam, ct string) engine.WadoPart {
		return engine.WadoPart{Exchange: 1, Index: i, ContentType: ct, MediaType: ct, TSParam: tsParam, Size: 1, SHA256: "h",
			TransferSyntax: ts, SOPInstanceUID: sop, File: fmt.Sprintf("/w/p%d.dcm", i)}
	}
	wado := func(status int, ctype string, parts ...engine.WadoPart) func(engine.Web, engine.WadoRequest) engine.WadoResult {
		return func(_ engine.Web, q engine.WadoRequest) engine.WadoResult {
			if len(q.Resources) != 1 || q.Resources[0] != "/studies/9.9" || q.TransferSyntax != "1.2.840.10008.1.2.1" {
				t.Errorf("wado request %+v", q)
			}
			x := xh(1, "GET", "/studies/9.9", status, ctype)
			for _, p := range parts {
				x.HTTP.Parts = append(x.HTTP.Parts, result.PartInfo{Index: p.Index})
			}
			return engine.WadoResult{Exchanges: []result.Exchange{x}, Parts: parts}
		}
	}
	ts := "1.2.840.10008.1.2.1"
	a, b := "1.2.a.dcm", "1.2.b.dcm"
	mp := `multipart/related; type="application/dicom"; boundary=x`
	cases := map[string]struct {
		w    func(engine.Web, engine.WadoRequest) engine.WadoResult
		cmp  func([]string) []engine.CompareFile
		want result.Verdict
		msg  string
	}{
		"all back":          {wado(200, mp, part(1, a, ts, "", "application/dicom"), part(2, b, ts, ts, "application/dicom")), nil, result.Pass, ""},
		"one missing":       {wado(200, mp, part(1, a, ts, "", "application/dicom")), nil, result.Fail, "1.2.b.dcm"},
		"one extra":         {wado(200, mp, part(1, a, ts, "", "application/dicom"), part(2, b, ts, "", "application/dicom"), part(3, "7.7", ts, "", "application/dicom")), nil, result.Fail, "unexpected instance"},
		"one twice":         {wado(200, mp, part(1, a, ts, "", "application/dicom"), part(2, b, ts, "", "application/dicom"), part(3, b, ts, "", "application/dicom")), nil, result.Fail, "received 2 times"},
		"octet-stream part": {wado(200, mp, part(1, a, ts, "", "application/octet-stream"), part(2, b, ts, "", "application/dicom")), nil, result.Fail, "wado.part.content_type"},
		"other TS":          {wado(200, mp, part(1, a, "1.2.840.10008.1.2.4.70", "", "application/dicom"), part(2, b, ts, "", "application/dicom")), nil, result.Fail, "wado.part.transfer_syntax"},
		"TS header differs": {wado(200, mp, part(1, a, ts, "1.2.840.10008.1.2", "application/dicom"), part(2, b, ts, "", "application/dicom")), nil, result.Fail, "wado.part.ts_header"},
		"206":               {wado(206, mp, part(1, a, ts, "", "application/dicom"), part(2, b, ts, "", "application/dicom")), nil, result.Fail, "wado.status"},
		"404":               {wado(404, "text/plain"), nil, result.Fail, "wado.status"},
		"503":               {wado(503, "text/plain"), nil, result.Fail, "wado.status"},
		"not multipart":     {wado(200, "application/dicom", part(1, a, ts, "", "application/dicom"), part(2, b, ts, "", "application/dicom")), nil, result.Fail, "wado.content_type"},
		"pixel differs": {wado(200, mp, part(1, a, ts, "", "application/dicom"), part(2, b, ts, "", "application/dicom")), func(sent []string) []engine.CompareFile {
			out := []engine.CompareFile{}
			for i, s := range sent {
				out = append(out, engine.CompareFile{File: s, Match: i != 1, Error: "pixel data differs"})
			}
			return out
		}, result.Fail, "pixel data differs"},
	}
	for name, c := range cases {
		f := &fake{stowFn: stowOK(200, nil), wadoFn: c.w, compare: c.cmp,
			qidoFn: func(_ engine.Web, q engine.QidoRequest) engine.QidoResult {
				// DVT-SRS-135: exactly the stowed instances, one search each.
				if strings.Join(q.UIDFilter, ",") != a+","+b || q.Level != "instances" {
					t.Errorf("qido from_store request %+v", q)
				}
				r := engine.QidoResult{Keywords: map[string]string{}}
				for i, u := range q.UIDFilter {
					r.Exchanges = append(r.Exchanges, xh(i+1, "GET", "/instances?00080018="+u, 200, "application/dicom+json"))
					r.Pages = append(r.Pages, engine.QidoPage{Exchange: i + 1, Status: 200, ContentType: "application/dicom+json", JSON: "array", Objects: []engine.QidoObject{obj(map[string]string{"00080018": u})}})
				}
				return r
			}}
		r := run(t, f, setup(t, src), webTarget(store.Lab, ""))
		if stepOf(r, "q").Verdict != result.Pass {
			t.Errorf("%s: qido from_store %s %v", name, stepOf(r, "q").Verdict, stepOf(r, "q").Messages)
		}
		v, msg := verdictOf(r)
		if v != c.want || !strings.Contains(msg, c.msg) {
			t.Errorf("%s: %s, want %s with %q: %s", name, v, c.want, c.msg, msg)
		}
	}
}

// Verifies: DVT-SRS-124
func TestWadoMetadata(t *testing.T) {
	src := "name: m\ntarget: web\nsteps:\n  - {id: w, type: wado, level: study, study: 9.9, files: [\"fx/*.dcm\"], metadata: true, compare_attributes: [PatientID]}\n"
	meta := func(objs []engine.MetaObject, origPID string) func(engine.Web, engine.WadoRequest) engine.WadoResult {
		return func(_ engine.Web, q engine.WadoRequest) engine.WadoResult {
			if !q.Metadata || len(q.ExpectFiles) != 2 || q.Attributes[0] != "PatientID" {
				t.Errorf("metadata request %+v", q)
			}
			r := engine.WadoResult{Exchanges: []result.Exchange{xh(1, "GET", "/studies/9.9/metadata", 200, "application/dicom+json")},
				Metadata: objs, Keywords: map[string]string{"PatientID": "00100020"}}
			for _, f := range q.ExpectFiles {
				r.Originals = append(r.Originals, engine.Original{File: f, SOPInstanceUID: "1.2." + filepath.Base(f), Attrs: map[string]string{"00100020": origPID}})
			}
			return r
		}
	}
	m := func(sop, pid string) engine.MetaObject {
		return engine.MetaObject{Exchange: 1, StudyInstanceUID: "9.9", SeriesInstanceUID: "9.9.1", SOPInstanceUID: sop, Attrs: map[string]string{"00100020": pid}}
	}
	for name, c := range map[string]struct {
		w    func(engine.Web, engine.WadoRequest) engine.WadoResult
		want result.Verdict
	}{
		"match":              {meta([]engine.MetaObject{m("1.2.a.dcm", `["P1"]`), m("1.2.b.dcm", `["P1"]`)}, `["P1"]`), result.Pass},
		"instance missing":   {meta([]engine.MetaObject{m("1.2.a.dcm", `["P1"]`)}, `["P1"]`), result.Fail},
		"Patient ID changed": {meta([]engine.MetaObject{m("1.2.a.dcm", `["P1"]`), m("1.2.b.dcm", `["CHANGED"]`)}, `["P1"]`), result.Fail},
		"other study":        {meta([]engine.MetaObject{m("1.2.a.dcm", `["P1"]`), {Exchange: 1, StudyInstanceUID: "8.8", SOPInstanceUID: "1.2.b.dcm", Attrs: map[string]string{"00100020": `["P1"]`}}}, `["P1"]`), result.Fail},
	} {
		f := &fake{wadoFn: c.w}
		if v, msg := verdictOf(run(t, f, setup(t, src), webTarget(store.Lab, ""))); v != c.want {
			t.Errorf("%s: %s, want %s: %s", name, v, c.want, msg)
		}
	}
}

// Verifies: DVT-SRS-132
func TestStowVerifyRetrieve(t *testing.T) {
	src := "name: s\ntarget: web\nsteps:\n  - {id: s, type: stow, files: [\"fx/*.dcm\"], verify_retrieve: true}\n"
	for name, match := range map[string]bool{"identical": true, "pixels changed": false} {
		var reqs []engine.WadoRequest
		f := &fake{stowFn: stowOK(200, nil),
			wadoFn: func(_ engine.Web, q engine.WadoRequest) engine.WadoResult {
				reqs = append(reqs, q)
				r := engine.WadoResult{}
				for i, res := range q.Resources {
					sop := res[strings.LastIndex(res, "/")+1:]
					x := xh(i+1, "GET", res, 200, `multipart/related; type="application/dicom"; boundary=b`)
					x.HTTP.Parts = []result.PartInfo{{Index: 1}}
					r.Exchanges = append(r.Exchanges, x)
					r.Parts = append(r.Parts, engine.WadoPart{Exchange: i + 1, Index: 1, ContentType: "application/dicom", SHA256: "h", SOPInstanceUID: sop, File: "/w/" + sop})
				}
				return r
			},
			compare: func(sent []string) []engine.CompareFile {
				var out []engine.CompareFile
				for _, s := range sent {
					out = append(out, engine.CompareFile{File: s, Match: match})
				}
				return out
			}}
		r := run(t, f, setup(t, src), webTarget(store.Lab, ""))
		want := result.Pass
		if !match {
			want = result.Fail
		}
		if v, msg := verdictOf(r); v != want {
			t.Errorf("%s: %s, want %s: %s", name, v, want, msg)
		}
		if len(reqs) != 1 || len(reqs[0].Resources) != 2 || reqs[0].Resources[0] != "/studies/9.9/series/9.9.1/instances/1.2.a.dcm" {
			t.Errorf("%s: verify requests %+v", name, reqs)
		}
		// Exchange numbers continue across the two worker calls.
		ev := r.Steps[0].Evidence
		for i, x := range ev.Exchanges {
			if x.Seq != i+1 {
				t.Errorf("%s: exchange %d has seq %d", name, i+1, x.Seq)
			}
		}
	}
}

// Verifies: DVT-SRS-112
func TestBearerOverClearTextRefusedBeforeEngine(t *testing.T) {
	tg := &store.Target{Name: "web", Class: store.Lab, Protocol: store.DICOMweb, BaseURL: "http://pacs.lab.example/dw",
		Auth: &store.Auth{Type: store.AuthBearer, TokenFile: secretFile(t, "dvt-synthetic-token-0123456789")}} // gitleaks:allow (synthetic test value)
	f := &fake{}
	r := run(t, f, setup(t, qidoPlan), tg)
	if v, msg := verdictOf(r); v != result.Fail || len(f.calls) != 0 || !strings.Contains(msg, "loopback") {
		t.Fatalf("token over http off-host: %s %v %s", v, f.calls, msg)
	}
	// The same on a loopback host reaches the engine.
	tg.BaseURL = "http://127.0.0.1:8042/dw"
	f = &fake{qidoFn: func(engine.Web, engine.QidoRequest) engine.QidoResult {
		return qidoResult(200, "application/dicom+json", "array", obj(map[string]string{"0020000D": "1.2.3", "00100020": "P1", "00080020": "x"}))
	}}
	r = run(t, f, setup(t, qidoPlan), tg)
	if v, msg := verdictOf(r); v != result.Pass || len(f.webs) != 1 || f.webs[0].TokenFile == "" {
		t.Fatalf("loopback: %s %+v %s", v, f.webs, msg)
	}
}
