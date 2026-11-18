package fakedcm

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/mulcahyio/libre-rws/modules/wt/internal/dcmclient"
)

// The data part of the fake: study query, send, audit records and log
// lines (control-api-v1.md §3.4, §3.5, §5.1, §5.2). It behaves like
// LibreDCM where Libre WT depends on it: the operator header is required,
// a query without a value is refused, the send needs the exact Patient ID
// of the study, a destination from the configured list and the digest of
// the current plan. All data is synthetic.

// Study is one study of the fake archive.
type Study struct {
	Row    map[string]string // study-level attributes, keyed by DICOM keyword
	Series []Series
}

// Series is one series of a study.
type Series struct {
	Row       map[string]string
	Instances []map[string]string
}

// Data holds what the data endpoints of the fake answer. Tests change it
// through Server.Set.
type Data struct {
	Studies []Study
	// Held is the conflict visibility the node reports ("hidden" or
	// "flagged").
	Held string
	// FailUIDs are SOP Instance UIDs the destination refuses on a send.
	FailUIDs map[string]bool
	// PlanPatient, PlanStudy and PlanDest, when set, are what every send
	// plan names, whatever the request held: a node that answers about
	// another patient, study or destination. NoDigest leaves the plan
	// digest out.
	PlanPatient, PlanStudy, PlanDest string
	NoDigest                         bool
	// Audit and Logs are the node's in-memory records and lines, oldest
	// first, as JSON objects.
	Audit, Logs []map[string]any
	AuditLogOff bool

	queries []url.Values // every study query, with "operator" added
}

func defaultData() Data {
	// Audit records and log lines are dated an hour ago, so they stay inside
	// Libre WT's default 24-hour window whatever day the tests run (they
	// were fixed dates and dropped out of the window after a day).
	base := time.Now().UTC().Add(-time.Hour).Truncate(time.Minute)
	at := func(min int) string { return base.Add(time.Duration(min) * time.Minute).Format(time.RFC3339) }
	inst := func(series, n string) map[string]string {
		return map[string]string{"SOPInstanceUID": series + "." + n, "InstanceNumber": n, "SOPClassUID": "1.2.840.10008.5.1.4.1.1.2",
			"TransferSyntaxUID": "1.2.840.10008.1.2.1"}
	}
	study := func(uid, pid, name, birth, date, acc, desc, mods string, series ...Series) Study {
		n := 0
		for _, s := range series {
			n += len(s.Instances)
		}
		return Study{Row: map[string]string{"StudyInstanceUID": uid, "PatientID": pid, "PatientName": name, "PatientBirthDate": birth,
			"PatientSex": "O", "StudyDate": date, "AccessionNumber": acc, "StudyDescription": desc, "ModalitiesInStudy": mods,
			"NumberOfStudyRelatedSeries": strconv.Itoa(len(series)), "NumberOfStudyRelatedInstances": strconv.Itoa(n)}, Series: series}
	}
	series := func(uid, mod, num, desc string, n int) Series {
		s := Series{Row: map[string]string{"SeriesInstanceUID": uid, "Modality": mod, "SeriesNumber": num, "SeriesDescription": desc,
			"NumberOfSeriesRelatedInstances": strconv.Itoa(n)}}
		for i := 1; i <= n; i++ {
			s.Instances = append(s.Instances, inst(uid, strconv.Itoa(i)))
		}
		return s
	}
	return Data{
		Held: "hidden",
		Studies: []Study{
			study("1.3.6.1.4.1.67686.9.2.1", "PAT-001", "DOE^JANE", "19700101", "20261101", "ACC-1001", "CT CHEST", `CT`,
				series("1.3.6.1.4.1.67686.9.2.1.1", "CT", "1", "AXIAL", 3), series("1.3.6.1.4.1.67686.9.2.1.2", "CT", "2", "CORONAL", 2)),
			study("1.3.6.1.4.1.67686.9.2.2", "PAT-002", "DOE^JOHN", "19710202", "20261105", "ACC-1002", "MR BRAIN", `MR`,
				series("1.3.6.1.4.1.67686.9.2.2.1", "MR", "1", "T1", 2)),
		},
		FailUIDs: map[string]bool{},
		Audit: []map[string]any{
			{"time": at(0), "level": "INFO", "msg": "audit", "audit": true, "event": "DICOMInstancesTransferred.received",
				"outcome": "success", "calling_ae": "CT1", "patient_ids": []any{"PAT-001"}, "studies": []any{"1.3.6.1.4.1.67686.9.2.1"}},
			{"time": at(5), "level": "INFO", "msg": "audit", "audit": true, "event": "SecurityAlert.association-rejected",
				"outcome": "minor-failure", "calling_ae": "=HYPERLINK(\"http://x\")", "remote": "10.9.9.9:4000"},
		},
		Logs: []map[string]any{
			{"time": at(-1), "level": "DEBUG", "msg": "association negotiated", "calling_ae": "CT1"},
			{"time": at(0), "level": "INFO", "msg": "stored instance", "sop_class": "CT"},
			{"time": at(1), "level": "WARN", "msg": "outbox publish retry", "attempt": 2},
			{"time": at(2), "level": "ERROR", "msg": "+cmd|' /C calc'!A0 storage write failed", "path": "/data/blob"},
		},
	}
}

