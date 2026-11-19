package runner

import (
	"context"
	"fmt"
	"mime"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/mulcahyio/libre-rws/modules/dvt/internal/engine"
	"github.com/mulcahyio/libre-rws/modules/dvt/internal/plan"
	"github.com/mulcahyio/libre-rws/modules/dvt/internal/result"
	"github.com/mulcahyio/libre-rws/modules/dvt/internal/store"
)

// Unique keys of the QIDO-RS levels (PS3.18 Table 10.6.1-5).
var uniqueKey = map[string]string{"studies": "0020000D", "series": "0020000E", "instances": "00080018"}

const (
	mediaDICOMJSON = "application/dicom+json"
	mediaDICOM     = "application/dicom"
	mediaMultipart = "multipart/related"
)

// web turns a DICOMweb target into the worker's options: paths and names
// only (DVT-SRS-153), and the plan's limits (DVT-SRS-154).
func (rn *Runner) web(p *plan.Plan, t *store.Target) engine.Web {
	w := engine.Web{BaseURL: t.BaseURL, Timeout: p.Timeout, TLS: engineTLS(t)}
	if t.Auth != nil && t.Auth.Type == store.AuthBearer {
		w.TokenFile = t.Auth.TokenFile
	}
	l := p.Limits
	w.Limits = engine.Limits{MaxResponseBytes: l.MaxResponseBytes, MaxParts: l.MaxParts, MaxPartBytes: l.MaxPartBytes,
		MaxJSONBytes: l.MaxJSONBytes, MaxPages: l.MaxPages, MaxBodyExcerptBytes: l.MaxBodyExcerptBytes}
	return w
}

// checkWebPreflight refuses what must never reach the network: a bearer
// token over clear text off the host (DVT-SRS-112). Normalize rejects such
// a target already; this is the second line.
func checkWebPreflight(t *store.Target) error {
	if t.Auth == nil || t.Auth.Type != store.AuthBearer {
		return nil
	}
	u, err := url.Parse(t.BaseURL)
	if err != nil {
		return fmt.Errorf("base_url: %v", err)
	}
	if u.Scheme != "https" && !store.IsLoopback(u.Hostname()) {
		return fmt.Errorf("a bearer token over %s is only sent to a loopback host; nothing was sent", u.Scheme)
	}
	return nil
}

func mediaType(ct string) (string, map[string]string) {
	mt, params, err := mime.ParseMediaType(ct)
	if err != nil {
		return strings.ToLower(strings.TrimSpace(strings.SplitN(ct, ";", 2)[0])), map[string]string{}
	}
	return mt, params
}

func statusStr(n int) string {
	if n == 0 {
		return "no response"
	}
	return strconv.Itoa(n)
}

// webExchanges copies the worker's exchanges into the evidence: every
// failed exchange is a problem (DVT-SRS-150), every redirect is a problem
// with its Location (DVT-SRS-151).
func webExchanges(ev *result.Evidence, xs []result.Exchange) {
	ev.Exchanges = append(ev.Exchanges, renumber(xs, len(ev.Exchanges))...)
	for _, x := range xs {
		if x.Error != nil {
			ev.Problemf(x.Error.Kind, "%s %s: %s", x.Operation, x.Endpoint, x.Error.Message)
		}
		if x.HTTP != nil && x.HTTP.Status >= 300 && x.HTTP.Status < 400 {
			loc := ""
			for k, v := range x.HTTP.ResponseHeaders {
				if strings.EqualFold(k, "Location") && len(v) > 0 {
					loc = v[0]
				}
			}
			ev.Problemf(result.ProblemProtocol, "%s %s: HTTP %d redirect to %q was not followed", x.Operation, x.Endpoint, x.HTTP.Status, loc)
		}
	}
}

// renumber shifts exchange sequence numbers when one step makes several
// worker calls (stow with verify_retrieve).
func renumber(xs []result.Exchange, offset int) []result.Exchange {
	out := make([]result.Exchange, len(xs))
	for i, x := range xs {
		x.Seq += offset
		out[i] = x
	}
	return out
}

// sentFor lists what earlier store or stow steps sent, narrowed to a study
// and series when given.
func (st *sent) sentFor(study, series string) []sentInstance {
	var out []sentInstance
	for _, s := range st.list {
		if (study == "" || s.Study == study) && (series == "" || s.Series == series) {
			out = append(out, s)
		}
	}
	return out
}

// --- qido (DVT-SRS-113…-118) ---

