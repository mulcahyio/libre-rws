package web

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/mulcahyio/libre-rws/modules/wt/internal/dcmclient"
)

// Study search, study and series pages, and the send of a study or some of
// its series to a configured destination (WT-SRS-025, -026, -031, -033).
// Libre WT shows no images: it is not a diagnostic viewer.

const (
	actSearch    = "studies.search"
	actStudyView = "studies.view"
	actSend      = "send"

	studyPage    = 50  // study rows per page
	childPage    = 200 // series or instance rows per page (LibreDCM's maximum)
	maxField     = 64  // characters of one search value
	maxSendItems = 500 // series named in one send (LibreDCM's limit)
)

var (
	reUID      = regexp.MustCompile(`^[0-9]+(\.[0-9]+)*$`)
	reModality = regexp.MustCompile(`^[A-Z0-9]{1,16}$`)
	reDate     = regexp.MustCompile(`^([0-9]{4})-?([0-9]{2})-?([0-9]{2})$`)
	reCursor   = regexp.MustCompile(`^[A-Za-z0-9_=.-]{1,256}$`)
)

// validUID reports whether s is a DICOM UID of at most 64 characters.
func validUID(s string) bool { return len(s) <= 64 && reUID.MatchString(s) }

// searchForm is the study search as the person typed it.
type searchForm struct {
	PatientID, PatientName, DateFrom, DateTo, Modality, Accession, StudyUID string
}

// onlyWildcards reports whether a value asks for nothing: empty, or made of
// "*" only, which matches every value.
func onlyWildcards(s string) bool { return strings.Trim(s, "*") == "" }

// cleanField trims a search value and refuses control characters, the
// DICOM list separator and values longer than maxField.
func cleanField(label, v string) (string, error) {
	v = strings.TrimSpace(v)
	if len([]rune(v)) > maxField {
		return "", fmt.Errorf("%s is longer than %d characters", label, maxField)
	}
	for _, r := range v {
		if unicode.IsControl(r) || r == '\\' {
			return "", fmt.Errorf("%s holds a character that can't be searched for", label)
		}
	}
	return v, nil
}

// dicomDate turns a date of the form (YYYY-MM-DD or YYYYMMDD) into
// YYYYMMDD.
func dicomDate(label, v string) (string, error) {
	if v == "" {
		return "", nil
	}
	m := reDate.FindStringSubmatch(v)
	if m == nil {
		return "", fmt.Errorf("%s is not a date (YYYY-MM-DD)", label)
	}
	d := m[1] + m[2] + m[3]
	if _, err := time.Parse("20060102", d); err != nil {
		return "", fmt.Errorf("%s is not a date (YYYY-MM-DD)", label)
	}
	return d, nil
}

// keys turns the form into LibreDCM matching keys. A search without a
// value is refused here and never sent (WT-SRS-025).
func (f *searchForm) keys() (map[string]string, error) {
	var err error
	fields := []struct {
		label string
		v     *string
	}{{"Patient ID", &f.PatientID}, {"Patient name", &f.PatientName}, {"Accession number", &f.Accession},
		{"Modality", &f.Modality}, {"Study Instance UID", &f.StudyUID}, {"From", &f.DateFrom}, {"To", &f.DateTo}}
	for _, fl := range fields {
		if *fl.v, err = cleanField(fl.label, *fl.v); err != nil {
			return nil, err
		}
	}
	keys := map[string]string{}
	add := func(kw, v string) {
		if !onlyWildcards(v) {
			keys[kw] = v
		}
	}
	add("PatientID", f.PatientID)
	add("PatientName", f.PatientName)
	add("AccessionNumber", f.Accession)
	if f.Modality = strings.ToUpper(f.Modality); f.Modality != "" {
		if !reModality.MatchString(f.Modality) {
			return nil, fmt.Errorf("Modality must be a DICOM modality code such as CT or MR")
		}
		keys["ModalitiesInStudy"] = f.Modality
	}
	if f.StudyUID != "" {
		if !validUID(f.StudyUID) {
			return nil, fmt.Errorf("Study Instance UID must be digits and dots, at most 64 characters")
		}
		keys["StudyInstanceUID"] = f.StudyUID
	}
	from, err := dicomDate("From", f.DateFrom)
	if err != nil {
		return nil, err
	}
	to, err := dicomDate("To", f.DateTo)
	if err != nil {
		return nil, err
	}
	switch {
	case from != "" && to != "" && from > to:
		return nil, fmt.Errorf("the date From is after the date To")
	case from != "" && from == to:
		keys["StudyDate"] = from
	case from != "" || to != "":
		keys["StudyDate"] = from + "-" + to
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("give at least one search value (a Patient ID, a name, a date, an accession number, a modality or a Study Instance UID); \"*\" alone matches everything and is not a search value")
	}
	return keys, nil
}

