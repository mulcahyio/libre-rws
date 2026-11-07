package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/godicom/uid"
	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/status"

	"github.com/mulcahyio/libre-rws/modules/dicom/internal/dcm"
	"github.com/mulcahyio/libre-rws/modules/dicom/internal/index"
)

func TestTimeBounds(t *testing.T) {
	cases := []struct{ in, lo, hi string }{
		{"08", "080000", "085959.999999"},
		{"0830", "083000", "083059.999999"},
		{"083015", "083015", "083015.999999"},
		{"083015.5", "083015.5", "083015.599999"},
		{"", "", ""},
	}
	for _, c := range cases {
		if got := timeLower(c.in); got != c.lo {
			t.Errorf("timeLower(%q) = %q, want %q", c.in, got, c.lo)
		}
		if got := timeUpper(c.in); got != c.hi {
			t.Errorf("timeUpper(%q) = %q, want %q", c.in, got, c.hi)
		}
	}
}

func TestBuildFilterKinds(t *testing.T) {
	on := queryOptions{caseInsensitivePN: true}
	f, _ := buildFilter(queryKeys["PatientName"], []string{"müller*"}, on)
	if f.Op != index.OpLikeFold || f.Column != "st.patient_name_fold" || f.Values[0] != "müller%" {
		t.Errorf("case-insensitive PN filter = %+v", f)
	}
	f, _ = buildFilter(queryKeys["PatientName"], []string{"DOE^JANE"}, queryOptions{})
	if f.Op != index.OpEq || f.Column != "st.patient_name" {
		t.Errorf("case-sensitive PN filter = %+v", f)
	}
	f, _ = buildFilter(queryKeys["StudyTime"], []string{"08:00-12"}, on)
	if f.Op != index.OpRange || f.Values[0] != "080000" || f.Values[1] != "125959.999999" {
		t.Errorf("time range filter = %+v", f)
	}
}

func TestResponseCharset(t *testing.T) {
	if got := responseCharset(nil, []string{"DOE^JANE"}); got != nil {
		t.Errorf("ASCII result changed charset: %v", got)
	}
	if got := responseCharset([]string{"ISO_IR 100"}, []string{"Müller^Hans"}); len(got) != 1 || got[0] != "ISO_IR 192" {
		t.Errorf("non-ASCII with Latin-1 request: %v", got)
	}
	if got := responseCharset(nil, []string{"Müller^Hans"}); len(got) != 1 || got[0] != "ISO_IR 192" {
		t.Errorf("non-ASCII without request charset: %v", got)
	}
	if got := responseCharset([]string{"ISO_IR 100"}, []string{"山田^太郎"}); len(got) != 1 || got[0] != "ISO_IR 192" {
		t.Errorf("value Latin-1 can't carry: %v", got)
	}
}

// TestFindRefinementsEndToEnd stores a Latin-1 encoded study over the network
// and checks case-insensitive names, time ranges and the response charset.
func TestFindRefinementsEndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	addr, _, _ := startGateway(t, ctx, nil)

	study, series := "1.2.826.0.1.3680043.10.999.20", "1.2.826.0.1.3680043.10.999.20.1"
	ds := ctImage(study, series, series+".1", 1)
	_ = ds.SetString(godicom.MustTag("SpecificCharacterSet"), "ISO_IR 100")
	_ = ds.SetString(godicom.MustTag("PatientName"), "Müller^Hans")
	_ = ds.SetString(godicom.MustTag("StudyTime"), "083015")
	scu, err := dialAs(ctx, addr, "MODALITY")
	if err != nil {
		t.Fatal(err)
	}
	res, err := scu.CStore(ctx, ae.StoreRequest{AffectedSOPClassUID: string(uid.CTImageStorage), AffectedSOPInstanceUID: series + ".1", Data: ds})
	if err != nil || res.Status != status.Success {
		t.Fatalf("store: %v %v", err, res)
	}
	_ = scu.Release(ctx)

	find := func(charset string, keys map[string]string) []*godicom.Dataset {
		t.Helper()
		q := godicom.NewDataset()
		if charset != "" {
			_ = q.SetString(godicom.MustTag("SpecificCharacterSet"), charset)
		}
		_ = q.SetString(godicom.MustTag("QueryRetrieveLevel"), "STUDY")
		_ = q.SetString(godicom.MustTag("StudyInstanceUID"), "")
		_ = q.SetString(godicom.MustTag("PatientName"), "")
		for k, v := range keys {
			_ = q.SetString(godicom.MustTag(k), v)
		}
		a, err := ae.Dial(ctx, ae.Config{AETitle: "VIEWER", PresentationContexts: []ae.PresentationContext{{
			AbstractSyntax: ae.StudyRootQueryRetrieveInformationModelFind, TransferSyntaxes: []string{dcm.ImplicitVRLittleEndian},
		}}}, addr, "RWS_TEST")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = a.Release(ctx) }()
		ms, err := a.CFind(ctx, ae.FindRequest{QueryModel: ae.StudyRootQueryRetrieveInformationModelFind, IdentifierData: q})
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

	got := find("ISO_IR 100", map[string]string{"PatientName": "müller*"})
	if len(got) != 1 {
		t.Fatalf("case-insensitive name: %d matches", len(got))
	}
	if n := dcm.String(got[0], "PatientName"); n != "Müller^Hans" {
		t.Errorf("name round trip = %q", n)
	}
	if cs := dcm.String(got[0], "SpecificCharacterSet"); cs != "ISO_IR 192" {
		t.Errorf("response charset = %q, want ISO_IR 192", cs)
	}
	if got := find("", map[string]string{"PatientName": "MÜLLER^HANS"}); len(got) != 1 {
		t.Errorf("exact name, other case: %d matches", len(got))
	} else if n := dcm.String(got[0], "PatientName"); n != "Müller^Hans" {
		t.Errorf("name without request charset = %q", n)
	}
	for q, want := range map[string]int{"08-09": 1, "0830": 1, "0831-": 0, "-0830": 1, "0900-1700": 0, "08:30-08:31": 1} {
		if got := find("", map[string]string{"StudyTime": q}); len(got) != want {
			t.Errorf("StudyTime %q: %d matches, want %d", q, len(got), want)
		}
	}
}
