package gateway

// Tests of the functions the control API calls for an operator: the study
// query (DCM-SRS-091) and the send (DCM-SRS-092), against a real gateway
// with a real index and store, compared with C-FIND and C-MOVE.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/status"

	"github.com/mulcahyio/libre-rws/modules/dicom/internal/dcm"
)

const (
	qStudyA = "1.3.6.1.4.1.67686.9.1.81"
	qStudyB = "1.3.6.1.4.1.67686.9.1.82"
	qStudyC = "1.3.6.1.4.1.67686.9.1.83"
)

// queryImage is a synthetic image with the keys the query tests match on.
func queryImage(pid, name, study, series, sop, date, modality, accession string, n int) *godicom.Dataset {
	ds := patientImage(pid, name, study, series, sop, n)
	_ = ds.SetString(godicom.MustTag("StudyDate"), date)
	_ = ds.SetString(godicom.MustTag("Modality"), modality)
	_ = ds.SetString(godicom.MustTag("AccessionNumber"), accession)
	return ds
}

// queryNode holds three studies of two patients:
//
//	A  QP1 DOE^JANE     20260110  CT (2 instances) + SR (1)  ACC-A
//	B  QP1 DOE^JANE     20260220  CT (1)                     ACC-B
//	C  QP2 ROE^RICHARD  20260305  CT (3)                     ACC-C
func queryNode(t *testing.T, ctx context.Context, mod func(*Config)) *testNode {
	t.Helper()
	n := startNode(t, ctx, nil, mod)
	for _, ds := range []*godicom.Dataset{
		queryImage("QP1", "DOE^JANE", qStudyA, qStudyA+".1", qStudyA+".1.1", "20260110", "CT", "ACC-A", 1),
		queryImage("QP1", "DOE^JANE", qStudyA, qStudyA+".1", qStudyA+".1.2", "20260110", "CT", "ACC-A", 2),
		queryImage("QP1", "DOE^JANE", qStudyA, qStudyA+".2", qStudyA+".2.1", "20260110", "SR", "ACC-A", 1),
		queryImage("QP1", "DOE^JANE", qStudyB, qStudyB+".1", qStudyB+".1.1", "20260220", "CT", "ACC-B", 1),
		queryImage("QP2", "ROE^RICHARD", qStudyC, qStudyC+".1", qStudyC+".1.1", "20260305", "CT", "ACC-C", 1),
		queryImage("QP2", "ROE^RICHARD", qStudyC, qStudyC+".1", qStudyC+".1.2", "20260305", "CT", "ACC-C", 2),
		queryImage("QP2", "ROE^RICHARD", qStudyC, qStudyC+".1", qStudyC+".1.3", "20260305", "CT", "ACC-C", 3),
	} {
		if st := storeAs(t, ctx, n.addr, ds); st != 0 {
			t.Fatalf("store = %04X", st)
		}
	}
	return n
}

// uidKey is the unique key of a level.
var uidKey = map[string]string{"PATIENT": "PatientID", "STUDY": "StudyInstanceUID", "SERIES": "SeriesInstanceUID", "IMAGE": "SOPInstanceUID"}

// viaControl runs a query through QueryIndex and returns the unique keys of
// the rows, in the order returned.
func viaControl(t *testing.T, ctx context.Context, n *testNode, level string, keys map[string]string) []string {
	t.Helper()
	res, err := n.gw.QueryIndex(ctx, IndexQuery{Level: level, Keys: keys, Limit: 1000})
	if err != nil {
		t.Fatalf("QueryIndex %s %v: %v", level, keys, err)
	}
	var out []string
	for _, r := range res.Rows {
		out = append(out, r[uidKey[strings.ToUpper(level)]])
	}
	return out
}