// keysText prints the matching keys in a fixed order, for the audit log.
func keysText(keys map[string]string) string {
	names := make([]string, 0, len(keys))
	for k := range keys {
		names = append(names, k)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, k := range names {
		parts = append(parts, k+"="+keys[k])
	}
	return strings.Join(parts, " ")
}

// fmtDA prints a DICOM date YYYYMMDD as YYYY-MM-DD.
func fmtDA(s string) string {
	if len(s) == 8 && reDate.MatchString(s) {
		return s[:4] + "-" + s[4:6] + "-" + s[6:]
	}
	return s
}

// fmtPN prints a DICOM person name with spaces instead of "^".
func fmtPN(s string) string {
	return strings.TrimSpace(strings.Join(strings.FieldsFunc(s, func(r rune) bool { return r == '^' }), " "))
}

// studiesPage shows the empty search form.
func (s *Server) studiesPage(w http.ResponseWriter, r *http.Request, rc *reqCtx, ic *instCtx) {
	if !s.need(w, r, rc, ic, "studies", "Studies") {
		return
	}
	s.renderI(w, r, http.StatusOK, "studies", rc, ic, map[string]any{"F": searchForm{}})
}

// studiesSearch runs a study search: the values come by POST, so they never
// appear in an address. The search is recorded in the Libre WT audit log
// before it is sent; LibreDCM records it again under the person's name
// (WT-SRS-025, -033).
func (s *Server) studiesSearch(w http.ResponseWriter, r *http.Request, rc *reqCtx, ic *instCtx) {
	if !s.need(w, r, rc, ic, "studies", "Studies") {
		return
	}
	f := searchForm{PatientID: r.PostFormValue("patient_id"), PatientName: r.PostFormValue("patient_name"),
		DateFrom: r.PostFormValue("date_from"), DateTo: r.PostFormValue("date_to"), Modality: r.PostFormValue("modality"),
		Accession: r.PostFormValue("accession"), StudyUID: r.PostFormValue("study_uid")}
	data := map[string]any{"F": f}
	keys, err := f.keys()
	data["F"] = f
	if err != nil {
		data["Error"] = err.Error() + ". Nothing was searched."
		s.renderI(w, r, http.StatusUnprocessableEntity, "studies", rc, ic, data)
		return
	}
	cursor := r.PostFormValue("cursor")
	if cursor != "" && !reCursor.MatchString(cursor) {
		cursor = ""
	}
	target := clip(keysText(keys), 300)
	if !s.viewed(w, r, rc, ic, actSearch, target) {
		return
	}
	res, err := ic.client.Studies(r.Context(), dcmclient.StudyQuery{Level: "study", Keys: keys, Limit: studyPage, Cursor: cursor}, rc.user.Username)
	if err != nil {
		_ = s.auditI(r, rc.user.Username, ic.name, actSearch, target, "failure", clip(err.Error(), 300))
		data["Error"] = err.Error() + "."
		s.renderI(w, r, http.StatusBadGateway, "studies", rc, ic, data)
		return
	}
	if !s.sameNode(w, r, rc, ic, res.Instance) {
		return
	}
	data["R"], data["Searched"], data["Cursor"], data["ReadAt"] = res, true, cursor, time.Now()
	s.renderI(w, r, http.StatusOK, "studies", rc, ic, data)
}

// studyRow is the study a study page is about, with the row LibreDCM gave.
type studyView struct {
	Row       map[string]string
	PatientID string
	Rows      []map[string]string // every row LibreDCM returned for the UID
	// Sendable is false when the study can't be sent from the console
	// (held as a patient conflict, or listed under more than one patient);
	// Why says why.
	Sendable bool
	Why      string
}

// studyOf reads the study rows of one Study Instance UID and decides
// whether the console may offer to send it.
func studyOf(rows []map[string]string) studyView {
	v := studyView{Rows: rows}
	var plain []map[string]string
	for _, row := range rows {
		if row["ConflictID"] == "" {
			plain = append(plain, row)
		}
	}
	switch {
	case len(rows) == 0:
		v.Why = "LibreDCM lists no study with this Study Instance UID."
	case len(plain) == 0:
		v.Row = rows[0]
		v.Why = "The study is held as a patient conflict. Resolve it in Study Exceptions first; held instances are never sent from the console."
	case len(plain) > 1:
		v.Row = plain[0]
		v.Why = "LibreDCM lists this Study Instance UID under more than one patient. Nothing can be sent from the console until that is resolved in Study Exceptions."
	default:
		v.Row, v.Sendable = plain[0], true
	}
	if v.Row != nil {
		v.PatientID = v.Row["PatientID"]
	}
	return v
}

// studyPageH shows one study and its series (WT-SRS-025). An Admin gets
// the send form (WT-SRS-026).
func (s *Server) studyPageH(w http.ResponseWriter, r *http.Request, rc *reqCtx, ic *instCtx) {
	if !s.need(w, r, rc, ic, "studies", "Studies") {
		return
	}
	uid := r.PathValue("uid")
	if !validUID(uid) {
		s.renderI(w, r, http.StatusNotFound, "error", rc, ic, map[string]any{"Title": "Not a study", "Message": "The address does not hold a Study Instance UID.",
			"Back": ic.base() + "/studies"})
		return
	}
	if !s.viewed(w, r, rc, ic, actStudyView, uid) {
		return
	}
	st, err := ic.client.Studies(r.Context(), dcmclient.StudyQuery{Level: "study", Keys: map[string]string{"StudyInstanceUID": uid}, Limit: 10}, rc.user.Username)
	if err != nil {
		s.readFailed(w, r, rc, ic, actStudyView, uid, err)
		return
	}
	if !s.sameNode(w, r, rc, ic, st.Instance) {
		return
	}
	data := map[string]any{"UID": uid, "S": studyOf(st.Rows), "Held": st.Held, "ReadAt": time.Now(), "Back": ic.base() + "/studies"}
	cursor := r.URL.Query().Get("cursor")
	if cursor != "" && !reCursor.MatchString(cursor) {
		cursor = ""
	}
	se, err := ic.client.Studies(r.Context(), dcmclient.StudyQuery{Level: "series", Keys: map[string]string{"StudyInstanceUID": uid}, Limit: childPage, Cursor: cursor}, rc.user.Username)
	switch {
	case err != nil:
		data["SeriesErr"] = err.Error()
	case se.Instance.Identity() != ic.snap.Reported:
		data["SeriesErr"] = "another node answered than the one this page is for"
	default:
		data["Series"], data["SeriesMore"], data["SeriesCursor"] = se.Rows, se.More, se.Cursor
	}
	sv := data["S"].(studyView)
	if isAdmin(rc) && sv.Sendable && ic.snap.Supports("send") {
		allowed, why := ic.snap.ActionsAllowed()
		data["CanSend"], data["Allowed"], data["Blocked"] = true, allowed, why
		if ic.snap.Supports("remote-aes") {
			if ra, err := ic.client.RemoteAEs(r.Context()); err == nil && ra.Instance.Identity() == ic.snap.Reported {
				data["Destinations"] = ra.RemoteAEs
			} else if err != nil {
				data["DestErr"] = err.Error()
			}
		}
	}
	s.renderI(w, r, http.StatusOK, "study", rc, ic, data)
}

// seriesPage lists the instances of one series: identifiers only, no
// pixel data (WT-SRS-025).
func (s *Server) seriesPage(w http.ResponseWriter, r *http.Request, rc *reqCtx, ic *instCtx) {
	if !s.need(w, r, rc, ic, "studies", "Studies") {
		return
	}
	uid, suid := r.PathValue("uid"), r.PathValue("suid")
	if !validUID(uid) || !validUID(suid) {
		s.renderI(w, r, http.StatusNotFound, "error", rc, ic, map[string]any{"Title": "Not a series", "Message": "The address does not hold a Study and Series Instance UID.",
			"Back": ic.base() + "/studies"})
		return
	}
	if !s.viewed(w, r, rc, ic, actStudyView, uid+" series "+suid) {
		return
	}
	cursor := r.URL.Query().Get("cursor")
	if cursor != "" && !reCursor.MatchString(cursor) {
		cursor = ""
	}
	res, err := ic.client.Studies(r.Context(), dcmclient.StudyQuery{Level: "image",
		Keys: map[string]string{"StudyInstanceUID": uid, "SeriesInstanceUID": suid}, Limit: childPage, Cursor: cursor}, rc.user.Username)
	if err != nil {
		s.readFailed(w, r, rc, ic, actStudyView, uid, err)
		return
	}
	if !s.sameNode(w, r, rc, ic, res.Instance) {
		return
	}
	s.renderI(w, r, http.StatusOK, "series", rc, ic, map[string]any{"UID": uid, "SUID": suid, "R": res, "ReadAt": time.Now(),
		"Back": ic.base() + "/studies/" + uid})
}

// ---------------------------------------------------------------- send

// sendShown is what the warning page shows of a send: the patient, the
// study, the destination's address and the instances. The approval is
// bound to it.
func sendShown(p dcmclient.SendPlan) string {
	parts := []string{p.PatientID, p.PatientName, p.PatientBirthDate, p.StudyInstanceUID, p.StudyDate, p.StudyDescription, p.AccessionNumber,
		p.Destination.Name, p.Destination.Host, strconv.Itoa(p.Destination.Port), p.Destination.AETitle,
		strconv.Itoa(p.SeriesCount), strconv.Itoa(p.Instances), p.InstancesDigest}
	return strings.Join(parts, "\x00")
}

func sendSummary(p dcmclient.SendPlan) string {
	return fmt.Sprintf("send study %s of patient %s (%s) to %s=%s (AE %s): %d series, %d instances",
		p.StudyInstanceUID, p.PatientID, p.PatientName, p.Destination.Name,
		net.JoinHostPort(p.Destination.Host, strconv.Itoa(p.Destination.Port)), p.Destination.AETitle, p.SeriesCount, p.Instances)
}

// sendReview asks LibreDCM for the plan of a send and shows the warning
// page: patient, study, series and instances, destination address
// (WT-SRS-026, -031). The person names the patient (the Patient ID the
// study page showed) as well as the study; LibreDCM refuses a mismatch.
func (s *Server) sendReview(w http.ResponseWriter, r *http.Request, rc *reqCtx, ic *instCtx) {
	uid := r.PathValue("uid")
	back := ic.base() + "/studies/" + url.PathEscape(uid)
	if !s.need(w, r, rc, ic, "send", "Send") {
		return
	}
	bad := func(code int, msg string) {
		_ = s.auditI(r, rc.user.Username, ic.name, actSend+".review", clip(uid, 64), "failure", clip(msg, 600))
		s.renderI(w, r, code, "error", rc, ic, map[string]any{"Title": "Send: not possible", "Message": msg, "Back": back})
	}
	if !validUID(uid) {
		bad(http.StatusNotFound, "The address does not hold a Study Instance UID. Nothing was sent.")
		return
	}
	if s.blocked(w, r, rc, ic, actSend, uid, back) {
		return
	}
	req := dcmclient.SendRequest{Destination: strings.TrimSpace(r.PostFormValue("destination")),
		PatientID: r.PostFormValue("patient_id"), StudyInstanceUID: uid}
	if req.Destination == "" || len(req.Destination) > 64 {
		bad(http.StatusUnprocessableEntity, "Choose one of the configured destinations. Nothing was sent.")
		return
	}
	if req.PatientID == "" || len(req.PatientID) > 64 {
		bad(http.StatusUnprocessableEntity, "The Patient ID of the study is missing. Open the study again. Nothing was sent.")
		return
	}
	seen := map[string]bool{}
	for _, su := range r.PostForm["series"] {
		if !validUID(su) {
			bad(http.StatusUnprocessableEntity, "A chosen series is not a Series Instance UID. Nothing was sent.")
			return
		}
		if !seen[su] {
			seen[su] = true
			req.SeriesUIDs = append(req.SeriesUIDs, su)
		}
	}
	if len(req.SeriesUIDs) > maxSendItems {
		bad(http.StatusUnprocessableEntity, fmt.Sprintf("Too many series chosen (%d at most). Nothing was sent.", maxSendItems))
		return
	}
	ans, err := ic.client.SendPlan(r.Context(), req, ic.snap.Expect())
	if err != nil {
		s.planFailed(w, r, rc, ic, actSend, uid, "Send", back, err)
		return
	}
	p := ans.Plan
	switch {
	case ans.Instance.Identity() != ic.snap.Reported:
		bad(http.StatusConflict, "Another node answered than the one this page is for. Nothing was sent.")
		return
	case ans.PlanDigest == "":
		bad(http.StatusBadGateway, "LibreDCM sent a plan without a digest, so it can't be approved. Nothing was sent.")
		return
	case p.PatientID != req.PatientID || p.StudyInstanceUID != req.StudyInstanceUID || p.Destination.Name != req.Destination:
		// The plan must be about the patient, study and destination the
		// person chose; anything else is refused (wrong-patient guard).
		bad(http.StatusConflict, "LibreDCM's plan names another patient, study or destination than the one you chose. Nothing was sent. Open the study again.")
		return
	case p.Instances < 1:
		bad(http.StatusUnprocessableEntity, "The plan holds no instance to send. Nothing was sent.")
		return
	}
	pd := &pending{target: uid, send: &req, planDigest: ans.PlanDigest, shown: sendShown(p), summary: sendSummary(p)}
	tok := s.issueAction(r, rc, ic, actSend, pd)
	s.renderI(w, r, http.StatusOK, "sendreview", rc, ic, map[string]any{"P": p, "Addr": net.JoinHostPort(p.Destination.Host, strconv.Itoa(p.Destination.Port)),
		"Whole": len(req.SeriesUIDs) == 0, "Confirm": tok, "Back": back, "UID": uid})
}

// sendApply sends exactly what the warning page showed, once approved: the
// request comes from the server-side approval, never from the form, and
// LibreDCM refuses it when its plan no longer has the digest shown.
func (s *Server) sendApply(w http.ResponseWriter, r *http.Request, rc *reqCtx, ic *instCtx) {
	uid := r.PathValue("uid")
	back := ic.base() + "/studies/" + url.PathEscape(uid)
	const title = "Send"
	p, env, _, ok := s.approveAction(w, r, rc, ic, actSend, title, back, func(p *pending) (int, string) {
		// The approval belongs to the study in this request's address.
		if p.send == nil || p.target != uid {
			return http.StatusConflict, "This approval was given for another study. Nothing was sent. Review the send again."
		}
		return 0, ""
	})
	if !ok {
		return
	}
	res, err := ic.client.Send(r.Context(), *p.send, p.planDigest, env)
	if err != nil {
		s.actionFailed(w, r, rc, ic, actSend, title, back, p, env, err)
		return
	}
	outcome := "success"
	switch res.Outcome {
	case "complete":
	case "partial":
		outcome = "partial"
	default:
		outcome = "failure"
	}
	_ = s.auditI(r, rc.user.Username, ic.name, actSend, p.target, outcome,
		clip(fmt.Sprintf("%s: %d instances, %d completed, %d failed, %d warning, %d ms; failed: %s; request_id=%s",
			res.Outcome, res.Instances, res.Completed, res.Failed, res.Warning, res.DurationMS, strings.Join(res.FailedUIDs, " "), env.RequestID), 1000))
	code := http.StatusOK
	if res.Outcome != "complete" && res.Outcome != "partial" {
		code = http.StatusBadGateway
	}
	s.renderI(w, r, code, "sendresult", rc, ic, map[string]any{"R": res, "RequestID": env.RequestID, "Back": back})
}