func (rn *Runner) qido(ctx context.Context, p *plan.Plan, ps plan.Step, t *store.Target, st *sent, s *result.Step) {
	ev := result.NewEvidence(t.Info())
	req := engine.QidoRequest{Level: ps.Level, Study: ps.Study, Series: ps.Series, IncludeField: ps.IncludeField,
		Fuzzy: ps.FuzzyMatching, Keywords: ps.RequireAttributes}
	keys := make([]string, 0, len(ps.Query))
	for k := range ps.Query {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		req.Query = append(req.Query, [2]string{k, ps.Query[k]})
	}
	if ps.Limit != nil {
		req.Limit = *ps.Limit
		if ps.Offset != nil {
			req.Offset = *ps.Offset
		}
	}
	// The UIDs the step expects at its level.
	expected := ps.ExpectUIDs
	if ps.FromStore {
		set := map[string]bool{}
		for _, x := range st.sentFor(ps.Study, ps.Series) {
			uid := map[string]string{"studies": x.Study, "series": x.Series, "instances": x.SOP}[ps.Level]
			if uid != "" && !set[uid] {
				set[uid] = true
				expected = append(expected, uid)
			}
		}
		sort.Strings(expected)
		if len(expected) == 0 {
			ev.Problemf(result.ProblemConfig, "nothing to search for: no earlier store or stow step sent an object at level %s", ps.Level)
			s.ApplyEvidence(ev)
			return
		}
		req.UIDFilter = expected
	}
	res, _, err := rn.Engines.Qido(ctx, rn.web(p, t), req)
	if err != nil {
		ev.Problemf(result.ProblemEngine, "engine error: %v", err)
		s.ApplyEvidence(ev)
		return
	}
	if res.ConfigError != "" {
		ev.Problemf(result.ProblemConfig, "%s", res.ConfigError)
		s.ApplyEvidence(ev)
		return
	}
	webExchanges(ev, res.Exchanges)
	if res.PageLimitReached {
		ev.Problemf(result.ProblemLimit, "the target was still returning full pages after limits.max_pages (%d) pages", p.Limits.MaxPages)
	}
	checkQidoPages(ev, ps, res, expected)
	s.ApplyEvidence(ev)
	n := 0
	for _, pg := range res.Pages {
		n += len(pg.Objects)
	}
	s.Messages = append(s.Messages, fmt.Sprintf("%d search request(s), %d object(s) returned at level %s", len(res.Pages), n, ps.Level))
	addWebTLSNote(s, ev.Exchanges)
}

// checkQidoPages applies DVT-SRS-114…-117 to the worker's facts.
func checkQidoPages(ev *result.Evidence, ps plan.Step, res engine.QidoResult, expected []string) {
	key := uniqueKey[ps.Level]
	found := map[string]bool{}
	var objects []engine.QidoObject
	for _, pg := range res.Pages {
		subj := res.Exchanges[pg.Exchange-1].Endpoint
		if res.Exchanges[pg.Exchange-1].Outcome != result.ExchangeCompleted {
			continue // already a problem
		}
		mt, _ := mediaType(pg.ContentType)
		switch {
		case ps.ExpectNone:
			ok := pg.Status == 204 || (pg.Status == 200 && (pg.JSON == "empty" || (pg.JSON == "array" && len(pg.Objects) == 0)))
			ev.AssertIf("qido.none", subj, "204, or 200 with an empty JSON array", fmt.Sprintf("%s, %s, %d object(s)", statusStr(pg.Status), pg.JSON, len(pg.Objects)), ok, "")
			if pg.Status == 200 && pg.JSON != "empty" {
				ev.Assert("qido.content_type", subj, mediaDICOMJSON, mt, "")
			}
			continue
		default:
			ev.Assert("qido.status", subj, "200", statusStr(pg.Status), "PS3.18: 200 with matches; 204 means no match")
		}
		if pg.Status != 200 {
			continue
		}
		ev.Assert("qido.content_type", subj, mediaDICOMJSON, mt, "")
		ev.AssertIf("qido.json", subj, "JSON array of DICOM JSON objects", pg.JSON+" "+pg.Detail, pg.JSON == "array", "PS3.18 Annex F")
		if ps.Limit != nil {
			ev.AssertIf("qido.page.size", subj, fmt.Sprintf("at most %d objects", *ps.Limit), strconv.Itoa(len(pg.Objects)), len(pg.Objects) <= *ps.Limit, "")
		}
		objects = append(objects, pg.Objects...)
		for _, o := range pg.Objects {
			for _, v := range o.Values[key] {
				found[v] = true
			}
		}
	}
	if ps.ExpectNone {
		return
	}
	// DVT-SRS-115: the level's unique key and every required attribute.
	missingKey, missingAttr := 0, 0
	for i, o := range objects {
		subj := fmt.Sprintf("object %d", i+1)
		if v := o.Values[key]; len(v) == 0 || v[0] == "" {
			missingKey++
			ev.AssertIf("qido.object.unique_key", subj, key+" with a value", "missing", false, "")
		} else {
			subj = v[0]
		}
		present := map[string]bool{}
		for _, tg := range o.Present {
			present[tg] = true
		}
		for _, k := range ps.RequireAttributes {
			tag := res.Keywords[k]
			if !present[tag] {
				missingAttr++
				ev.AssertIf("qido.object.attribute", subj, k+" ("+tag+") present", "missing", false, "")
			}
		}
	}
	if len(objects) > 0 && missingKey == 0 {
		ev.AssertIf("qido.object.unique_key", ps.Level, key+" in every object", fmt.Sprintf("%d of %d", len(objects), len(objects)), true, "")
	}
	if len(objects) > 0 && len(ps.RequireAttributes) > 0 && missingAttr == 0 {
		ev.AssertIf("qido.object.attribute", ps.Level, strings.Join(ps.RequireAttributes, ", ")+" in every object", fmt.Sprintf("%d of %d", len(objects), len(objects)), true, "")
	}
	// DVT-SRS-116: single-value, non-wildcard query keys must match.
	for _, q := range res.Query {
		if !q.Exact {
			continue
		}
		bad := 0
		for i, o := range objects {
			if !valueMatches(o.Values[q.Tag], q.Value, q.VR) {
				bad++
				subj := fmt.Sprintf("object %d", i+1)
				if v := o.Values[key]; len(v) > 0 {
					subj = v[0]
				}
				ev.AssertIf("qido.object.match", subj, q.Key+" = "+q.Value, strings.Join(o.Values[q.Tag], "\\"), false, "returned object does not match the query")
			}
		}
		if bad == 0 && len(objects) > 0 {
			ev.AssertIf("qido.object.match", q.Key, q.Key+" = "+q.Value+" in every object", fmt.Sprintf("%d of %d", len(objects), len(objects)), true, "")
		}
	}
	// DVT-SRS-114: every expected UID is present.
	for _, u := range expected {
		got := "missing"
		if found[u] {
			got = "present"
		}
		ev.Assert("qido.uid.present", u, "present", got, "")
	}
}

