package gateway

import (
	"context"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/godicom/uid"
	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/status"

	"github.com/mulcahyio/libre-rws/modules/dicom/internal/dcm"
	"github.com/mulcahyio/libre-rws/modules/dicom/internal/index"
)

const (
	cStudy  = "1.2.826.0.1.3680043.10.999.70"
	cSeries = cStudy + ".1"
)

func patientImage(pid, name, study, series, sop string, n int) *godicom.Dataset {
	ds := ctImage(study, series, sop, n)
	_ = ds.SetString(godicom.MustTag("PatientID"), pid)
	_ = ds.SetString(godicom.MustTag("PatientName"), name)
	return ds
}

func storeAs(t *testing.T, ctx context.Context, addr string, ds *godicom.Dataset) uint16 {
	t.Helper()
	scu, err := ae.Dial(ctx, ae.Config{AETitle: "MODALITY", PresentationContexts: storeContexts()}, addr, "RWS_TEST")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = scu.Release(ctx) }()
	res, err := scu.CStore(ctx, ae.StoreRequest{AffectedSOPClassUID: string(uid.CTImageStorage),
		AffectedSOPInstanceUID: dcm.String(ds, "SOPInstanceUID"), Data: ds})
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	return res.Status
}

func cfind(t *testing.T, ctx context.Context, addr, level string, keys map[string]string) []*godicom.Dataset {
	t.Helper()
	q := godicom.NewDataset()
	_ = q.SetString(godicom.MustTag("QueryRetrieveLevel"), level)
	for k, v := range keys {
		_ = q.SetString(godicom.MustTag(k), v)
	}
	scu, err := ae.Dial(ctx, ae.Config{AETitle: "VIEWER", PresentationContexts: []ae.PresentationContext{{
		AbstractSyntax: ae.StudyRootQueryRetrieveInformationModelFind, TransferSyntaxes: []string{dcm.ImplicitVRLittleEndian},
	}}}, addr, "RWS_TEST")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = scu.Release(ctx) }()
	ms, err := scu.CFind(ctx, ae.FindRequest{QueryModel: ae.StudyRootQueryRetrieveInformationModelFind, IdentifierData: q})
	if err != nil {
		t.Fatal(err)
	}
	var out []*godicom.Dataset
	for _, m := range ms {
		if m.Status == status.Pending && m.Identifier != nil {
			out = append(out, m.Identifier)
		}
	}
	return out
}

// moveDest starts a C-MOVE destination and returns its config and a function
// listing received SOP instance UID -> Patient ID.
func moveDest(t *testing.T, ctx context.Context) (RemoteAE, func() map[string]string) {
	t.Helper()
	var mu sync.Mutex
	got := map[string]string{}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		_ = ae.Serve(ctx, ln, ae.ServerConfig{AETitle: "DEST", AcceptedAbstractSyntaxes: ae.AllStorageSOPClasses,
			OnCStore: func(_ context.Context, req ae.StoreRequest) uint16 {
				mu.Lock()
				got[req.AffectedSOPInstanceUID] = dcm.String(req.Data, "PatientID")
				mu.Unlock()
				return status.Success
			}})
	}()
	h, p, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(p)
	return RemoteAE{Host: h, Port: port}, func() map[string]string {
		mu.Lock()
		defer mu.Unlock()
		cp := map[string]string{}
		for k, v := range got {
			cp[k] = v
		}
		return cp
	}
}