// Queries returns every study query the node received, each with the
// operator header as "operator".
func (s *Server) Queries() []url.Values {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]url.Values(nil), s.Data.queries...)
}

// match is DICOM wildcard matching with * and ?.
func match(pattern, value string) bool {
	ok, err := path.Match(strings.NewReplacer("[", `\[`, "]", `\]`, `\`, `\\`).Replace(pattern), value)
	return err == nil && ok
}

// keyMatches matches one key of a row. A date key takes a range.
func keyMatches(kw, want, got string) bool {
	switch {
	case kw == "ModalitiesInStudy":
		for _, m := range strings.Split(got, `\`) {
			if m == want {
				return true
			}
		}
		return false
	case strings.HasSuffix(kw, "Date") && strings.Contains(want, "-"):
		from, to, _ := strings.Cut(want, "-")
		return (from == "" || got >= from) && (to == "" || got <= to)
	}
	return match(want, got)
}

func rowMatches(keys map[string]string, rows ...map[string]string) bool {
	for kw, want := range keys {
		found := false
		for _, r := range rows {
			if v, ok := r[kw]; ok {
				found = true
				if !keyMatches(kw, want, v) {
					return false
				}
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func merged(rows ...map[string]string) map[string]string {
	out := map[string]string{}
	for _, r := range rows {
		for k, v := range r {
			out[k] = v
		}
	}
	return out
}

var levelKeys = map[string]map[string]bool{
	"study":  {"PatientID": true, "PatientName": true, "PatientBirthDate": true, "StudyInstanceUID": true, "StudyDate": true, "AccessionNumber": true, "ModalitiesInStudy": true, "StudyDescription": true},
	"series": {"PatientID": true, "StudyInstanceUID": true, "SeriesInstanceUID": true, "Modality": true},
	"image":  {"PatientID": true, "StudyInstanceUID": true, "SeriesInstanceUID": true, "SOPInstanceUID": true},
}

// studies serves GET /control/v1/studies. The caller holds the lock.
func (s *Server) studies(w http.ResponseWriter, r *http.Request, inst dcmclient.InstanceInfo) {
	operator := strings.TrimSpace(r.Header.Get(dcmclient.OperatorHeader))
	q := r.URL.Query()
	rec := url.Values{}
	for k, v := range q {
		rec[k] = append([]string(nil), v...)
	}
	rec.Set("operator", operator)
	s.Data.queries = append(s.Data.queries, rec)
	if operator == "" {
		fail(w, http.StatusUnprocessableEntity, "operator_required", "the header X-Libre-Operator is required")
		return
	}
	level := q.Get("level")
	if level == "" {
		level = "study"
	}
	limit := 50
	if v := q.Get("limit"); v != "" {
		limit, _ = strconv.Atoi(v)
	}
	offset := 0
	if c := q.Get("cursor"); c != "" {
		offset, _ = strconv.Atoi(strings.TrimPrefix(c, "c"))
	}
	keys := map[string]string{}
	for k, v := range q {
		if k == "level" || k == "limit" || k == "cursor" {
			continue
		}
		if !levelKeys[level][k] {
			fail(w, http.StatusUnprocessableEntity, "key_not_at_level", k+" cannot be matched at the "+level+" level")
			return
		}
		if v[0] != "" && v[0] != "*" {
			keys[k] = v[0]
		}
	}
	if len(keys) == 0 {
		fail(w, http.StatusUnprocessableEntity, "no_matching_key", "give at least one matching key with a value; the whole archive is not listed")
		return
	}
	var rows []map[string]string
	for _, st := range s.Data.Studies {
		switch level {
		case "study":
			if rowMatches(keys, st.Row) {
				rows = append(rows, st.Row)
			}
		case "series":
			for _, se := range st.Series {
				if rowMatches(keys, st.Row, se.Row) {
					rows = append(rows, merged(map[string]string{"StudyInstanceUID": st.Row["StudyInstanceUID"], "PatientID": st.Row["PatientID"]}, se.Row))
				}
			}
		case "image":
			for _, se := range st.Series {
				for _, in := range se.Instances {
					if rowMatches(keys, st.Row, se.Row, in) {
						rows = append(rows, merged(map[string]string{"StudyInstanceUID": st.Row["StudyInstanceUID"],
							"SeriesInstanceUID": se.Row["SeriesInstanceUID"]}, in))
					}
				}
			}
		}
	}
	if offset > len(rows) {
		offset = len(rows)
	}
	rows = rows[offset:]
	more := len(rows) > limit
	if more {
		rows = rows[:limit]
	}
	if rows == nil {
		rows = []map[string]string{}
	}
	out := map[string]any{"instance": inst, "level": level, "rows": rows, "count": len(rows), "limit": limit, "more": more,
		"held_instances": s.Data.Held, "note": "Same matching rules and patient-conflict visibility as C-FIND."}
	if more {
		out["cursor"] = "c" + strconv.Itoa(offset+limit)
	}
	write(w, 200, out)
}

// sendPlan builds the plan of a send request, or answers with LibreDCM's
// refusal and returns false.
func (s *Server) sendPlan(w http.ResponseWriter, body map[string]any) (dcmclient.SendPlan, string, bool) {
	var p dcmclient.SendPlan
	dest, _ := body["destination"].(string)
	pid, _ := body["patient_id"].(string)
	uid, _ := body["study_instance_uid"].(string)
	var d *dcmclient.RemoteAE
	for i := range s.Cfg.RemoteAEs {
		if s.Cfg.RemoteAEs[i].Name == dest {
			d = &s.Cfg.RemoteAEs[i]
		}
	}
	if d == nil {
		fail(w, http.StatusNotFound, "unknown_destination", "no destination with this name is configured on this node; nothing was sent")
		return p, "", false
	}
	if pid == "" {
		fail(w, http.StatusUnprocessableEntity, "patient_id_required", "patient_id is required")
		return p, "", false
	}
	var st *Study
	for i := range s.Data.Studies {
		if s.Data.Studies[i].Row["StudyInstanceUID"] == uid {
			st = &s.Data.Studies[i]
		}
	}
	if st == nil {
		fail(w, http.StatusNotFound, "study_not_found", "no study with this Study Instance UID is indexed on this node")
		return p, "", false
	}
	if st.Row["PatientID"] != pid {
		fail(w, http.StatusUnprocessableEntity, "patient_mismatch", "the study is not indexed under this Patient ID; nothing was sent")
		return p, "", false
	}
	want := map[string]bool{}
	list, _ := body["series_instance_uids"].([]any)
	for _, v := range list {
		u, _ := v.(string)
		found := false
		for _, se := range st.Series {
			found = found || se.Row["SeriesInstanceUID"] == u
		}
		if !found {
			fail(w, http.StatusUnprocessableEntity, "series_not_in_study", "a series does not belong to the study")
			return p, "", false
		}
		want[u] = true
	}
	p = dcmclient.SendPlan{Destination: dcmclient.SendDestination{Name: d.Name, Host: d.Host, Port: d.Port, AETitle: d.AETitle},
		PatientID: st.Row["PatientID"], PatientName: st.Row["PatientName"], PatientBirthDate: st.Row["PatientBirthDate"],
		StudyInstanceUID: uid, StudyDate: st.Row["StudyDate"], StudyDescription: st.Row["StudyDescription"], AccessionNumber: st.Row["AccessionNumber"],
		Note: "Instances held as a patient conflict are never sent."}
	if s.Data.PlanPatient != "" {
		p.PatientID = s.Data.PlanPatient
	}
	if s.Data.PlanStudy != "" {
		p.StudyInstanceUID = s.Data.PlanStudy
	}
	if s.Data.PlanDest != "" {
		p.Destination.Name = s.Data.PlanDest
	}
	h := sha256.New()
	for _, se := range st.Series {
		if len(want) > 0 && !want[se.Row["SeriesInstanceUID"]] {
			continue
		}
		p.Series = append(p.Series, dcmclient.SendSeries{SeriesInstanceUID: se.Row["SeriesInstanceUID"], Modality: se.Row["Modality"], Instances: len(se.Instances)})
		p.Instances += len(se.Instances)
		for _, in := range se.Instances {
			h.Write([]byte(in["SOPInstanceUID"] + "\n"))
		}
	}
	p.SeriesCount = len(p.Series)
	p.InstancesDigest = "sha256:" + hex.EncodeToString(h.Sum(nil))
	raw, _ := json.Marshal(p)
	sum := sha256.Sum256(raw)
	return p, "sha256:" + hex.EncodeToString(sum[:]), true
}

// send serves the send plan and the send. The caller holds the lock and
// has checked the envelope of the send.
func (s *Server) send(w http.ResponseWriter, path string, body map[string]any, inst dcmclient.InstanceInfo) {
	p, digest, ok := s.sendPlan(w, body)
	if !ok {
		return
	}
	if path == "/control/v1/send/plan" {
		out := map[string]any{"instance": inst, "plan": p, "plan_digest": digest}
		if s.Data.NoDigest {
			delete(out, "plan_digest")
		}
		write(w, 200, out)
		return
	}
	if body["confirm"] != true {
		fail(w, http.StatusUnprocessableEntity, "not_confirmed", "send not confirmed")
		return
	}
	if got, _ := body["plan_digest"].(string); got == "" {
		fail(w, http.StatusUnprocessableEntity, "plan_digest_required", "plan_digest is required")
		return
	} else if got != digest {
		fail(w, http.StatusConflict, "plan_changed", "the study, the destination or the instances changed since the plan was made; nothing was sent. Ask for a new plan.")
		return
	}
	failed, completed := []string{}, 0
	for _, st := range s.Data.Studies {
		if st.Row["StudyInstanceUID"] != p.StudyInstanceUID {
			continue
		}
		for _, se := range st.Series {
			inPlan := false
			for _, ps := range p.Series {
				inPlan = inPlan || ps.SeriesInstanceUID == se.Row["SeriesInstanceUID"]
			}
			if !inPlan {
				continue
			}
			for _, in := range se.Instances {
				if s.Data.FailUIDs[in["SOPInstanceUID"]] {
					failed = append(failed, in["SOPInstanceUID"])
				} else {
					completed++
				}
			}
		}
	}
	out := map[string]any{"instance": inst, "plan": p, "instances": p.Instances, "completed": completed, "failed": len(failed), "warning": 0,
		"failed_sop_instance_uids": failed, "warning_sop_instance_uids": []string{}, "duration_ms": 345,
		"note": "Every instance of the plan that is not listed as failed or warning was accepted by the destination."}
	switch {
	case len(failed) == 0:
		out["outcome"] = "complete"
	case completed > 0:
		out["outcome"] = "partial"
	default:
		out["outcome"], out["code"], out["error"] = "failed", "send_failed", "no instance was accepted by the destination; see the node's log"
		write(w, http.StatusBadGateway, out)
		return
	}
	write(w, 200, out)
}

var levelRank = map[string]int{"DEBUG": 0, "INFO": 1, "WARN": 2, "ERROR": 3}

// ring serves the audit records and the log lines.
func (s *Server) ring(w http.ResponseWriter, r *http.Request, inst dcmclient.InstanceInfo) {
	q := r.URL.Query()
	audit := r.URL.Path == "/control/v1/audit"
	limit := 100
	if !audit {
		limit = 200
	}
	if v := q.Get("limit"); v != "" {
		limit, _ = strconv.Atoi(v)
	}
	var since time.Time
	if v := q.Get("since"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			fail(w, http.StatusBadRequest, "invalid_since", "since must be an RFC 3339 time")
			return
		}
		since = t
	}
	minLevel := levelRank[strings.ToUpper(q.Get("level"))]
	src := s.Data.Logs
	if audit {
		src = s.Data.Audit
		if s.Data.AuditLogOff {
			src = nil
		}
	}
	var keep []map[string]any
	for _, e := range src {
		ts, _ := e["time"].(string)
		t, _ := time.Parse(time.RFC3339, ts)
		ev, _ := e["event"].(string)
		oc, _ := e["outcome"].(string)
		lv, _ := e["level"].(string)
		switch {
		case !since.IsZero() && t.Before(since):
		case audit && !strings.HasPrefix(ev, q.Get("type")):
		case audit && q.Get("outcome") != "" && oc != q.Get("outcome"):
		case !audit && levelRank[lv] < minLevel:
		default:
			keep = append(keep, e)
		}
	}
	more := len(keep) > limit
	if more {
		keep = keep[len(keep)-limit:]
	}
	raw := []json.RawMessage{}
	for _, e := range keep {
		b, _ := json.Marshal(e)
		raw = append(raw, b)
	}
	out := map[string]any{"instance": inst, "count": len(raw), "more": more, "limit": limit, "buffer_size": 1000,
		"note": "Most recent entries held in memory on this node (test)."}
	if audit {
		out["records"], out["audit_log_off"] = raw, s.Data.AuditLogOff
	} else {
		out["lines"] = raw
	}
	write(w, 200, out)
}