// valueMatches applies single value matching (PS3.4 C.2.2.2.1): exact,
// except Person Name, which LibreDVT compares case-insensitively.
func valueMatches(values []string, want, vr string) bool {
	for _, v := range values {
		if v == want || (vr == "PN" && strings.EqualFold(v, want)) {
			return true
		}
	}
	return false
}

// --- wado (DVT-SRS-119…-127) ---

// expectation is what a retrieval must return.
type expectation struct {
	sops      map[string]bool   // SOP Instance UIDs expected
	files     []string          // originals, for pixels and attributes
	studyOf   map[string]string // SOP -> study, series (metadata check)
	seriesOf  map[string]string
	resources []string
}

func (rn *Runner) wado(ctx context.Context, p *plan.Plan, ps plan.Step, t *store.Target, st *sent, s *result.Step) {
	ev := result.NewEvidence(t.Info())
	exp := expectation{sops: map[string]bool{}, studyOf: map[string]string{}, seriesOf: map[string]string{}}
	if ps.FromStore {
		studies := map[string]bool{}
		for _, x := range st.list {
			exp.sops[x.SOP] = true
			exp.files = append(exp.files, x.File)
			exp.studyOf[x.SOP], exp.seriesOf[x.SOP] = x.Study, x.Series
			studies[x.Study] = true
		}
		for _, k := range sortedKeys(studies) {
			exp.resources = append(exp.resources, "/studies/"+k)
		}
	} else {
		files, err := p.ResolveFiles(ps.Files)
		if err != nil {
			ev.Problemf(result.ProblemConfig, "%v", err)
			s.ApplyEvidence(ev)
			return
		}
		probes, err := rn.Engines.Probe(ctx, files)
		if err != nil {
			ev.Problemf(result.ProblemEngine, "engine error: %v", err)
			s.ApplyEvidence(ev)
			return
		}
		for _, pr := range probes {
			if !pr.DICOM || pr.SOPInstanceUID == "" {
				ev.Problemf(result.ProblemConfig, "%s: not a DICOM instance with a SOP Instance UID", rel(p, pr.File))
				continue
			}
			exp.sops[pr.SOPInstanceUID] = true
			exp.files = append(exp.files, pr.File)
			exp.studyOf[pr.SOPInstanceUID], exp.seriesOf[pr.SOPInstanceUID] = pr.StudyInstanceUID, pr.SeriesInstanceUID
		}
		res := "/studies/" + ps.Study
		if ps.Level == "series" || ps.Level == "instance" {
			res += "/series/" + ps.Series
		}
		if ps.Level == "instance" {
			res += "/instances/" + ps.Instance
		}
		exp.resources = []string{res}
	}
	if len(ev.Errors) == 0 && len(exp.sops) == 0 {
		ev.Problemf(result.ProblemConfig, "nothing to retrieve: no earlier store or stow step sent an instance")
	}
	if len(ev.Errors) > 0 {
		s.ApplyEvidence(ev)
		return
	}
	if ps.Metadata {
		rn.metadata(ctx, p, ps, t, ev, exp)
	} else {
		rn.retrieve(ctx, p, t, ev, s, exp, retrieveOpts{ts: ps.TransferSyntax, pixels: ps.ComparePixels, validate: ps.ValidateObjects, prefix: "wado"})
	}
	s.ApplyEvidence(ev)
	addWebTLSNote(s, ev.Exchanges)
}

