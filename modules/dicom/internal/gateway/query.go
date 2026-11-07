package gateway

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/status"

	"rws/modules/dicom/internal/dcm"
	"rws/modules/dicom/internal/index"
)

type matchKind int

const (
	kString   matchKind = iota // single value or wildcard
	kUID                       // single UID or list
	kDate                      // single date or range
	kExact                     // single value only
	kReturn                    // return-only (computed); matching ignored
	kModality                  // ModalitiesInStudy
	kPersonName                // like kString; case-insensitive when configured
	kTime                      // single time or range (TM)
)

type keyDef struct {
	column string
	kind   matchKind
}

// queryKeys maps supported DICOM matching/return keys to index columns.
var queryKeys = map[string]keyDef{
	"PatientID":                      {"st.patient_id", kString},
	"PatientName":                    {"st.patient_name", kPersonName},
	"PatientBirthDate":               {"st.patient_birth_date", kDate},
	"PatientSex":                     {"st.patient_sex", kExact},
	"StudyInstanceUID":               {"st.study_uid", kUID},
	"StudyDate":                      {"st.study_date", kDate},
	"StudyTime":                      {"st.study_time", kTime},
	"AccessionNumber":                {"st.accession_number", kString},
	"StudyID":                        {"st.study_id", kString},
	"StudyDescription":               {"st.study_description", kString},
	"ReferringPhysicianName":         {"st.referring_physician", kString},
	"ModalitiesInStudy":              {"", kModality},
	"SeriesInstanceUID":              {"se.series_uid", kUID},
	"Modality":                       {"se.modality", kExact},
	"SeriesNumber":                   {"se.series_number", kExact},
	"SeriesDescription":              {"se.series_description", kString},
	"SOPInstanceUID":                 {"i.sop_instance_uid", kUID},
	"SOPClassUID":                    {"i.sop_class_uid", kUID},
	"InstanceNumber":                 {"i.instance_number", kExact},
	"NumberOfPatientRelatedStudies":  {"", kReturn},
	"NumberOfStudyRelatedSeries":     {"", kReturn},
	"NumberOfStudyRelatedInstances":  {"", kReturn},
	"NumberOfSeriesRelatedInstances": {"", kReturn},
}

// levelOf says which levels may filter on a column prefix.
var allowedPrefixes = map[index.Level][]string{
	index.Patient: {"st.patient_"},
	index.Study:   {"st."},
	index.Series:  {"st.", "se."},
	index.Image:   {"st.", "se.", "i."},
}

var errBadIdentifier = errors.New("identifier does not match information model")

type parsedQuery struct {
	level   index.Level
	filters []index.Filter
}

// queryOptions are the configurable parts of matching.
type queryOptions struct {
	caseInsensitivePN bool
}

func parseIdentifier(ident *godicom.Dataset, model string, opt queryOptions) (parsedQuery, error) {
	var q parsedQuery
	if ident == nil {
		return q, errBadIdentifier
	}
	q.level = index.Level(strings.ToUpper(dcm.String(ident, "QueryRetrieveLevel")))
	switch q.level {
	case index.Patient:
		switch model { // Study Root has no PATIENT level
		case ae.StudyRootQueryRetrieveInformationModelFind,
			ae.StudyRootQueryRetrieveInformationModelMove,
			ae.StudyRootQueryRetrieveInformationModelGet:
			return q, errBadIdentifier
		}
	case index.Study, index.Series, index.Image:
	default:
		return q, errBadIdentifier
	}

	for _, el := range ident.Iter() {
		kw := el.Keyword()
		def, ok := queryKeys[kw]
		if !ok || def.kind == kReturn {
			continue
		}
		vals := dcm.Strings(ident, kw)
		if len(vals) == 0 || (len(vals) == 1 && (vals[0] == "" || vals[0] == "*")) {
			continue // universal match
		}
		if def.kind == kModality {
			if q.level == index.Study {
				q.filters = append(q.filters, index.Filter{Op: index.OpStudyHasModality, Values: vals})
			}
			continue
		}
		if !columnAllowed(q.level, def.column) {
			continue
		}
		f, ok := buildFilter(def, vals, opt)
		if ok {
			q.filters = append(q.filters, f)
		}
	}
	return q, nil
}

func columnAllowed(level index.Level, column string) bool {
	for _, p := range allowedPrefixes[level] {
		if strings.HasPrefix(column, p) {
			return true
		}
	}
	return false
}

func buildFilter(def keyDef, vals []string, opt queryOptions) (index.Filter, bool) {
	switch def.kind {
	case kPersonName:
		v := vals[0]
		if !opt.caseInsensitivePN {
			return buildFilter(keyDef{def.column, kString}, vals, opt)
		}
		col := def.column + "_fold"
		if strings.ContainsAny(v, "*?") {
			return index.Filter{Column: col, Op: index.OpLikeFold, Values: []string{wildcardToLike(v)}}, true
		}
		return index.Filter{Column: col, Op: index.OpEqFold, Values: []string{v}}, true
	case kTime:
		v := strings.ReplaceAll(vals[0], ":", "") // ACR-NEMA style 08:30 is still sent by old devices
		if from, to, ok := strings.Cut(v, "-"); ok {
			return index.Filter{Column: def.column, Op: index.OpRange, Values: []string{timeLower(from), timeUpper(to)}}, true
		}
		// A single time matches the whole period it names: 0830 = 08:30:00 to 08:30:59.999999.
		return index.Filter{Column: def.column, Op: index.OpRange, Values: []string{timeLower(v), timeUpper(v)}}, true
	case kUID:
		return index.Filter{Column: def.column, Op: index.OpIn, Values: vals}, true
	case kDate:
		v := vals[0]
		if from, to, ok := strings.Cut(v, "-"); ok {
			return index.Filter{Column: def.column, Op: index.OpRange, Values: []string{from, to}}, true
		}
		return index.Filter{Column: def.column, Op: index.OpEq, Values: []string{v}}, true
	case kString:
		v := vals[0]
		if strings.ContainsAny(v, "*?") {
			return index.Filter{Column: def.column, Op: index.OpLike, Values: []string{wildcardToLike(v)}}, true
		}
		return index.Filter{Column: def.column, Op: index.OpEq, Values: []string{v}}, true
	case kExact:
		return index.Filter{Column: def.column, Op: index.OpEq, Values: []string{vals[0]}}, true
	}
	return index.Filter{}, false
}