func cmove(t *testing.T, ctx context.Context, addr string, keys map[string]string) ae.RetrieveMatch {
	t.Helper()
	q := godicom.NewDataset()
	_ = q.SetString(godicom.MustTag("QueryRetrieveLevel"), "STUDY")
	for k, v := range keys {
		_ = q.SetString(godicom.MustTag(k), v)
	}
	scu, err := ae.Dial(ctx, ae.Config{AETitle: "VIEWER", PresentationContexts: []ae.PresentationContext{{
		AbstractSyntax: ae.StudyRootQueryRetrieveInformationModelMove, TransferSyntaxes: []string{dcm.ImplicitVRLittleEndian},
	}}}, addr, "RWS_TEST")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = scu.Release(ctx) }()
	res, err := scu.CMove(ctx, ae.MoveRequest{QueryModel: ae.StudyRootQueryRetrieveInformationModelMove, MoveDestination: "DEST", IdentifierData: q})
	if err != nil || len(res) == 0 {
		t.Fatalf("move: %v", err)
	}
	return res[len(res)-1]
}

func readArchived(t *testing.T, n *testNode, key string) *godicom.Dataset {
	t.Helper()
	rc, err := n.blobs.Get(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rc)
	_ = rc.Close()
	fd, err := godicom.ReadBytes(b, nil)
	if err != nil {
		t.Fatal(err)
	}
	return fd.Dataset
}