type retrieveOpts struct {
	ts       string
	pixels   bool
	validate bool
	prefix   string // assertion id prefix: wado or stow.verify
}

// retrieve runs a WADO-RS instance retrieval and its checks (DVT-SRS-120…
// -123, -125…-127). Received files live in a scratch directory removed
// when the checks are done.
func (rn *Runner) retrieve(ctx context.Context, p *plan.Plan, t *store.Target, ev *result.Evidence, s *result.Step, exp expectation, o retrieveOpts) {
	if err := os.MkdirAll(rn.WorkDir, 0o750); err != nil {
		ev.Problemf(result.ProblemEngine, "work directory: %v", err)
		return
	}
	dir, err := os.MkdirTemp(rn.WorkDir, "wado-")
	if err != nil {
		ev.Problemf(result.ProblemEngine, "work directory: %v", err)
		return
	}
	defer os.RemoveAll(dir)
	res, _, err := rn.Engines.Wado(ctx, rn.web(p, t), engine.WadoRequest{Resources: exp.resources, TransferSyntax: o.ts, OutDir: dir})
	if err != nil {
		ev.Problemf(result.ProblemEngine, "engine error: %v", err)
		return
	}
	if res.ConfigError != "" {
		ev.Problemf(result.ProblemConfig, "%s", res.ConfigError)
		return
	}
	base := len(ev.Exchanges)
	webExchanges(ev, res.Exchanges)
	for _, x := range res.Exchanges {
		if x.HTTP == nil || x.HTTP.Status == 0 {
			continue
		}
		subj := x.Endpoint
		// DVT-SRS-125: only 200; 206 and every other status fail.
		ev.Assert(o.prefix+".status", subj, "200", statusStr(x.HTTP.Status), "")
		if x.HTTP.Status == 200 && x.Outcome == result.ExchangeCompleted {
			mt, _ := mediaType(x.HTTP.Body.ContentType)
			ev.Assert(o.prefix+".content_type", subj, mediaMultipart, mt, "")
		}
	}
	got := map[string]int{}
	var received []string
	for _, pt := range res.Parts {
		subj := fmt.Sprintf("%s part %d", res.Exchanges[pt.Exchange-1].Endpoint, pt.Index)
		// Go checks the part's media type itself as well (DVT-SRS-120).
		mt, _ := mediaType(pt.ContentType)
		ev.Assert(o.prefix+".part.content_type", subj, mediaDICOM, mt, "")
		if pt.Error != "" {
			ev.Problemf(result.ProblemProtocol, "%s: %s", subj, pt.Error)
			continue
		}
		if pt.SOPInstanceUID == "" {
			ev.Problemf(result.ProblemProtocol, "%s: no SOP Instance UID", subj)
			continue
		}
		got[pt.SOPInstanceUID]++
		received = append(received, pt.File)
		// DVT-SRS-122: transfer syntax as requested; header and File Meta agree.
		if o.ts != "" && o.ts != "*" {
			ev.Assert(o.prefix+".part.transfer_syntax", pt.SOPInstanceUID, o.ts, pt.TransferSyntax, "File Meta Transfer Syntax UID")
		}
		if pt.TSParam != "" {
			ev.Assert(o.prefix+".part.ts_header", pt.SOPInstanceUID, pt.TransferSyntax, pt.TSParam, "transfer-syntax parameter of the part's Content-Type")
		}
		ev.Instances = append(ev.Instances, result.Instance{SOPInstanceUID: pt.SOPInstanceUID, SOPClassUID: pt.SOPClassUID,
			TransferSyntax: pt.TransferSyntax, Size: pt.Size, SHA256: pt.SHA256, RemoteResult: "success"})
		if x := &ev.Exchanges[base+pt.Exchange-1]; x.HTTP != nil && len(x.HTTP.Parts) < pt.Index {
			ev.Problemf(result.ProblemEngine, "%s: part facts missing from the exchange record", subj)
		}
	}
	// DVT-SRS-121: exactly the expected set.
	setOK := true
	for _, u := range sortedKeys(exp.sops) {
		switch got[u] {
		case 1:
		case 0:
			setOK = false
			ev.AssertIf(o.prefix+".uid.set", u, "received once", "missing", false, "")
			ev.Instances = append(ev.Instances, result.Instance{SOPInstanceUID: u, RemoteResult: "missing"})
		default:
			setOK = false
			ev.AssertIf(o.prefix+".uid.set", u, "received once", fmt.Sprintf("received %d times", got[u]), false, "")
			markInstances(ev, u, "duplicate")
		}
	}
	for _, u := range sortedIntKeys(got) {
		if !exp.sops[u] {
			setOK = false
			ev.AssertIf(o.prefix+".uid.set", u, "not expected", "received", false, "unexpected instance")
			markInstances(ev, u, "extra")
		}
	}
	if setOK {
		ev.AssertIf(o.prefix+".uid.set", "retrieved instances", fmt.Sprintf("exactly the %d expected", len(exp.sops)), fmt.Sprintf("%d received", len(received)), true, "")
	}
	if o.pixels && len(received) > 0 {
		rn.comparePixels(ctx, p, ev, exp.files, dir, o.prefix)
	}
	if o.validate && len(received) > 0 {
		v := rn.Engines.Validate(ctx, received, map[string]string{})
		v.ID = s.ID
		s.Validation = &v
		for _, f := range v.Files {
			e, ee := 0, len(f.EngineErrors)
			for _, x := range f.Findings {
				if x.Severity == result.Error {
					e++
				}
			}
			ev.AssertIf(o.prefix+".validate", filepath.Base(f.File), "no errors and no engine errors", fmt.Sprintf("%d errors, %d engine errors", e, ee), e == 0 && ee == 0, "")
		}
		if len(v.Files) != len(received) {
			ev.Problemf(result.ProblemEngine, "validation returned %d results for %d instances", len(v.Files), len(received))
		}
	}
}