// timeLower pads a TM bound with zeros (08 -> 080000); "" stays open.
func timeLower(t string) string {
	t = strings.TrimSpace(t)
	if t == "" {
		return ""
	}
	h, _, _ := strings.Cut(t, ".")
	return (h + "000000")[:6] + strings.TrimPrefix(t, h)
}

// timeUpper pads a TM bound to the end of the period it names
// (12 -> 125959.999999, 1200 -> 120059.999999); "" stays open.
func timeUpper(t string) string {
	t = strings.TrimSpace(t)
	if t == "" {
		return ""
	}
	h, frac, hasFrac := strings.Cut(t, ".")
	if hasFrac {
		return h + "." + (frac + "999999")[:6]
	}
	return h + "595959"[min(len(h), 6):] + ".999999"
}

func wildcardToLike(v string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	v = r.Replace(v)
	return strings.NewReplacer("*", "%", "?", "_").Replace(v)
}

func (g *Gateway) onCFind(ctx context.Context, req ae.FindRequest) []ae.FindMatch {
	start := time.Now()
	defer func() { g.m.findSeconds.Observe(time.Since(start).Seconds()) }()

	q, err := parseIdentifier(req.IdentifierData, req.QueryModel, g.queryOptions())
	if err != nil {
		g.m.finds.WithLabelValues("invalid", "invalid").Inc()
		return []ae.FindMatch{{Status: status.IdentifierDoesNotMatchSOPClass}}
	}
	lvl := string(q.level)

	rows, err := g.idx.Find(ctx, q.level, q.filters, g.cfg.FindLimit)
	if err != nil {
		g.log.Error("C-FIND query failed", "level", lvl, "err", err)
		g.m.finds.WithLabelValues(lvl, "error").Inc()
		return []ae.FindMatch{{Status: status.UnableToProcess}}
	}
	if g.cfg.FindLimit > 0 && len(rows) >= g.cfg.FindLimit {
		g.log.Warn("C-FIND result capped", "level", lvl, "limit", g.cfg.FindLimit)
	}

	if q.level == index.Study && dcm.Has(req.IdentifierData, "ModalitiesInStudy") {
		uids := make([]string, len(rows))
		for i, r := range rows {
			uids[i] = r["StudyInstanceUID"]
		}
		mods, err := g.idx.ModalitiesForStudies(ctx, uids)
		if err == nil {
			for _, r := range rows {
				r["ModalitiesInStudy"] = strings.Join(mods[r["StudyInstanceUID"]], `\`)
			}
		}
	}

	matches := make([]ae.FindMatch, 0, len(rows)+1)
	for _, r := range rows {
		select {
		case <-ctx.Done():
			return []ae.FindMatch{{Status: status.Cancel}}
		default:
		}
		matches = append(matches, ae.FindMatch{Status: status.Pending, Identifier: buildResponse(req.IdentifierData, lvl, r)})
	}
	g.m.finds.WithLabelValues(lvl, "success").Inc()
	return append(matches, ae.FindMatch{Status: status.Success})
}

// buildResponse returns exactly the keys the requestor asked for, filled from
// the index row where we have a value and zero-length otherwise. Text is held
// as Unicode in the index; the response is labelled with a character set that
// can carry it (see responseCharset).
func buildResponse(ident *godicom.Dataset, level string, row index.Row) *godicom.Dataset {
	out := godicom.NewDataset()
	var texts []string
	for _, el := range ident.Iter() {
		kw := el.Keyword()
		switch kw {
		case "QueryRetrieveLevel":
			out.Set(godicom.NewDataElement(el.Tag, el.VR, level))
			continue
		case "SpecificCharacterSet":
			continue // set below
		}
		if v, ok := row[kw]; ok && v != "" {
			out.Set(godicom.NewDataElement(el.Tag, el.VR, v))
			texts = append(texts, v)
			continue
		}
		out.Set(godicom.NewDataElement(el.Tag, el.VR, nil))
	}
	if cs := responseCharset(dcm.Strings(ident, "SpecificCharacterSet"), texts); len(cs) > 0 {
		out.Set(godicom.NewDataElement(godicom.MustTag("SpecificCharacterSet"), godicom.VRCS, cs))
	}
	return out
}

// responseCharset labels a response. Index text is Unicode and the encoder
// writes it as UTF-8 bytes, so any non-ASCII result is answered as ISO_IR 192
// (UTF-8), which PS3.4 C.4.1.1.3.2 allows whatever the request used. Plain
// ASCII results keep the request's value. Known limit: a requestor that can't
// read UTF-8 sees non-ASCII names garbled (re-encoding to the request's
// character set is a later improvement).
func responseCharset(requested, values []string) []string {
	for _, v := range values {
		for i := 0; i < len(v); i++ {
			if v[i] >= 0x80 {
				return []string{"ISO_IR 192"}
			}
		}
	}
	return requested
}
