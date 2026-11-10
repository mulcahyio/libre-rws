package index

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
)

func crec(pid, study, series, sop string) Record {
	return Record{PatientID: pid, PatientName: "NAME^" + pid, StudyUID: study, SeriesUID: series,
		SOPInstanceUID: sop, SOPClassUID: "1.2.840.10008.5.1.4.1.1.2", Modality: "CT",
		TransferSyntaxUID: "1.2.840.10008.1.2.1", BlobKey: sop}
}

func findOne(t *testing.T, idx *SQL, level Level, col, val string, opts FindOptions) []Row {
	t.Helper()
	rows, err := idx.FindWith(context.Background(), level, []Filter{{Column: col, Op: OpEq, Values: []string{val}}}, 0, opts)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

// The ANOM-002 probe: instances of one study arriving with another Patient
// ID are refused by Upsert, and the study stays with its first patient.
// Verifies: DCM-SRS-070
func TestUpsertNeverReassignsStudy(t *testing.T) {
	for name, idx := range openTestIndexes(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			if err := idx.Upsert(ctx, crec("P1", "S1", "SE1", "I1")); err != nil {
				t.Fatal(err)
			}
			err := idx.Upsert(ctx, crec("P2", "S1", "SE1", "I2"))
			var ce *ConflictError
			if !errors.As(err, &ce) || ce.Conflict.Kind != ConflictPatientID || ce.Conflict.ExistingPatientID != "P1" {
				t.Fatalf("err = %v", err)
			}
			if rows := findOne(t, idx, Study, "st.patient_id", "P1", FindOptions{}); len(rows) != 1 || rows[0]["NumberOfStudyRelatedInstances"] != "1" {
				t.Fatalf("P1 studies = %v", rows)
			}
			if rows := findOne(t, idx, Study, "st.patient_id", "P2", FindOptions{}); len(rows) != 0 {
				t.Fatalf("P2 got a study: %v", rows)
			}
			// A resend of the same instance for the same patient still replaces it.
			r := crec("P1", "S1", "SE1", "I1")
			r.BlobKey = "I1-resent"
			if err := idx.Upsert(ctx, r); err != nil {
				t.Fatal(err)
			}
			if rows := findOne(t, idx, Image, "i.sop_instance_uid", "I1", FindOptions{}); rows[0]["_BlobKey"] != "I1-resent" {
				t.Fatalf("resend not applied: %v", rows)
			}
		})
	}
}

// The ANOM-009 probe: a series arriving under another study, or an instance
// under another series, is refused and nothing moves.
// Verifies: DCM-SRS-070
func TestUpsertNeverMovesSeriesOrInstance(t *testing.T) {
	for name, idx := range openTestIndexes(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			if err := idx.Upsert(ctx, crec("P1", "S1", "SE1", "I1")); err != nil {
				t.Fatal(err)
			}
			err := idx.Upsert(ctx, crec("P2", "S2", "SE1", "I2"))
			var ce *ConflictError
			if !errors.As(err, &ce) || ce.Conflict.Kind != ConflictSeriesStudy || ce.Conflict.ExistingStudyUID != "S1" || ce.Conflict.ExistingPatientID != "P1" {
				t.Fatalf("series: err = %v", err)
			}
			err = idx.Upsert(ctx, crec("P1", "S1", "SE9", "I1"))
			if !errors.As(err, &ce) || ce.Conflict.Kind != ConflictInstanceRelation || ce.Conflict.ExistingSeries != "SE1" {
				t.Fatalf("instance: err = %v", err)
			}
			rows := findOne(t, idx, Series, "se.series_uid", "SE1", FindOptions{})
			if len(rows) != 1 || rows[0]["StudyInstanceUID"] != "S1" || rows[0]["PatientID"] != "P1" {
				t.Fatalf("series moved: %v", rows)
			}
			if rows := findOne(t, idx, Study, "st.study_uid", "S2", FindOptions{}); len(rows) != 0 {
				t.Fatalf("rolled-back study S2 exists: %v", rows)
			}
		})
	}
}