// viaCFind runs the same query as a C-FIND and returns the unique keys.
func viaCFind(t *testing.T, ctx context.Context, n *testNode, level string, keys map[string]string) []string {
	t.Helper()
	ident := map[string]string{uidKey[level]: ""}
	for k, v := range keys {
		ident[k] = v
	}
	var out []string
	if level != "PATIENT" {
		for _, ds := range cfind(t, ctx, n.addr, level, ident) {
			out = append(out, dcm.String(ds, uidKey[level]))
		}
		return out
	}
	// The patient level exists in the Patient Root model only.
	model := ae.PatientRootQueryRetrieveInformationModelFind
	q := godicom.NewDataset()
	_ = q.SetString(godicom.MustTag("QueryRetrieveLevel"), level)
	for k, v := range ident {
		_ = q.SetString(godicom.MustTag(k), v)
	}
	scu, err := ae.Dial(ctx, ae.Config{AETitle: "VIEWER", PresentationContexts: []ae.PresentationContext{{
		AbstractSyntax: model, TransferSyntaxes: []string{dcm.ImplicitVRLittleEndian}}}}, n.addr, "RWS_TEST")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = scu.Release(ctx) }()
	ms, err := scu.CFind(ctx, ae.FindRequest{QueryModel: model, IdentifierData: q})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range ms {
		if m.Status == status.Pending && m.Identifier != nil {
			out = append(out, dcm.String(m.Identifier, uidKey[level]))
		}
	}
	return out
}

func sorted(s []string) string {
	c := append([]string{}, s...)
	sort.Strings(c)
	return fmt.Sprint(c)
}

func refusalCode(err error) string {
	var re *RequestError
	if errors.As(err, &re) {
		return re.Code
	}
	return ""
}

