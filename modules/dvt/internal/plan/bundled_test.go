package plan

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func bundledPlan(t *testing.T, file string) *Plan {
	t.Helper()
	_, here, _, _ := runtime.Caller(0)
	dir := filepath.Join(filepath.Dir(here), "..", "..", "plans")
	data, err := os.ReadFile(filepath.Join(dir, file))
	if err != nil {
		t.Fatal(err)
	}
	p, err := Parse(data, dir)
	if err != nil {
		t.Fatalf("%s rejected: %v", file, err)
	}
	return p
}

func stepsByID(p *Plan) map[string]Step {
	m := map[string]Step{}
	for _, s := range p.Steps {
		m[s.ID] = s
	}
	return m
}

const (
	verification = "1.2.840.10008.1.1"
	ctStorage    = "1.2.840.10008.5.1.4.1.1.2"
	explicitLE   = "1.2.840.10008.1.2.1"
)

// Verifies: DVT-SRS-156
func TestBundledExamplePlans(t *testing.T) {
	// LibreDCM smoke: association (Verification and CT Image Storage
	// accepted with Explicit VR Little Endian, an unsupported abstract
	// syntax rejected) and TLS (echo and store over TLS; plaintext and
	// TLS 1.1 refused).
	smoke := bundledPlan(t, "libredcm-smoke-tls.yaml")
	if smoke.Name != "LibreDCM smoke (DIMSE + TLS)" {
		t.Errorf("smoke plan name %q", smoke.Name)
	}
	s := stepsByID(smoke)
	neg := s["negotiate"]
	if neg.Type != Associate || len(neg.Contexts) != 3 {
		t.Fatalf("negotiate step: %+v", neg)
	}
	for i, as := range []string{verification, ctStorage} {
		c := neg.Contexts[i]
		if c.AbstractSyntax != as || c.Expect != "accepted" || c.ExpectTransferSyntax != explicitLE || strings.Join(c.TransferSyntaxes, ",") != explicitLE {
			t.Errorf("context %d: %+v", i+1, c)
		}
	}
	if c := neg.Contexts[2]; c.Expect != "rejected" || c.AbstractSyntax == verification || c.AbstractSyntax == ctStorage {
		t.Errorf("unsupported context: %+v", c)
	}
	if s["echo-over-tls"].Type != Echo || s["store-over-tls"].Type != Store || len(s["store-over-tls"].Files) == 0 {
		t.Errorf("echo/store steps: %+v %+v", s["echo-over-tls"], s["store-over-tls"])
	}
	if p := s["plaintext-refused"]; p.Type != TLSReject || p.Mode != "plaintext" {
		t.Errorf("plaintext step: %+v", p)
	}
	if p := s["tls11-refused"]; p.Type != TLSReject || p.Mode != "tls_version" || p.TLSVersion != "1.1" {
		t.Errorf("TLS 1.1 step: %+v", p)
	}
	if err := smoke.CheckTarget("libredcm-tls-lab", ProtocolDIMSE, true); err != nil {
		t.Errorf("smoke plan does not fit a DIMSE TLS target: %v", err)
	}
	if smoke.CheckTarget("x", ProtocolDIMSE, false) == nil || smoke.CheckTarget("x", ProtocolDICOMweb, true) == nil {
		t.Error("smoke plan accepted a target without TLS or of the other protocol")
	}

	// Generic DICOM TLS audit: verified TLS, echo, plaintext/1.0/1.1
	// refused; nothing is stored, so no step is lab-only.
	dt := bundledPlan(t, "generic-dicom-tls-audit.yaml")
	if dt.Name != "Generic DICOM TLS audit" {
		t.Errorf("DICOM TLS audit name %q", dt.Name)
	}
	modes := map[string]bool{}
	for _, st := range dt.Steps {
		if LabOnly(st.Type) {
			t.Errorf("DICOM TLS audit step %s (%s) needs a lab target; the audit must not change the target", st.ID, st.Type)
		}
		if st.Type == TLSReject {
			modes[st.Mode+st.TLSVersion] = true
		}
	}
	if !modes["plaintext"] || !modes["tls_version1.0"] || !modes["tls_version1.1"] || stepsByID(dt)["echo-over-tls"].Type != Echo {
		t.Errorf("DICOM TLS audit checks: %v", modes)
	}
	if err := dt.CheckTarget("dicom-tls-lab", ProtocolDIMSE, true); err != nil || dt.CheckTarget("x", ProtocolDIMSE, false) == nil {
		t.Errorf("DICOM TLS audit target check: %v", err)
	}

	// Generic DICOMweb audit: TLS refusals, STOW, QIDO, WADO with pixels.
	dw := bundledPlan(t, "generic-dicomweb-audit.yaml")
	if dw.Name != "Generic DICOMweb audit" {
		t.Errorf("DICOMweb audit name %q", dw.Name)
	}
	types := map[string]int{}
	for _, st := range dw.Steps {
		types[st.Type]++
	}
	if types[TLSReject] != 2 || types[Stow] != 1 || types[Qido] < 3 || types[Wado] != 1 {
		t.Errorf("DICOMweb audit step types: %v", types)
	}
	w := stepsByID(dw)["wado-and-compare-pixels"]
	if !w.FromStore || !w.ComparePixels || !stepsByID(dw)["stow-samples"].VerifyRetrieve {
		t.Errorf("DICOMweb audit does not compare what it stored: %+v", w)
	}
	if err := dw.CheckTarget("dicomweb-lab", ProtocolDICOMweb, true); err != nil || dw.CheckTarget("x", ProtocolDIMSE, true) == nil || dw.CheckTarget("x", ProtocolDICOMweb, false) == nil {
		t.Errorf("DICOMweb audit target check: %v", err)
	}
	// A client's system may hold real data: the audit keeps no body
	// excerpts in its evidence.
	if dw.Limits.MaxBodyExcerptBytes != 0 {
		t.Errorf("DICOMweb audit keeps %d bytes of body excerpts, want 0", dw.Limits.MaxBodyExcerptBytes)
	}
}