func markInstances(ev *result.Evidence, uid, res string) {
	for i := range ev.Instances {
		if ev.Instances[i].SOPInstanceUID == uid {
			ev.Instances[i].RemoteResult = res
		}
	}
}

// comparePixels applies DVT-SRS-123 (as DVT-SRS-025): pixel data and SOP
// Instance UID equal to the originals; compressed data in another transfer
// syntax counts as transcoded.
func (rn *Runner) comparePixels(ctx context.Context, p *plan.Plan, ev *result.Evidence, files []string, dir, prefix string) {
	cmp, _, err := rn.Engines.Compare(ctx, files, dir)
	if err != nil {
		ev.Problemf(result.ProblemEngine, "engine error (compare): %v", err)
		return
	}
	if len(cmp) != len(files) {
		ev.Problemf(result.ProblemEngine, "compare: %d results for %d files", len(cmp), len(files))
		return
	}
	for _, c := range cmp {
		subj := rel(p, c.File)
		actual := "identical"
		ok := c.Match
		if !c.Match {
			actual = firstNonEmpty(c.Error, "pixel data differs")
		} else if c.SentTransferSyntax != c.ReceivedTransferSyntax && (!nativeTS[c.SentTransferSyntax] || !nativeTS[c.ReceivedTransferSyntax]) {
			ok, actual = false, fmt.Sprintf("transcoded from %s to %s", c.SentTransferSyntax, c.ReceivedTransferSyntax)
		}
		ev.AssertIf(prefix+".pixels", subj, "pixel data identical to the sent file", actual, ok, "")
		for i := range ev.Instances {
			if ev.Instances[i].SOPInstanceUID == c.SOPInstanceUID {
				ev.Instances[i].PixelSHA256Sent, ev.Instances[i].PixelSHA256Received = c.SentPixelSHA256, c.ReceivedPixelSHA256
				ev.Instances[i].File = subj
			}
		}
	}
}