// The study query of the control API answers every query exactly as C-FIND
// does, at every level: single value, wildcard (case-insensitive person
// name), date range, UID list, modalities in study. A query that nothing
// restricts, an unknown key and a key of a lower level are refused instead
// of being answered with more than was asked for.
// Verifies: DCM-SRS-091
func TestQueryIndexMatchesCFind(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	n := queryNode(t, ctx, nil)

	for _, c := range []struct {
		level string
		keys  map[string]string
		want  int
	}{
		{"STUDY", map[string]string{"PatientID": "QP1"}, 2},
		{"STUDY", map[string]string{"PatientID": "QP*"}, 3},
		{"STUDY", map[string]string{"PatientName": "doe*"}, 2},
		{"STUDY", map[string]string{"PatientName": "ROE^RICHARD"}, 1},
		{"STUDY", map[string]string{"StudyDate": "20260201-20260331"}, 2},
		{"STUDY", map[string]string{"StudyDate": "-20260115"}, 1},
		{"STUDY", map[string]string{"StudyDate": "20260220"}, 1},
		{"STUDY", map[string]string{"AccessionNumber": "ACC-C"}, 1},
		{"STUDY", map[string]string{"AccessionNumber": "ACC-?"}, 3},
		{"STUDY", map[string]string{"ModalitiesInStudy": "SR"}, 1},
		{"STUDY", map[string]string{"ModalitiesInStudy": "CT"}, 3},
		{"STUDY", map[string]string{"StudyInstanceUID": qStudyA + `\` + qStudyC}, 2},
		{"STUDY", map[string]string{"PatientID": "QP1", "StudyDate": "20260220-"}, 1},
		{"STUDY", map[string]string{"PatientID": "NOBODY"}, 0},
		{"PATIENT", map[string]string{"PatientID": "QP*"}, 2},
		{"PATIENT", map[string]string{"PatientName": "roe*"}, 1},
		{"SERIES", map[string]string{"StudyInstanceUID": qStudyA}, 2},
		{"SERIES", map[string]string{"StudyInstanceUID": qStudyA, "Modality": "SR"}, 1},
		{"SERIES", map[string]string{"PatientID": "QP2"}, 1},
		{"IMAGE", map[string]string{"StudyInstanceUID": qStudyC}, 3},
		{"IMAGE", map[string]string{"SeriesInstanceUID": qStudyA + ".1"}, 2},
		{"IMAGE", map[string]string{"StudyInstanceUID": qStudyA, "InstanceNumber": "1"}, 2},
		{"IMAGE", map[string]string{"PatientID": "QP1"}, 4},
	} {
		got, find := viaControl(t, ctx, n, strings.ToLower(c.level), c.keys), viaCFind(t, ctx, n, c.level, c.keys)
		if len(got) != c.want {
			t.Errorf("%s %v: %d rows, want %d", c.level, c.keys, len(got), c.want)
		}
		if sorted(got) != sorted(find) {
			t.Errorf("%s %v: control query %v, C-FIND %v", c.level, c.keys, sorted(got), sorted(find))
		}
	}

	// Rows carry the indexed attributes, and no internal column.
	res, err := n.gw.QueryIndex(ctx, IndexQuery{Level: "study", Keys: map[string]string{"AccessionNumber": "ACC-A"}, Limit: 10})
	if err != nil || len(res.Rows) != 1 {
		t.Fatalf("study A: %v %v", res, err)
	}
	row := res.Rows[0]
	if row["PatientID"] != "QP1" || row["PatientName"] != "DOE^JANE" || row["StudyDate"] != "20260110" ||
		row["NumberOfStudyRelatedInstances"] != "3" || row["NumberOfStudyRelatedSeries"] != "2" || row["ModalitiesInStudy"] != `CT\SR` {
		t.Fatalf("study row = %v", row)
	}
	if res.Level != "study" || res.More || res.Filters != 1 || res.Held != VisibilityHidden {
		t.Fatalf("result = %+v", res)
	}
	img, err := n.gw.QueryIndex(ctx, IndexQuery{Level: "IMAGE", Keys: map[string]string{"SOPInstanceUID": qStudyA + ".1.1"}, Limit: 10})
	if err != nil || len(img.Rows) != 1 || img.Rows[0]["TransferSyntaxUID"] == "" {
		t.Fatalf("image row = %v %v", img.Rows, err)
	}
	for _, r := range append(res.Rows, img.Rows...) {
		for k := range r {
			if strings.HasPrefix(k, "_") {
				t.Errorf("internal column %s returned", k)
			}
		}
	}

	// Refusals.
	for name, c := range map[string]struct {
		q    IndexQuery
		code string
	}{
		"no key":             {IndexQuery{Level: "study", Limit: 10}, "no_matching_key"},
		"only empty":         {IndexQuery{Level: "study", Keys: map[string]string{"PatientID": ""}, Limit: 10}, "no_matching_key"},
		"only star":          {IndexQuery{Level: "study", Keys: map[string]string{"PatientID": "*", "PatientName": ""}, Limit: 10}, "no_matching_key"},
		"unknown key":        {IndexQuery{Level: "study", Keys: map[string]string{"PatientID": "QP1", "OtherPatientIDs": "x"}, Limit: 10}, "unknown_key"},
		"return-only key":    {IndexQuery{Level: "study", Keys: map[string]string{"NumberOfStudyRelatedInstances": "3"}, Limit: 10}, "unknown_key"},
		"key of lower level": {IndexQuery{Level: "study", Keys: map[string]string{"PatientID": "QP1", "SeriesInstanceUID": qStudyA + ".1"}, Limit: 10}, "key_not_at_level"},
		"modality at study":  {IndexQuery{Level: "patient", Keys: map[string]string{"PatientID": "QP1", "ModalitiesInStudy": "CT"}, Limit: 10}, "key_not_at_level"},
		"bad level":          {IndexQuery{Level: "everything", Keys: map[string]string{"PatientID": "QP1"}, Limit: 10}, "invalid_level"},
		"no level":           {IndexQuery{Keys: map[string]string{"PatientID": "QP1"}, Limit: 10}, "invalid_level"},
		"limit 0":            {IndexQuery{Level: "study", Keys: map[string]string{"PatientID": "QP1"}}, "invalid_limit"},
		"negative offset":    {IndexQuery{Level: "study", Keys: map[string]string{"PatientID": "QP1"}, Limit: 5, Offset: -1}, "invalid_limit"},
	} {
		res, err := n.gw.QueryIndex(ctx, c.q)
		if refusalCode(err) != c.code || len(res.Rows) != 0 {
			t.Errorf("%s: err %v (code %q), %d rows; want refusal %s", name, err, refusalCode(err), len(res.Rows), c.code)
		}
	}
}

// The result is paged: each page holds at most the limit, says whether more
// rows exist, and the pages together are the whole result, each row once.
// Verifies: DCM-SRS-091, DCM-SRS-096
func TestQueryIndexPages(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	n := queryNode(t, ctx, nil)
	keys := map[string]string{"PatientID": "QP*"}
	all := viaControl(t, ctx, n, "image", keys)
	if len(all) != 7 {
		t.Fatalf("images = %d", len(all))
	}
	for _, limit := range []int{1, 2, 3, 7, 50} {
		var got []string
		for off := 0; ; {
			res, err := n.gw.QueryIndex(ctx, IndexQuery{Level: "image", Keys: keys, Limit: limit, Offset: off})
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Rows) > limit {
				t.Fatalf("limit %d: page of %d rows", limit, len(res.Rows))
			}
			for _, r := range res.Rows {
				got = append(got, r["SOPInstanceUID"])
			}
			off += len(res.Rows)
			if want := off < len(all); res.More != want {
				t.Fatalf("limit %d offset %d: more = %v, want %v", limit, off, res.More, want)
			}
			if !res.More {
				break
			}
		}
		if fmt.Sprint(got) != fmt.Sprint(all) {
			t.Errorf("limit %d: pages give %v, want %v", limit, got, all)
		}
	}
	// A page after the end is empty and says so.
	if res, err := n.gw.QueryIndex(ctx, IndexQuery{Level: "image", Keys: keys, Limit: 5, Offset: 7}); err != nil || len(res.Rows) != 0 || res.More {
		t.Fatalf("page after the end = %+v %v", res, err)
	}
}

// Held conflict instances are as visible to the study query as to C-FIND:
// not at all while visibility is hidden (the default), and under their own
// Patient ID, marked with the conflict, when it is flagged.
// Verifies: DCM-SRS-091, DCM-SRS-073
func TestQueryIndexConflictVisibility(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Hidden: P1 has two indexed instances; P2's two held instances exist
	// in the conflict tables only.
	hidden, conflictID := heldNode(t, ctx, nil)
	for _, c := range []struct {
		level string
		keys  map[string]string
		want  int
	}{
		{"STUDY", map[string]string{"PatientID": "P2"}, 0},
		{"STUDY", map[string]string{"PatientID": "P*"}, 1},
		{"STUDY", map[string]string{"PatientName": "ROE*"}, 0},
		{"STUDY", map[string]string{"StudyInstanceUID": cStudy}, 1},
		{"PATIENT", map[string]string{"PatientID": "P*"}, 1},
		{"SERIES", map[string]string{"StudyInstanceUID": cStudy}, 1},
		{"IMAGE", map[string]string{"StudyInstanceUID": cStudy}, 2},
		{"IMAGE", map[string]string{"SOPInstanceUID": cSeries + ".3"}, 0},
		{"IMAGE", map[string]string{"PatientID": "P2"}, 0},
	} {
		got, find := viaControl(t, ctx, hidden, strings.ToLower(c.level), c.keys), viaCFind(t, ctx, hidden, c.level, c.keys)
		if len(got) != c.want || sorted(got) != sorted(find) {
			t.Errorf("hidden: %s %v: control query %v, C-FIND %v, want %d rows", c.level, c.keys, got, find, c.want)
		}
	}
	res, err := hidden.gw.QueryIndex(ctx, IndexQuery{Level: "study", Keys: map[string]string{"StudyInstanceUID": cStudy}, Limit: 10})
	if err != nil || len(res.Rows) != 1 || res.Rows[0]["PatientID"] != "P1" || res.Rows[0]["NumberOfStudyRelatedInstances"] != "2" ||
		res.Rows[0]["ConflictID"] != "" || res.Held != VisibilityHidden {
		t.Fatalf("hidden: study = %+v %v", res, err)
	}

	// Flagged: the held instances are a second entry under P2.
	flagged, flaggedID := heldNode(t, ctx, func(c *Config) { c.PatientConflictVisibility = VisibilityFlagged })
	for _, c := range []struct {
		level string
		keys  map[string]string
		want  int
	}{
		{"STUDY", map[string]string{"PatientID": "P2"}, 1},
		{"STUDY", map[string]string{"StudyInstanceUID": cStudy}, 2},
		{"PATIENT", map[string]string{"PatientID": "P*"}, 2},
		{"IMAGE", map[string]string{"StudyInstanceUID": cStudy}, 4},
		{"IMAGE", map[string]string{"PatientID": "P2"}, 2},
	} {
		got, find := viaControl(t, ctx, flagged, strings.ToLower(c.level), c.keys), viaCFind(t, ctx, flagged, c.level, c.keys)
		if len(got) != c.want || sorted(got) != sorted(find) {
			t.Errorf("flagged: %s %v: control query %v, C-FIND %v, want %d rows", c.level, c.keys, got, find, c.want)
		}
	}
	res, err = flagged.gw.QueryIndex(ctx, IndexQuery{Level: "study", Keys: map[string]string{"PatientID": "P2"}, Limit: 10})
	if err != nil || len(res.Rows) != 1 || res.Rows[0]["ConflictID"] != flaggedID || res.Held != VisibilityFlagged {
		t.Fatalf("flagged: P2 study = %+v %v", res, err)
	}
	_ = conflictID
}

func sopsOf(got map[string]string) string {
	var out []string
	for sop := range got {
		out = append(out, strings.TrimPrefix(strings.TrimPrefix(sop, qStudyA), cSeries))
	}
	sort.Strings(out)
	return strings.Join(out, " ")
}

// A send goes to a configured destination only, needs the Patient ID the
// study is indexed under, and sends exactly the instances of the plan: the
// whole study, chosen series or chosen instances. The plan shows where the
// images go, whose they are and how many. Nothing is sent when the request
// is refused or when the check of the plan refuses it.
// Verifies: DCM-SRS-092
func TestSendPlanAndSend(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	dest, received := moveDest(t, ctx)
	n := queryNode(t, ctx, func(c *Config) { c.RemoteAEs = map[string]RemoteAE{"DEST": dest} })
	base := SendRequest{Destination: "DEST", StudyUID: qStudyA, PatientID: "QP1"}

	plan, err := n.gw.PlanSend(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Destination != (SendDestination{Name: "DEST", Host: dest.Host, Port: dest.Port, AETitle: "DEST"}) ||
		plan.PatientID != "QP1" || plan.PatientName != "DOE^JANE" || plan.StudyInstanceUID != qStudyA || plan.StudyDate != "20260110" ||
		plan.AccessionNumber != "ACC-A" || plan.SeriesCount != 2 || plan.Instances != 3 || len(plan.Series) != 2 ||
		!strings.HasPrefix(plan.InstancesDigest, "sha256:") || plan.Note == "" {
		t.Fatalf("plan = %+v", plan)
	}
	bySeries := map[string]SendSeries{}
	for _, s := range plan.Series {
		bySeries[s.SeriesInstanceUID] = s
	}
	if bySeries[qStudyA+".1"] != (SendSeries{qStudyA + ".1", "CT", 2}) || bySeries[qStudyA+".2"] != (SendSeries{qStudyA + ".2", "SR", 1}) {
		t.Fatalf("plan series = %+v", plan.Series)
	}
	if again, _ := n.gw.PlanSend(ctx, base); again.InstancesDigest != plan.InstancesDigest {
		t.Fatal("the plan of an unchanged study is not stable")
	}

	// Refusals: nothing is sent.
	with := func(edit func(*SendRequest)) SendRequest { r := base; edit(&r); return r }
	for name, c := range map[string]struct {
		req  SendRequest
		code string
	}{
		"unknown destination":     {with(func(r *SendRequest) { r.Destination = "NOWHERE" }), "unknown_destination"},
		"no destination":          {with(func(r *SendRequest) { r.Destination = "" }), "unknown_destination"},
		"no study":                {with(func(r *SendRequest) { r.StudyUID = "" }), "study_required"},
		"no patient":              {with(func(r *SendRequest) { r.PatientID = "" }), "patient_id_required"},
		"another patient":         {with(func(r *SendRequest) { r.PatientID = "QP2" }), "patient_mismatch"},
		"patient in other case":   {with(func(r *SendRequest) { r.PatientID = "qp1" }), "patient_mismatch"},
		"patient wildcard":        {with(func(r *SendRequest) { r.PatientID = "QP*" }), "patient_mismatch"},
		"unknown study":           {with(func(r *SendRequest) { r.StudyUID = qStudyA + ".404" }), "study_not_found"},
		"series of another study": {with(func(r *SendRequest) { r.SeriesUIDs = []string{qStudyA + ".1", qStudyC + ".1"} }), "series_not_in_study"},
		"only a foreign series":   {with(func(r *SendRequest) { r.SeriesUIDs = []string{qStudyC + ".1"} }), "nothing_to_send"},
		"instance of other study": {with(func(r *SendRequest) { r.SOPInstanceUIDs = []string{qStudyA + ".1.1", qStudyC + ".1.1"} }), "instance_not_in_study"},
		"instance of other series": {with(func(r *SendRequest) {
			r.SeriesUIDs = []string{qStudyA + ".2"}
			r.SOPInstanceUIDs = []string{qStudyA + ".1.1"}
		}), "nothing_to_send"},
	} {
		if _, err := n.gw.PlanSend(ctx, c.req); refusalCode(err) != c.code {
			t.Errorf("plan, %s: %v (code %q), want %s", name, err, refusalCode(err), c.code)
		}
		if res, err := n.gw.Send(ctx, c.req, nil); refusalCode(err) != c.code || res.Completed != 0 {
			t.Errorf("send, %s: %v (code %q), want %s", name, err, refusalCode(err), c.code)
		}
	}
	// The check of the plan refuses: nothing is sent, and the error is the check's.
	errNo := errors.New("not the approved plan")
	var seen SendPlan
	if res, err := n.gw.Send(ctx, base, func(p SendPlan) error { seen = p; return errNo }); !errors.Is(err, errNo) || res.Completed != 0 {
		t.Fatalf("send with a refusing check = %+v %v", res, err)
	}
	if seen.InstancesDigest != plan.InstancesDigest || seen.Instances != 3 {
		t.Fatalf("the check saw %+v, not the plan", seen)
	}
	if got := received(); len(got) != 0 {
		t.Fatalf("refused sends delivered %v", got)
	}

	// Chosen instance, then chosen series, then the whole study.
	res, err := n.gw.Send(ctx, with(func(r *SendRequest) { r.SOPInstanceUIDs = []string{qStudyA + ".1.2"} }), nil)
	if err != nil || res.Completed != 1 || res.Failed != 0 || res.Plan.Instances != 1 || sopsOf(received()) != ".1.2" {
		t.Fatalf("one instance: %+v %v, received %v", res, err, sopsOf(received()))
	}
	res, err = n.gw.Send(ctx, with(func(r *SendRequest) { r.SeriesUIDs = []string{qStudyA + ".2"} }), nil)
	if err != nil || res.Completed != 1 || res.Plan.SeriesCount != 1 || sopsOf(received()) != ".1.2 .2.1" {
		t.Fatalf("one series: %+v %v, received %v", res, err, sopsOf(received()))
	}
	var approved SendPlan
	res, err = n.gw.Send(ctx, base, func(p SendPlan) error { approved = p; return nil })
	if err != nil || res.Completed != 3 || res.Failed != 0 || res.Warning != 0 || len(res.FailedUIDs) != 0 ||
		res.Plan.InstancesDigest != approved.InstancesDigest || sopsOf(received()) != ".1.1 .1.2 .2.1" {
		t.Fatalf("whole study: %+v %v, received %v", res, err, sopsOf(received()))
	}
	for sop, pid := range received() {
		if pid != "QP1" {
			t.Errorf("instance %s arrived as patient %q", sop, pid)
		}
	}

	// One send is bounded: a study over the limit is refused whole, not
	// cut; at the limit it goes out (DCM-SRS-096).
	if MaxSendInstances != 5000 || sendLimit != MaxSendInstances {
		t.Fatalf("send limit = %d / %d", MaxSendInstances, sendLimit)
	}
	sendLimit = 2
	before := len(received())
	if res, err := n.gw.Send(ctx, base, nil); refusalCode(err) != "too_many_instances" || res.Completed != 0 || len(received()) != before {
		t.Fatalf("send of 3 instances with a limit of 2 = %+v %v", res, err)
	}
	if _, err := n.gw.PlanSend(ctx, base); refusalCode(err) != "too_many_instances" {
		t.Fatalf("plan of 3 instances with a limit of 2: %v", err)
	}
	if p, err := n.gw.PlanSend(ctx, with(func(r *SendRequest) { r.SeriesUIDs = []string{qStudyA + ".1"} })); err != nil || p.Instances != 2 {
		t.Fatalf("plan of 2 instances with a limit of 2 = %+v %v", p, err)
	}
	sendLimit = MaxSendInstances

	// A new instance in the study changes the plan: an approval given
	// before it arrived does not cover it.
	if st := storeAs(t, ctx, n.addr, queryImage("QP1", "DOE^JANE", qStudyA, qStudyA+".2", qStudyA+".2.2", "20260110", "SR", "ACC-A", 2)); st != 0 {
		t.Fatalf("store = %04X", st)
	}
	if later, _ := n.gw.PlanSend(ctx, base); later.InstancesDigest == plan.InstancesDigest || later.Instances != 4 {
		t.Fatalf("plan after a new instance = %+v", later)
	}

	// A destination that does not answer: every instance is reported failed, by UID.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	down := RemoteAE{Host: "127.0.0.1", Port: ln.Addr().(*net.TCPAddr).Port}
	_ = ln.Close()
	n.gw.SetRemoteAEs(map[string]RemoteAE{"DEST": dest, "DOWN": down})
	res, err = n.gw.Send(ctx, SendRequest{Destination: "DOWN", StudyUID: qStudyC, PatientID: "QP2"}, nil)
	if err != nil || res.Completed != 0 || res.Failed != 3 || sorted(res.FailedUIDs) != sorted([]string{qStudyC + ".1.1", qStudyC + ".1.2", qStudyC + ".1.3"}) {
		t.Fatalf("send to a dead destination = %+v %v", res, err)
	}
}

// Instances held as a patient conflict are never part of a send, also when
// they are visible to queries (flagged) and also when the request names
// the held patient or a held instance.
// Verifies: DCM-SRS-092, DCM-SRS-073
func TestSendNeverSendsHeldInstances(t *testing.T) {
	for _, visibility := range []string{VisibilityHidden, VisibilityFlagged} {
		t.Run(visibility, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			dest, received := moveDest(t, ctx)
			n, _ := heldNode(t, ctx, func(c *Config) {
				c.PatientConflictVisibility = visibility
				c.RemoteAEs = map[string]RemoteAE{"DEST": dest}
			})
			// The held patient cannot be named for this study.
			for _, req := range []SendRequest{
				{Destination: "DEST", StudyUID: cStudy, PatientID: "P2"},
				{Destination: "DEST", StudyUID: cStudy, PatientID: "P2", SOPInstanceUIDs: []string{cSeries + ".3"}},
			} {
				if res, err := n.gw.Send(ctx, req, nil); refusalCode(err) != "patient_mismatch" || res.Completed != 0 {
					t.Fatalf("send as the held patient = %+v %v", res, err)
				}
			}
			// A held instance cannot be picked under the indexed patient.
			if _, err := n.gw.Send(ctx, SendRequest{Destination: "DEST", StudyUID: cStudy, PatientID: "P1",
				SOPInstanceUIDs: []string{cSeries + ".1", cSeries + ".3"}}, nil); refusalCode(err) != "instance_not_in_study" {
				t.Fatalf("send of a held instance: %v", err)
			}
			if len(received()) != 0 {
				t.Fatalf("refused sends delivered %v", received())
			}
			// The whole study: the two indexed instances, as the indexed patient.
			plan, err := n.gw.PlanSend(ctx, SendRequest{Destination: "DEST", StudyUID: cStudy, PatientID: "P1"})
			if err != nil || plan.Instances != 2 || plan.PatientID != "P1" || plan.PatientName != "DOE^JANE" {
				t.Fatalf("plan = %+v %v", plan, err)
			}
			res, err := n.gw.Send(ctx, SendRequest{Destination: "DEST", StudyUID: cStudy, PatientID: "P1"}, nil)
			if err != nil || res.Completed != 2 || res.Failed != 0 {
				t.Fatalf("send = %+v %v", res, err)
			}
			got := received()
			if sopsOf(got) != ".1 .2" || got[cSeries+".1"] != "P1" || got[cSeries+".2"] != "P1" {
				t.Fatalf("destination received %v: want the two indexed instances of P1 only", got)
			}
		})
	}
}

// Verifies: DCM-SRS-093
func TestValidateRemoteAE(t *testing.T) {
	ok := RemoteAE{Host: "10.0.0.20", Port: 104, AETitle: "VIEWER1"}
	for name, c := range map[string]struct {
		name string
		r    RemoteAE
		ok   bool
	}{
		"plain":            {"VIEWER1", ok, true},
		"no called AE":     {"VIEWER1", RemoteAE{Host: "viewer-1.example", Port: 11112}, true},
		"IPv6":             {"V6", RemoteAE{Host: "fd00::20", Port: 104}, true},
		"16 characters":    {"ABCDEFGHIJKLMNOP", ok, true},
		"empty name":       {"", ok, false},
		"17 characters":    {"ABCDEFGHIJKLMNOPQ", ok, false},
		"name with space":  {" VIEWER1", ok, false},
		"name backslash":   {`VIEW\ER`, ok, false},
		"name newline":     {"VIEW\nER", ok, false},
		"long called AE":   {"VIEWER1", RemoteAE{Host: "10.0.0.20", Port: 104, AETitle: "ABCDEFGHIJKLMNOPQ"}, false},
		"no host":          {"VIEWER1", RemoteAE{Port: 104}, false},
		"host with slash":  {"VIEWER1", RemoteAE{Host: "10.0.0.20/x", Port: 104}, false},
		"host with space":  {"VIEWER1", RemoteAE{Host: "viewer 1", Port: 104}, false},
		"host and port":    {"VIEWER1", RemoteAE{Host: "10.0.0.20:104", Port: 104}, false},
		"port 0":           {"VIEWER1", RemoteAE{Host: "10.0.0.20"}, false},
		"port too high":    {"VIEWER1", RemoteAE{Host: "10.0.0.20", Port: 65536}, false},
		"negative port":    {"VIEWER1", RemoteAE{Host: "10.0.0.20", Port: -1}, false},
		"host 254 letters": {"VIEWER1", RemoteAE{Host: strings.Repeat("a", 254), Port: 104}, false},
	} {
		if err := ValidateRemoteAE(c.name, c.r); (err == nil) != c.ok {
			t.Errorf("%s: err = %v, want ok = %v", name, err, c.ok)
		}
	}
}

// AllowListPermits gives the answer of the association-time check for a
// list in configuration form.
// Verifies: DCM-SRS-095, DCM-SRS-005
func TestAllowListPermits(t *testing.T) {
	list := []string{"CT1", "MR1@10.0.0.5", "US1@192.168.7.0/24"}
	for _, c := range []struct {
		list   []string
		ae     string
		remote string
		want   bool
	}{
		{nil, "ANY", "10.9.9.9:5000", true}, // an empty list refuses nobody
		{list, "CT1", "10.9.9.9:5000", true},
		{list, "CT2", "10.9.9.9:5000", false},
		{list, "MR1", "10.0.0.5:40000", true},
		{list, "MR1", "10.0.0.6:40000", false},
		{list, "US1", "192.168.7.44:1234", true},
		{list, "US1", "192.168.8.44:1234", false},
		{list, "MR1", "not-an-address", false},
	} {
		got, err := AllowListPermits(c.list, c.ae, c.remote)
		if err != nil || got != c.want {
			t.Errorf("%v %s from %s = %v %v, want %v", c.list, c.ae, c.remote, got, err, c.want)
		}
	}
	if _, err := AllowListPermits([]string{"CT1@not-a-network"}, "CT1", "10.0.0.5:1"); err == nil {
		t.Error("an invalid list gave no error")
	}
}