// Default policy: the conflicting instances are stored, held and hidden; the
// study keeps its first patient; the archived file of a resent SOP instance
// is not overwritten; audit, metrics and the conflict list record it. A
// series arriving under another study is held the same way (ANOM-009).
// Verifies: DCM-SRS-070, DCM-SRS-071, DCM-SRS-072, DCM-SRS-073, DCM-SRS-074
func TestConflictQuarantineHidden(t *testing.T) {
	logs := captureLog(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	dest, received := moveDest(t, ctx)
	n := startNode(t, ctx, nil, func(c *Config) { c.RemoteAEs = map[string]RemoteAE{"DEST": dest} })

	for i, sop := range []string{cSeries + ".1", cSeries + ".2"} {
		if st := storeAs(t, ctx, n.addr, patientImage("P1", "DOE^JANE", cStudy, cSeries, sop, i+1)); st != status.Success {
			t.Fatalf("store = %04X", st)
		}
	}
	// corrected resend of .1 under another patient, plus a new instance
	for i, sop := range []string{cSeries + ".1", cSeries + ".3"} {
		if st := storeAs(t, ctx, n.addr, patientImage("P2", "ROE^RICHARD", cStudy, cSeries, sop, i+1)); st != status.Success {
			t.Fatalf("conflict store = %04X, want success (quarantine)", st)
		}
	}
	// ANOM-009: the same series under another study
	if st := storeAs(t, ctx, n.addr, patientImage("P1", "DOE^JANE", cStudy+"9", cSeries, cSeries+".9", 9)); st != status.Success {
		t.Fatalf("series conflict store = %04X", st)
	}

	if rows := cfind(t, ctx, n.addr, "STUDY", map[string]string{"PatientID": "P1", "StudyInstanceUID": "", "NumberOfStudyRelatedInstances": ""}); len(rows) != 1 ||
		dcm.String(rows[0], "NumberOfStudyRelatedInstances") != "2" {
		t.Fatalf("P1 studies = %d", len(rows))
	}
	for _, pid := range []string{"P2", "B000_P2"} {
		if rows := cfind(t, ctx, n.addr, "STUDY", map[string]string{"PatientID": pid, "StudyInstanceUID": ""}); len(rows) != 0 {
			t.Fatalf("held patient %s visible", pid)
		}
	}
	if rows := cfind(t, ctx, n.addr, "STUDY", map[string]string{"StudyInstanceUID": cStudy + "9"}); len(rows) != 0 {
		t.Fatal("held series study visible")
	}
	if got := dcm.String(readArchived(t, n, blobKey(cStudy, cSeries, cSeries+".1")), "PatientID"); got != "P1" {
		t.Fatalf("archived file overwritten: PatientID %q", got)
	}
	if last := cmove(t, ctx, n.addr, map[string]string{"StudyInstanceUID": cStudy, "PatientID": "P2"}); last.SubOperations.Completed != 0 {
		t.Fatalf("held instances retrievable: %+v", last)
	}
	if len(received()) != 0 {
		t.Fatalf("received %v", received())
	}

	list, err := n.idx.ListConflicts(ctx, "", 0)
	if err != nil || len(list) != 2 {
		t.Fatalf("conflicts = %+v, %v", list, err)
	}
	kinds := map[index.ConflictKind]int{}
	for _, c := range list {
		kinds[c.Kind] = c.Instances
		if c.State != index.StateOpen || c.Coerced {
			t.Fatalf("conflict %+v", c)
		}
	}
	if kinds[index.ConflictPatientID] != 2 || kinds[index.ConflictSeriesStudy] != 1 {
		t.Fatalf("kinds = %v", kinds)
	}
	mfs := gatherMetrics(t, n.reg)
	if v := metricValue(t, mfs, "libre_dcm_patient_conflicts_total", map[string]string{"kind": "patient_id", "policy": "quarantine"}); v != 2 {
		t.Fatalf("conflicts_total = %v", v)
	}
	if v := metricValue(t, mfs, "libre_dcm_patient_conflicts_open", nil); v != 2 {
		t.Fatalf("open = %v", v)
	}
	if v := metricValue(t, mfs, "libre_dcm_cstore_total", map[string]string{"result": "conflict-open"}); v != 3 {
		t.Fatalf("cstore conflict-open = %v", v)
	}
	if !strings.Contains(logs.String(), "event="+auditConflictDetected) {
		t.Fatal("no conflict audit record")
	}
}

// Policy reject: the conflicting instance is refused with A778 and nothing
// is stored or recorded.
// Verifies: DCM-SRS-071
func TestConflictReject(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	n := startNode(t, ctx, nil, func(c *Config) { c.PatientConflictPolicy = PolicyReject })
	_ = storeAs(t, ctx, n.addr, patientImage("P1", "DOE^JANE", cStudy, cSeries, cSeries+".1", 1))
	if st := storeAs(t, ctx, n.addr, patientImage("P2", "ROE^RICHARD", cStudy, cSeries, cSeries+".2", 2)); st != StatusConflictingPatientID {
		t.Fatalf("status = %04X, want A778", st)
	}
	if list, _ := n.idx.ListConflicts(ctx, "all", 0); len(list) != 0 {
		t.Fatalf("recorded: %v", list)
	}
	if rows := cfind(t, ctx, n.addr, "IMAGE", map[string]string{"StudyInstanceUID": cStudy, "SOPInstanceUID": ""}); len(rows) != 1 {
		t.Fatalf("images = %d", len(rows))
	}
}

// Visibility flagged + coercion: the held instance is stored with Patient ID
// B000_P2 (Original Attributes Sequence, Warning B000), is found under that
// ID, and is retrieved only when the request names it; a retrieve by Study
// UID alone returns only the indexed patient's instances.
// Verifies: DCM-SRS-073, DCM-SRS-075
func TestConflictFlaggedCoerced(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	dest, received := moveDest(t, ctx)
	n := startNode(t, ctx, nil, func(c *Config) {
		c.RemoteAEs = map[string]RemoteAE{"DEST": dest}
		c.PatientConflictVisibility = VisibilityFlagged
		c.PatientConflictCoercePatientID = true
	})
	_ = storeAs(t, ctx, n.addr, patientImage("P1", "DOE^JANE", cStudy, cSeries, cSeries+".1", 1))
	if st := storeAs(t, ctx, n.addr, patientImage("P2", "ROE^RICHARD", cStudy, cSeries, cSeries+".2", 2)); st != StatusCoercion {
		t.Fatalf("status = %04X, want B000", st)
	}
	pats := cfind(t, ctx, n.addr, "STUDY", map[string]string{"PatientID": "", "StudyInstanceUID": cStudy, "NumberOfStudyRelatedInstances": ""})
	ids := map[string]bool{}
	for _, p := range pats {
		ids[dcm.String(p, "PatientID")] = true
	}
	if !ids["P1"] || !ids["B000_P2"] || len(ids) != 2 {
		t.Fatalf("patients = %v", ids)
	}
	for _, p := range pats {
		if dcm.String(p, "NumberOfStudyRelatedInstances") != "1" {
			t.Fatalf("instances per entry = %q", dcm.String(p, "NumberOfStudyRelatedInstances"))
		}
	}
	if last := cmove(t, ctx, n.addr, map[string]string{"StudyInstanceUID": cStudy}); last.SubOperations.Completed != 1 {
		t.Fatalf("move by study = %+v", last)
	}
	if got := received(); len(got) != 1 || got[cSeries+".1"] != "P1" {
		t.Fatalf("received %v", got)
	}
	if last := cmove(t, ctx, n.addr, map[string]string{"StudyInstanceUID": cStudy, "PatientID": "B000_P2"}); last.SubOperations.Completed != 1 {
		t.Fatalf("move by flagged patient = %+v", last)
	}
	if got := received(); got[cSeries+".2"] != "B000_P2" {
		t.Fatalf("received %v", got)
	}
	list, _ := n.idx.ListConflicts(ctx, "", 0)
	insts, _ := n.idx.ConflictInstances(ctx, list[0].ID)
	ds := readArchived(t, n, insts[0].Record.BlobKey)
	oas := itemsOf(ds, "OriginalAttributesSequence")
	if len(oas) != 1 || dcm.String(oas[0], "ReasonForTheAttributeModification") != "COERCE" ||
		dcm.String(itemsOf(oas[0], "ModifiedAttributesSequence")[0], "PatientID") != "P2" {
		t.Fatal("Original Attributes Sequence missing or wrong")
	}
}

func captureLog(t *testing.T) *syncBuf {
	buf := &syncBuf{}
	logSink = buf
	t.Cleanup(func() { logSink = nil })
	return buf
}

func itemsOf(ds *godicom.Dataset, kw string) []*godicom.Dataset {
	e, ok := ds.Get(godicom.MustTag(kw))
	if !ok {
		return nil
	}
	sq, _ := e.Value.(*godicom.Sequence)
	if sq == nil {
		return nil
	}
	return sq.Items()
}

// Policy accept_separate: visible even with hidden visibility, recorded as
// accepted, not counted as open.
// Verifies: DCM-SRS-071, DCM-SRS-073
func TestConflictAcceptSeparate(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	n := startNode(t, ctx, nil, func(c *Config) { c.PatientConflictPolicy = PolicyAcceptSeparate })
	_ = storeAs(t, ctx, n.addr, patientImage("P1", "DOE^JANE", cStudy, cSeries, cSeries+".1", 1))
	if st := storeAs(t, ctx, n.addr, patientImage("P2", "ROE^RICHARD", cStudy, cSeries, cSeries+".2", 2)); st != status.Success {
		t.Fatalf("status = %04X", st)
	}
	if rows := cfind(t, ctx, n.addr, "STUDY", map[string]string{"PatientID": "P2", "StudyInstanceUID": ""}); len(rows) != 1 {
		t.Fatalf("separate study not visible: %d", len(rows))
	}
	if v := metricValue(t, gatherMetrics(t, n.reg), "libre_dcm_patient_conflicts_open", nil); v != 0 {
		t.Fatalf("open = %v", v)
	}
	if list, _ := n.idx.ListConflicts(ctx, index.StateAccepted, 0); len(list) != 1 {
		t.Fatalf("accepted = %v", list)
	}
}

// Same Patient ID, another name: stored, first name kept, counted.
// Verifies: DCM-SRS-076
func TestDemographicsMismatchKeepsFirst(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	n := startNode(t, ctx, nil, nil)
	_ = storeAs(t, ctx, n.addr, patientImage("P1", "DOE^JANE", cStudy, cSeries, cSeries+".1", 1))
	if st := storeAs(t, ctx, n.addr, patientImage("P1", "DOE^JANET", cStudy, cSeries, cSeries+".2", 2)); st != status.Success {
		t.Fatalf("status = %04X", st)
	}
	rows := cfind(t, ctx, n.addr, "STUDY", map[string]string{"StudyInstanceUID": cStudy, "PatientName": ""})
	if len(rows) != 1 || dcm.String(rows[0], "PatientName") != "DOE^JANE" {
		t.Fatalf("rows = %v", rows)
	}
	if v := metricValue(t, gatherMetrics(t, n.reg), "libre_dcm_patient_demographics_mismatch_total", nil); v != 1 {
		t.Fatalf("mismatch = %v", v)
	}
}

func heldNode(t *testing.T, ctx context.Context, mod func(*Config)) (*testNode, string) {
	t.Helper()
	n := startNode(t, ctx, nil, mod)
	for i, sop := range []string{cSeries + ".1", cSeries + ".2"} {
		_ = storeAs(t, ctx, n.addr, patientImage("P1", "DOE^JANE", cStudy, cSeries, sop, i+1))
	}
	_ = storeAs(t, ctx, n.addr, patientImage("P2", "ROE^RICHARD", cStudy, cSeries, cSeries+".2", 2)) // corrected resend
	_ = storeAs(t, ctx, n.addr, patientImage("P2", "ROE^RICHARD", cStudy, cSeries, cSeries+".3", 3))
	list, err := n.idx.ListConflicts(ctx, "", 0)
	if err != nil || len(list) != 1 {
		t.Fatalf("conflicts = %v, %v", list, err)
	}
	return n, list[0].ID
}

// Resolve refuses requests without operator or reason and unknown actions,
// and PlanResolve describes the change without making it.
// Verifies: DCM-SRS-077
func TestResolveValidation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	n, id := heldNode(t, ctx, nil)
	if _, err := n.gw.Resolve(ctx, id, ActionCorrectIncoming, "", "typo"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("no operator: %v", err)
	}
	if _, err := n.gw.Resolve(ctx, id, ActionCorrectIncoming, "admin", " "); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("no reason: %v", err)
	}
	if _, err := n.gw.Resolve(ctx, id, "delete", "admin", "x"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("bad action: %v", err)
	}
	if _, err := n.gw.Resolve(ctx, "nope", ActionReject, "admin", "x"); !errors.Is(err, index.ErrNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
	p, err := n.gw.PlanResolve(ctx, id, ActionCorrectExisting)
	if err != nil || p.ExistingInstances != 2 || p.IncomingInstances != 2 || len(p.ExistingChanges) != 2 || len(p.Warnings) == 0 {
		t.Fatalf("plan = %+v, %v", p, err)
	}
	if got := dcm.String(readArchived(t, n, blobKey(cStudy, cSeries, cSeries+".1")), "PatientID"); got != "P1" {
		t.Fatal("plan changed a file")
	}
}