// metadata applies DVT-SRS-124.
func (rn *Runner) metadata(ctx context.Context, p *plan.Plan, ps plan.Step, t *store.Target, ev *result.Evidence, exp expectation) {
	res, _, err := rn.Engines.Wado(ctx, rn.web(p, t), engine.WadoRequest{Resources: exp.resources, OutDir: rn.WorkDir,
		Metadata: true, Attributes: ps.CompareAttributes, ExpectFiles: exp.files})
	if err != nil {
		ev.Problemf(result.ProblemEngine, "engine error: %v", err)
		return
	}
	if res.ConfigError != "" {
		ev.Problemf(result.ProblemConfig, "%s", res.ConfigError)
		return
	}
	webExchanges(ev, res.Exchanges)
	for _, x := range res.Exchanges {
		if x.HTTP == nil || x.HTTP.Status == 0 {
			continue
		}
		ev.Assert("wado.status", x.Endpoint, "200", statusStr(x.HTTP.Status), "")
		if x.HTTP.Status == 200 && x.HTTP.Body != nil {
			mt, _ := mediaType(x.HTTP.Body.ContentType)
			ev.Assert("wado.content_type", x.Endpoint, mediaDICOMJSON, mt, "")
		}
	}
	bySOP := map[string][]engine.MetaObject{}
	for _, m := range res.Metadata {
		bySOP[m.SOPInstanceUID] = append(bySOP[m.SOPInstanceUID], m)
	}
	for _, u := range sortedKeys(exp.sops) {
		ms := bySOP[u]
		if len(ms) != 1 {
			ev.AssertIf("wado.metadata.instance", u, "in the metadata once", fmt.Sprintf("%d times", len(ms)), false, "")
			continue
		}
		m := ms[0]
		want := exp.studyOf[u] + " / " + exp.seriesOf[u]
		ev.Assert("wado.metadata.instance", u, want, m.StudyInstanceUID+" / "+m.SeriesInstanceUID, "Study / Series Instance UID")
	}
	for u := range bySOP {
		if !exp.sops[u] {
			ev.AssertIf("wado.metadata.instance", u, "not expected", "in the metadata", false, "unexpected instance")
		}
	}
	// compare_attributes: each original's value equals the metadata value.
	for _, o := range res.Originals {
		if o.Error != "" {
			ev.Problemf(result.ProblemConfig, "%s: %s", rel(p, o.File), o.Error)
			continue
		}
		ms := bySOP[o.SOPInstanceUID]
		for _, k := range ps.CompareAttributes {
			tag := res.Keywords[k]
			got := "(instance missing)"
			if len(ms) == 1 {
				got = ms[0].Attrs[tag]
			}
			ev.Assert("wado.metadata.attribute", o.SOPInstanceUID+" "+k, o.Attrs[tag], got, "value in the original file vs. the metadata")
		}
	}
}

// --- stow (DVT-SRS-128…-136) ---

func (rn *Runner) stow(ctx context.Context, p *plan.Plan, ps plan.Step, t *store.Target, st *sent, s *result.Step) {
	ev := result.NewEvidence(t.Info())
	files, err := p.ResolveFiles(ps.Files)
	if err != nil {
		ev.Problemf(result.ProblemConfig, "%v; nothing was sent", err)
		s.ApplyEvidence(ev)
		return
	}
	// DVT-SRS-133: every file must be a readable DICOM Part 10 file before
	// anything is sent.
	probes, err := rn.Engines.Probe(ctx, files)
	if err != nil {
		ev.Problemf(result.ProblemEngine, "engine error: %v; nothing was sent", err)
		s.ApplyEvidence(ev)
		return
	}
	for _, pr := range probes {
		if !pr.DICOM || pr.SOPInstanceUID == "" || pr.TransferSyntaxUID == "" {
			ev.Problemf(result.ProblemConfig, "%s: not a readable DICOM Part 10 file (%s); nothing was sent", rel(p, pr.File), firstNonEmpty(pr.Error, "no file meta or SOP Instance UID"))
		}
	}
	if len(ev.Errors) > 0 {
		s.ApplyEvidence(ev)
		return
	}
	batch := 10
	if ps.BatchSize != nil {
		batch = *ps.BatchSize
	}
	res, _, err := rn.Engines.Stow(ctx, rn.web(p, t), ps.Study, batch, files)
	if err != nil {
		ev.Problemf(result.ProblemEngine, "engine error: %v", err)
		s.ApplyEvidence(ev)
		return
	}
	if res.ConfigError != "" {
		ev.Problemf(result.ProblemConfig, "%s", res.ConfigError)
		s.ApplyEvidence(ev)
		return
	}
	for _, f := range res.Files {
		if f.Error != "" {
			ev.Problemf(result.ProblemConfig, "%s: %s; nothing was sent", rel(p, f.File), f.Error)
		}
	}
	webExchanges(ev, res.Exchanges)
	stored := checkStow(ev, p, ps, t, res)
	for _, f := range stored {
		st.files = append(st.files, f.File)
		st.instances[f.StudyInstanceUID] = appendUnique(st.instances[f.StudyInstanceUID], f.SOPInstanceUID)
		st.add(sentInstance{File: f.File, Study: f.StudyInstanceUID, Series: f.SeriesInstanceUID, SOP: f.SOPInstanceUID, Class: f.SOPClassUID})
	}
	// DVT-SRS-132: re-retrieve each stored instance from the target's own
	// base URL by UID and compare.
	if ps.VerifyRetrieve && len(stored) > 0 && len(ev.Errors) == 0 {
		exp := expectation{sops: map[string]bool{}}
		for _, f := range stored {
			exp.sops[f.SOPInstanceUID] = true
			exp.files = append(exp.files, f.File)
			exp.resources = append(exp.resources, fmt.Sprintf("/studies/%s/series/%s/instances/%s", f.StudyInstanceUID, f.SeriesInstanceUID, f.SOPInstanceUID))
		}
		vs := &result.Step{ID: s.ID}
		rn.retrieve(ctx, p, t, ev, vs, exp, retrieveOpts{pixels: true, prefix: "stow.verify"})
	}
	s.ApplyEvidence(ev)
	s.Messages = append(s.Messages, fmt.Sprintf("%d of %d files stored and referenced by the target", len(stored), len(files)))
	addWebTLSNote(s, ev.Exchanges)
}

