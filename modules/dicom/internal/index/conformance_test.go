package index

import (
	"context"
	"fmt"
	"testing"
)

// The conformance suite runs every query operation against SQLite and, when
// LIBRE_DCM_TEST_POSTGRES_DSN is set, PostgreSQL, so the two engines can't drift
// (BACKLOG.md "Metadata index"). Run both with `make test-postgres`.

func seed(t *testing.T, idx *SQL) {
	t.Helper()
	ctx := context.Background()
	type st struct {
		study, pid, name, date, time, acc string
		series                            map[string]string // series uid -> modality
	}
	data := []st{
		{"1.1", "P1", "DOE^JANE", "20260110", "081500", "A1", map[string]string{"1.1.1": "CT", "1.1.2": "SR"}},
		{"1.2", "P1", "DOE^JANE", "20260220", "143000", "A2", map[string]string{"1.2.1": "MR"}},
		{"1.3", "P2", "Ünal^Ayşe", "20251231", "235959", "A3", map[string]string{"1.3.1": "US"}},
		{"1.4", "P3", "SMITH_JR^BOB", "20260301", "", "A%4", map[string]string{"1.4.1": "CT"}},
	}
	for _, s := range data {
		n := 0
		for se, mod := range s.series {
			for i := range 2 {
				n++
				r := Record{PatientID: s.pid, PatientName: s.name, StudyUID: s.study, StudyDate: s.date, StudyTime: s.time,
					AccessionNumber: s.acc, SeriesUID: se, Modality: mod, SeriesNumber: fmt.Sprint(n),
					SOPInstanceUID: fmt.Sprintf("%s.%d", se, i), SOPClassUID: "1.2.840.10008.5.1.4.1.1.2",
					InstanceNumber: fmt.Sprint(i + 1), TransferSyntaxUID: "1.2.840.10008.1.2.1", BlobKey: se}
				if err := idx.Upsert(ctx, r); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
}

func uids(rows []Row, key string) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r[key]
	}
	return out
}

func TestConformance(t *testing.T) {
	cases := []struct {
		name    string
		level   Level
		filters []Filter
		key     string
		want    []string // in result order
	}{
		{"all studies newest first", Study, nil, "StudyInstanceUID", []string{"1.4", "1.2", "1.1", "1.3"}},
		{"patient id", Study, []Filter{{Column: "st.patient_id", Op: OpEq, Values: []string{"P1"}}}, "StudyInstanceUID", []string{"1.2", "1.1"}},
		{"date range", Study, []Filter{{Column: "st.study_date", Op: OpRange, Values: []string{"20260101", "20260228"}}}, "StudyInstanceUID", []string{"1.2", "1.1"}},
		{"open date range", Study, []Filter{{Column: "st.study_date", Op: OpRange, Values: []string{"", "20260101"}}}, "StudyInstanceUID", []string{"1.3"}},
		{"time range", Study, []Filter{{Column: "st.study_time", Op: OpRange, Values: []string{"080000", "125959.999999"}}}, "StudyInstanceUID", []string{"1.1"}},
		{"uid list", Study, []Filter{{Column: "st.study_uid", Op: OpIn, Values: []string{"1.3", "1.1", "9.9"}}}, "StudyInstanceUID", []string{"1.1", "1.3"}},
		{"wildcard", Study, []Filter{{Column: "st.patient_name", Op: OpLike, Values: []string{"DOE%"}}}, "StudyInstanceUID", []string{"1.2", "1.1"}},
		{"like is case sensitive", Study, []Filter{{Column: "st.patient_name", Op: OpLike, Values: []string{"doe%"}}}, "StudyInstanceUID", []string{}},
		{"like escapes _ and %", Study, []Filter{{Column: "st.accession_number", Op: OpLike, Values: []string{`A\%%`}}}, "StudyInstanceUID", []string{"1.4"}},
		{"literal underscore", Study, []Filter{{Column: "st.patient_name", Op: OpLike, Values: []string{`SMITH\_JR%`}}}, "StudyInstanceUID", []string{"1.4"}},
		{"folded unicode eq", Study, []Filter{{Column: "st.patient_name_fold", Op: OpEqFold, Values: []string{"ÜNAL^AYŞE"}}}, "StudyInstanceUID", []string{"1.3"}},
		{"folded like", Study, []Filter{{Column: "st.patient_name_fold", Op: OpLikeFold, Values: []string{"doe%"}}}, "StudyInstanceUID", []string{"1.2", "1.1"}},
		{"modality in study", Study, []Filter{{Op: OpStudyHasModality, Values: []string{"SR", "US"}}}, "StudyInstanceUID", []string{"1.1", "1.3"}},
		{"patients", Patient, nil, "PatientID", []string{"P1", "P2", "P3"}},
		{"series of study", Series, []Filter{{Column: "st.study_uid", Op: OpEq, Values: []string{"1.1"}}}, "Modality", nil}, // order checked below
		{"images of series", Image, []Filter{{Column: "se.series_uid", Op: OpEq, Values: []string{"1.2.1"}}}, "SOPInstanceUID", []string{"1.2.1.0", "1.2.1.1"}},
	}
	for engine, idx := range openTestIndexes(t) {
		seed(t, idx)
		t.Run(engine, func(t *testing.T) {
			ctx := context.Background()
			for _, c := range cases {
				rows, err := idx.Find(ctx, c.level, c.filters, 0)
				if err != nil {
					t.Fatalf("%s: %v", c.name, err)
				}
				got := uids(rows, c.key)
				if c.want == nil {
					if len(got) != 2 {
						t.Errorf("%s: %v", c.name, got)
					}
					continue
				}
				if len(got) == 0 && len(c.want) == 0 {
					continue
				}
				if fmt.Sprint(got) != fmt.Sprint(c.want) {
					t.Errorf("%s: got %v want %v", c.name, got, c.want)
				}
			}

			p, _ := idx.Find(ctx, Patient, []Filter{{Column: "st.patient_id", Op: OpEq, Values: []string{"P1"}}}, 0)
			if len(p) != 1 || p[0]["NumberOfPatientRelatedStudies"] != "2" {
				t.Errorf("patient counts: %v", p)
			}
			s, _ := idx.Find(ctx, Study, []Filter{{Column: "st.study_uid", Op: OpEq, Values: []string{"1.1"}}}, 0)
			if len(s) != 1 || s[0]["NumberOfStudyRelatedSeries"] != "2" || s[0]["NumberOfStudyRelatedInstances"] != "4" {
				t.Errorf("study counts: %v", s)
			}
			if lim, _ := idx.Find(ctx, Study, nil, 2); len(lim) != 2 {
				t.Errorf("limit: %d rows", len(lim))
			}
			mods, err := idx.ModalitiesForStudies(ctx, []string{"1.1", "1.3"})
			if err != nil || fmt.Sprint(mods["1.1"]) != "[CT SR]" || fmt.Sprint(mods["1.3"]) != "[US]" {
				t.Errorf("modalities: %v %v", mods, err)
			}
			// Re-sending an instance updates, not duplicates.
			r := Record{PatientID: "P1", PatientName: "DOE^JANE", StudyUID: "1.1", SeriesUID: "1.1.1", Modality: "CT",
				SOPInstanceUID: "1.1.1.0", SOPClassUID: "x", TransferSyntaxUID: "y", BlobKey: "moved"}
			if err := idx.Upsert(ctx, r); err != nil {
				t.Fatal(err)
			}
			imgs, _ := idx.Find(ctx, Image, []Filter{{Column: "i.sop_instance_uid", Op: OpEq, Values: []string{"1.1.1.0"}}}, 0)
			if len(imgs) != 1 || imgs[0]["_BlobKey"] != "moved" {
				t.Errorf("idempotent upsert: %v", imgs)
			}
		})
	}
}