// Same Patient ID with another name keeps the first registration.
// Verifies: DCM-SRS-076
func TestFirstDemographicsKept(t *testing.T) {
	for name, idx := range openTestIndexes(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			_ = idx.Upsert(ctx, crec("P1", "S1", "SE1", "I1"))
			r := crec("P1", "S1", "SE1", "I2")
			r.PatientName, r.StudyDescription = "OTHER^NAME", "newer description"
			chk, err := idx.CheckConflict(ctx, r)
			if err != nil || chk.Conflict != nil || !chk.DemographicsMismatch || chk.Existing.PatientName != "NAME^P1" {
				t.Fatalf("check = %+v, %v", chk, err)
			}
			if err := idx.Upsert(ctx, r); err != nil {
				t.Fatal(err)
			}
			rows := findOne(t, idx, Study, "st.study_uid", "S1", FindOptions{})
			if rows[0]["PatientName"] != "NAME^P1" || rows[0]["StudyDescription"] != "newer description" {
				t.Fatalf("row = %v", rows[0])
			}
		})
	}
}

// Nodes storing different patients for one new study at the same time: one
// wins, every other gets a ConflictError, nothing is reassigned.
// Verifies: DCM-SRS-070
func TestConcurrentFirstRegistration(t *testing.T) {
	for name, idx := range openTestIndexes(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			var wg sync.WaitGroup
			var mu sync.Mutex
			ok, conflicts := map[string]bool{}, 0
			for i := range 8 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					pid := fmt.Sprintf("P%d", i)
					err := idx.Upsert(ctx, crec(pid, "SC", "SEC", fmt.Sprintf("IC%d", i)))
					mu.Lock()
					defer mu.Unlock()
					var ce *ConflictError
					switch {
					case err == nil:
						ok[pid] = true
					case errors.As(err, &ce):
						conflicts++
					default:
						t.Error(err)
					}
				}()
			}
			wg.Wait()
			if len(ok) != 1 || conflicts != 7 {
				t.Fatalf("winners=%v conflicts=%d", ok, conflicts)
			}
			for pid := range ok {
				if rows := findOne(t, idx, Image, "st.study_uid", "SC", FindOptions{}); len(rows) != 1 || rows[0]["PatientID"] != pid {
					t.Fatalf("rows = %v", rows)
				}
			}
		})
	}
}