// checkStow applies DVT-SRS-129…-132 and -136 per request and returns the
// files the target stored without failure.
func checkStow(ev *result.Evidence, p *plan.Plan, ps plan.Step, t *store.Target, res engine.StowResult) []engine.StowFile {
	var stored []engine.StowFile
	for _, q := range res.Requests {
		x := res.Exchanges[q.Exchange-1]
		subj := x.Endpoint
		if x.Outcome != result.ExchangeCompleted && q.Status == 0 {
			for _, k := range q.Files {
				f := res.Files[k]
				ev.Instances = append(ev.Instances, stowInstance(p, f, "missing", ""))
			}
			continue
		}
		// DVT-SRS-129: the body must be a DICOM JSON object.
		if q.JSON != "object" {
			ev.Problemf(result.ProblemProtocol, "%s: the STOW-RS response is not a DICOM JSON object (%s %s)", subj, firstNonEmpty(q.JSON, "unknown"), q.Detail)
		}
		refs := map[string][]engine.StowRef{}
		for _, r := range q.Referenced {
			refs[r.SOPInstanceUID] = append(refs[r.SOPInstanceUID], r)
		}
		failed := map[string]engine.StowFailed{}
		for _, f := range q.Failed {
			failed[f.SOPInstanceUID] = f
		}
		warnings := 0
		if q.WarningReason != "" {
			warnings++
			stowWarning(ev, ps, subj, "Warning Reason "+q.WarningReason)
		}
		sentHere := map[string]bool{}
		for _, k := range q.Files {
			f := res.Files[k]
			sentHere[f.SOPInstanceUID] = true
			uid := f.SOPInstanceUID
			if fl, ok := failed[uid]; ok {
				// DVT-SRS-131: a finding naming the UID and reason.
				ev.AssertIf("stow.instance.failed", uid, "stored", "failed, Failure Reason "+firstNonEmpty(fl.FailureReason, "(none)"), false, "")
				ev.Instances = append(ev.Instances, stowInstance(p, f, "failure", fl.FailureReason))
				continue
			}
			rs := refs[uid]
			switch {
			case len(rs) == 0:
				ev.AssertIf("stow.instance.referenced", uid, "in the Referenced SOP Sequence once", "missing", false, "")
				ev.Instances = append(ev.Instances, stowInstance(p, f, "missing", ""))
				continue
			case len(rs) > 1:
				ev.AssertIf("stow.instance.referenced", uid, "in the Referenced SOP Sequence once", fmt.Sprintf("%d times", len(rs)), false, "")
				ev.Instances = append(ev.Instances, stowInstance(p, f, "duplicate", ""))
				continue
			}
			r := rs[0]
			ok := ev.Assert("stow.instance.referenced", uid, "referenced with SOP Class "+f.SOPClassUID, "referenced with SOP Class "+r.SOPClassUID, "")
			in := stowInstance(p, f, "success", "")
			in.RetrieveURL = r.RetrieveURL
			if r.WarningReason != "" {
				warnings++
				in.RemoteResult, in.Reason = "warning", r.WarningReason
				stowWarning(ev, ps, uid, "Warning Reason "+r.WarningReason)
			}
			ev.Instances = append(ev.Instances, in)
			if ok {
				stored = append(stored, f)
			}
		}
		for _, f := range q.Failed {
			if !sentHere[f.SOPInstanceUID] {
				ev.AssertIf("stow.instance.failed", firstNonEmpty(f.SOPInstanceUID, "(no UID)"), "no failure", "failed item for an instance not sent, Failure Reason "+f.FailureReason, false, "")
			}
		}
		for u := range refs {
			if !sentHere[u] {
				ev.AssertIf("stow.instance.extra", firstNonEmpty(u, "(no UID)"), "not referenced", "referenced although not sent", false, "unexpected extra UID")
			}
		}
		// DVT-SRS-130: 200 only; 202 passes with warnings only when every
		// problem is a warning and allow_warnings is set.
		switch {
		case q.Status == 200 && len(q.Failed) == 0:
			ev.Assert("stow.status", subj, "200", "200", "")
		case q.Status == 202 && ps.AllowWarnings && len(q.Failed) == 0 && warnings > 0:
			ev.Warn("stow.status", subj, "200", "202", "accepted with warnings (allow_warnings)")
		default:
			ev.AssertIf("stow.status", subj, "200 with no failed instance", fmt.Sprintf("%s with %d failed instance(s)", statusStr(q.Status), len(q.Failed)), false, "")
		}
		// DVT-SRS-132: Retrieve URLs are recorded, never followed off-origin.
		for _, u := range append([]string{q.RetrieveURL}, retrieveURLs(q.Referenced)...) {
			if u != "" && !sameOrigin(u, t.BaseURL) {
				ev.Warn("stow.retrieve_url.origin", u, "the target's own origin", "another origin (recorded, not requested)", "")
			}
		}
	}
	return stored
}

