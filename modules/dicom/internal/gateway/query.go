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
)

type keyDef struct {
	column string
	kind   matchKind
}

// queryKeys maps supported DICOM matching/return keys to index columns.
var queryKeys = map[string]keyDef{
	"PatientID":                      {"st.patient_id", kString},
	"PatientName":                    {"st.patient_name", kString},
	"PatientBirthDate":               {"st.patient_birth_date", kDate},
	"PatientSex":                     {"st.patient_sex", kExact},
	"StudyInstanceUID":               {"st.study_uid", kUID},
	"StudyDate":                      {"st.study_date", kDate},
	"StudyTime":                      {"st.study_time", kReturn},
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

func parseIdentifier(ident *godicom.Dataset, model string) (parsedQuery, error) {
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
		f, ok := buildFilter(def, vals)
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

func buildFilter(def keyDef, vals []string) (index.Filter, bool) {
	switch def.kind {
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

func wildcardToLike(v string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	v = r.Replace(v)
	return strings.NewReplacer("*", "%", "?", "_").Replace(v)
}

func (g *Gateway) onCFind(ctx context.Context, req ae.FindRequest) []ae.FindMatch {
	start := time.Now()
	defer func() { g.m.findSeconds.Observe(time.Since(start).Seconds()) }()

	q, err := parseIdentifier(req.IdentifierData, req.QueryModel)
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
// the index row where we have a value and zero-length otherwise.
func buildResponse(ident *godicom.Dataset, level string, row index.Row) *godicom.Dataset {
	out := godicom.NewDataset()
	for _, el := range ident.Iter() {
		kw := el.Keyword()
		switch kw {
		case "QueryRetrieveLevel":
			out.Set(godicom.NewDataElement(el.Tag, el.VR, level))
			continue
		case "SpecificCharacterSet":
			out.Set(godicom.NewDataElement(el.Tag, el.VR, el.Value))
			continue
		}
		if v, ok := row[kw]; ok && v != "" {
			out.Set(godicom.NewDataElement(el.Tag, el.VR, v))
			continue
		}
		out.Set(godicom.NewDataElement(el.Tag, el.VR, nil))
	}
	return out
}