// Recorded conflicts group per study and incoming patient, are listed, can
// be resolved, and appear in queries only when asked for, as a separate
// patient/study entry that never mixes with the indexed one.
// Verifies: DCM-SRS-072, DCM-SRS-073, DCM-SRS-079
func TestConflictRecordAndVisibility(t *testing.T) {
	for name, idx := range openTestIndexes(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			_ = idx.Upsert(ctx, crec("P1", "S1", "SE1", "I1"))
			_ = idx.Upsert(ctx, crec("P1", "S1", "SE1", "I2"))
			var id string
			for _, sop := range []string{"I1", "I3"} { // I1: same SOP UID resent with another patient
				r := crec("P2", "S1", "SE1", sop)
				chk, err := idx.CheckConflict(ctx, r)
				if err != nil || chk.Conflict == nil {
					t.Fatalf("check %s: %+v %v", sop, chk, err)
				}
				r.PatientID = "B000_P2"
				r.BlobKey = "conflicts/" + sop
				got, err := idx.RecordConflict(ctx, *chk.Conflict, r, StateOpen, true)
				if err != nil {
					t.Fatal(err)
				}
				if id != "" && got != id {
					t.Fatalf("not grouped: %s vs %s", got, id)
				}
				id = got
			}
			if n, _ := idx.CountOpenConflicts(ctx); n != 1 {
				t.Fatalf("open = %d", n)
			}
			list, err := idx.ListConflicts(ctx, "", 0)
			if err != nil || len(list) != 1 || list[0].Instances != 2 || list[0].DisplayPatientID != "B000_P2" ||
				list[0].IncomingPatientID != "P2" || !list[0].Coerced {
				t.Fatalf("list = %+v, %v", list, err)
			}

			hidden := FindOptions{Accepted: true}
			flagged := FindOptions{Accepted: true, Open: true}
			if rows := findOne(t, idx, Patient, "st.patient_id", "B000_P2", hidden); len(rows) != 0 {
				t.Fatalf("hidden conflict visible: %v", rows)
			}
			rows := findOne(t, idx, Study, "st.study_uid", "S1", flagged)
			if len(rows) != 2 {
				t.Fatalf("flagged study rows = %v", rows)
			}
			counts := map[string]string{}
			for _, r := range rows {
				counts[r["PatientID"]] = r["NumberOfStudyRelatedInstances"] + "/" + r["NumberOfStudyRelatedSeries"]
			}
			if counts["P1"] != "2/1" || counts["B000_P2"] != "2/1" {
				t.Fatalf("counts = %v", counts)
			}
			// Image level by Study UID + Patient ID returns only that patient's instances.
			imgs, err := idx.FindWith(ctx, Image, []Filter{
				{Column: "st.study_uid", Op: OpEq, Values: []string{"S1"}},
				{Column: "st.patient_id", Op: OpEq, Values: []string{"B000_P2"}}}, 0, flagged)
			if err != nil || len(imgs) != 2 || imgs[0]["_BlobKey"] != "conflicts/I1" || imgs[0]["_ConflictID"] != id {
				t.Fatalf("conflict images = %v, %v", imgs, err)
			}
			imgs, _ = idx.FindWith(ctx, Image, []Filter{
				{Column: "st.study_uid", Op: OpEq, Values: []string{"S1"}},
				{Column: "st.patient_id", Op: OpEq, Values: []string{"P1"}}}, 0, flagged)
			if len(imgs) != 2 || imgs[0]["_BlobKey"] != "I1" {
				t.Fatalf("indexed images = %v", imgs)
			}
			mods, _ := idx.FindWith(ctx, Study, []Filter{{Column: "", Op: OpStudyHasModality, Values: []string{"CT"}}}, 0, flagged)
			if len(mods) != 2 {
				t.Fatalf("modality filter = %v", mods)
			}

			if err := idx.ResolveConflict(ctx, id, StateRejected, "admin", "reject", "wrong patient"); err != nil {
				t.Fatal(err)
			}
			if err := idx.ResolveConflict(ctx, id, StateRejected, "admin", "reject", "again"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("second resolve = %v", err)
			}
			if rows := findOne(t, idx, Study, "st.study_uid", "S1", flagged); len(rows) != 1 {
				t.Fatalf("rejected conflict visible: %v", rows)
			}
			c, err := idx.GetConflict(ctx, id)
			if err != nil || c.State != StateRejected || c.ResolvedBy != "admin" || c.ResolvedAt == nil {
				t.Fatalf("resolved = %+v, %v", c, err)
			}
			if list, _ := idx.ListConflicts(ctx, "resolved", 0); len(list) != 1 {
				t.Fatalf("resolved list = %v", list)
			}
			// A new conflict for the same group after resolution is a new conflict.
			r := crec("P2", "S1", "SE1", "I4")
			chk, _ := idx.CheckConflict(ctx, r)
			id2, err := idx.RecordConflict(ctx, *chk.Conflict, r, StateAccepted, false)
			if err != nil || id2 == id {
				t.Fatalf("new conflict = %s, %v", id2, err)
			}
			if rows := findOne(t, idx, Patient, "st.patient_id", "P2", hidden); len(rows) != 1 {
				t.Fatalf("accepted conflict not visible: %v", rows)
			}
		})
	}
}