func retrieveURLs(rs []engine.StowRef) []string {
	var out []string
	for _, r := range rs {
		out = append(out, r.RetrieveURL)
	}
	return out
}

func stowWarning(ev *result.Evidence, ps plan.Step, subj, what string) {
	if ps.AllowWarnings {
		ev.Warn("stow.instance.warning", subj, "no warning", what, "allowed by allow_warnings")
		return
	}
	ev.AssertIf("stow.instance.warning", subj, "no warning", what, false, "set allow_warnings to accept warnings")
}

func stowInstance(p *plan.Plan, f engine.StowFile, res, reason string) result.Instance {
	return result.Instance{SOPInstanceUID: f.SOPInstanceUID, SOPClassUID: f.SOPClassUID, TransferSyntax: f.TransferSyntax,
		File: rel(p, f.File), Size: f.Size, SHA256: f.SHA256, RemoteResult: res, Reason: reason}
}

// sameOrigin compares scheme, host and port (default ports filled in).
func sameOrigin(a, b string) bool {
	ua, err1 := url.Parse(a)
	ub, err2 := url.Parse(b)
	if err1 != nil || err2 != nil {
		return false
	}
	port := func(u *url.URL) string {
		if p := u.Port(); p != "" {
			return p
		}
		if u.Scheme == "https" {
			return "443"
		}
		return "80"
	}
	return strings.EqualFold(ua.Scheme, ub.Scheme) && strings.EqualFold(ua.Hostname(), ub.Hostname()) && port(ua) == port(ub)
}

// addWebTLSNote puts the TLS facts of the step's first https request into
// the messages every report shows (DVT-SRS-107).
func addWebTLSNote(s *result.Step, xs []result.Exchange) {
	for _, x := range xs {
		if x.TLS != nil && x.TLS.Verified {
			s.Messages = append(s.Messages, tlsText(x.TLS))
			return
		}
	}
}

func rel(p *plan.Plan, f string) string {
	if r, err := filepath.Rel(p.Dir, f); err == nil && !strings.HasPrefix(r, "..") {
		return r
	}
	return path.Base(f)
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedIntKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// webProbeVerdict maps an HTTP refusal probe's observation to refused or
// accepted (DVT-SRS-105). An HTTP 400 answer to the bare GET counts as a
// refusal: TLS servers answer a clear-text request on their port, or a
// request without the required client certificate, with 400 and do not
// serve it. Any other HTTP status shows the request was served.
func webProbeVerdict(mode, obs string, status int) (verdict, kind string) {
	switch obs {
	case engine.ObsHTTPReply:
		if mode == "tls_version" {
			return "accepted", "" // a handshake with the old version completed first
		}
		if status == 400 {
			return "refused", ""
		}
		return "accepted", ""
	case engine.ObsNonHTTPReply:
		if mode == "plaintext" {
			return "refused", ""
		}
		return "", result.ProblemProtocol
	}
	switch mode {
	case "plaintext":
		switch obs {
		case engine.ObsConnectionClosed, engine.ObsConnectionReset:
			return "refused", ""
		}
		v, k := probeVerdict(mode, obs)
		return v, k
	default:
		return probeVerdict(mode, obs)
	}
}

func (rn *Runner) tlsRejectWeb(ctx context.Context, p *plan.Plan, ps plan.Step, t *store.Target, s *result.Step) {
	ev := result.NewEvidence(t.Info())
	res, _, err := rn.Engines.TLSProbeWeb(ctx, rn.web(p, t), ps.Mode, ps.TLSVersion)
	if err != nil {
		ev.Problemf(result.ProblemEngine, "engine error: %v", err)
		s.ApplyEvidence(ev)
		return
	}
	ev.Exchanges = res.Exchanges
	subj := ps.Mode
	if ps.Mode == "tls_version" {
		subj = "TLS " + ps.TLSVersion + " only"
	}
	verdict, kind := webProbeVerdict(ps.Mode, res.Observation, engine.ProbeHTTPStatus(res))
	switch verdict {
	case "refused", "accepted":
		ev.Assert("tls_reject.refused", subj, "refused", verdict, res.Observation+": "+res.Detail)
	default:
		ev.Problemf(kind, "could not show a refusal (%s): %s", res.Observation, res.Detail)
	}
	s.ApplyEvidence(ev)
}