// correct_incoming: the held instances get the indexed patient (Original
// Attributes Sequence, reason CORRECT) and join the study; the conflict is
// closed and audited.
// Verifies: DCM-SRS-077, DCM-SRS-078
func TestResolveCorrectIncoming(t *testing.T) {
	logs := captureLog(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	n, id := heldNode(t, ctx, func(c *Config) { c.PatientConflictCoercePatientID = true })
	res, err := n.gw.Resolve(ctx, id, ActionCorrectIncoming, "admin", "technologist chose the wrong worklist entry")
	if err != nil || res.State != index.StateCorrected || res.Rewritten != 2 {
		t.Fatalf("res = %+v, %v", res, err)
	}
	rows := cfind(t, ctx, n.addr, "IMAGE", map[string]string{"StudyInstanceUID": cStudy, "PatientID": "", "SOPInstanceUID": ""})
	if len(rows) != 3 {
		t.Fatalf("images = %d", len(rows))
	}
	ds := readArchived(t, n, blobKey(cStudy, cSeries, cSeries+".3"))
	if dcm.String(ds, "PatientID") != "P1" || dcm.String(ds, "PatientName") != "DOE^JANE" {
		t.Fatalf("file patient = %q %q", dcm.String(ds, "PatientID"), dcm.String(ds, "PatientName"))
	}
	oas := itemsOf(ds, "OriginalAttributesSequence")
	if len(oas) != 2 || dcm.String(oas[1], "ReasonForTheAttributeModification") != "CORRECT" ||
		dcm.String(itemsOf(oas[1], "ModifiedAttributesSequence")[0], "PatientID") != "B000_P2" {
		t.Fatalf("history = %d items", len(oas))
	}
	c, _ := n.idx.GetConflict(ctx, id)
	if c.State != index.StateCorrected || c.ResolvedBy != "admin" || c.Instances != 0 {
		t.Fatalf("conflict = %+v", c)
	}
	if !strings.Contains(logs.String(), "event="+auditConflictResolved) || !strings.Contains(logs.String(), "operator=admin") {
		t.Fatal("no resolve audit record")
	}
	if _, err := n.gw.Resolve(ctx, id, ActionReject, "admin", "again"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("resolved twice: %v", err)
	}
}

// correct_existing: every archived instance of the study gets the incoming
// patient, the study row follows, and the held instances join (the corrected
// resend replaces the old file of the same SOP instance).
// Verifies: DCM-SRS-077, DCM-SRS-078
func TestResolveCorrectExisting(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	n, id := heldNode(t, ctx, nil)
	res, err := n.gw.Resolve(ctx, id, ActionCorrectExisting, "admin", "original registration was wrong")
	if err != nil || res.State != index.StateCorrected {
		t.Fatalf("res = %+v, %v", res, err)
	}
	if rows := cfind(t, ctx, n.addr, "STUDY", map[string]string{"PatientID": "P1", "StudyInstanceUID": ""}); len(rows) != 0 {
		t.Fatal("study still under P1")
	}
	rows := cfind(t, ctx, n.addr, "STUDY", map[string]string{"PatientID": "P2", "PatientName": "", "NumberOfStudyRelatedInstances": ""})
	if len(rows) != 1 || dcm.String(rows[0], "PatientName") != "ROE^RICHARD" || dcm.String(rows[0], "NumberOfStudyRelatedInstances") != "3" {
		t.Fatalf("rows = %v", rows)
	}
	for _, sop := range []string{".1", ".2", ".3"} {
		ds := readArchived(t, n, blobKey(cStudy, cSeries, cSeries+sop))
		if dcm.String(ds, "PatientID") != "P2" {
			t.Fatalf("%s patient = %q", sop, dcm.String(ds, "PatientID"))
		}
	}
	if len(itemsOf(readArchived(t, n, blobKey(cStudy, cSeries, cSeries+".1")), "OriginalAttributesSequence")) != 1 {
		t.Fatal("no correction history on the rewritten original")
	}
}

// reject: closed, kept, never visible even with flagged visibility.
// Verifies: DCM-SRS-079
func TestResolveReject(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	n, id := heldNode(t, ctx, func(c *Config) { c.PatientConflictVisibility = VisibilityFlagged })
	if rows := cfind(t, ctx, n.addr, "STUDY", map[string]string{"PatientID": "P2", "StudyInstanceUID": ""}); len(rows) != 1 {
		t.Fatal("flagged conflict not visible before reject")
	}
	if _, err := n.gw.Resolve(ctx, id, ActionReject, "admin", "duplicate from test modality"); err != nil {
		t.Fatal(err)
	}
	if rows := cfind(t, ctx, n.addr, "STUDY", map[string]string{"PatientID": "P2", "StudyInstanceUID": ""}); len(rows) != 0 {
		t.Fatal("rejected instances visible")
	}
	insts, _ := n.idx.ConflictInstances(ctx, id)
	if len(insts) != 2 {
		t.Fatalf("rejected instances not kept: %d", len(insts))
	}
}
