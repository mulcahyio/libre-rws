//go:build engines

// CLI/web parity with the real Python worker against LibreDVT's Go test
// peers (internal/fakepeer): a DIMSE TLS peer and a DICOMweb TLS server.
// Runs in the LibreDVT test image only.

package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mulcahyio/libre-rws/modules/dvt/internal/fakepeer"
	"github.com/mulcahyio/libre-rws/modules/dvt/internal/result"
)

// Synthetic test-data UIDs (docs/qms/uid-registry.md, test arc).
const (
	parityStudy  = "1.3.6.1.4.1.67686.9.3.146.1"
	paritySeries = "1.3.6.1.4.1.67686.9.3.146.1.1"
	paritySOP    = "1.3.6.1.4.1.67686.9.3.146.1.1.1"
)

// Verifies: DVT-SRS-146, DVT-SRS-145
func TestRealCLIAndWebGiveTheSameResult(t *testing.T) {
	assets := os.Getenv("LIBRE_DVT_TEST_ASSETS")
	if assets == "" {
		t.Skip("LIBRE_DVT_TEST_ASSETS not set")
	}
	dir := t.TempDir()
	secrets := filepath.Join(dir, "secrets")
	if err := os.MkdirAll(secrets, 0o750); err != nil {
		t.Fatal(err)
	}
	pki, err := fakepeer.NewPKI(secrets)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := pki.Issue("peer", fakepeer.LeafOpts{Issuer: pki.Root, CN: "peer", IPs: []net.IP{net.ParseIP("127.0.0.1")}})
	if err != nil {
		t.Fatal(err)
	}
	tlsCfg := &tls.Config{Certificates: []tls.Certificate{leaf.TLS}, MinVersion: tls.VersionTLS12}
	tok := filepath.Join(secrets, "token")
	if err := os.WriteFile(tok, []byte(synthToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{"LIBRE_DVT_WORKER": filepath.Join(assets, "worker", "libredvt_worker.py"), "LIBRE_DVT_PYTHON": "python3",
		"LIBRE_DVT_STANDARD_PATH": filepath.Join(assets, "standard"), "LIBRE_DVT_DATA": filepath.Join(dir, "data"), "LIBRE_DVT_SECRETS_DIR": secrets} {
		t.Setenv(k, v)
	}
	cfg, err := loadConfig(flag.NewFlagSet("parity", flag.ContinueOnError), nil)
	if err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}

	t.Run("dimse-tls", func(t *testing.T) {
		peer, err := fakepeer.Start(tlsCfg, fakepeer.Script{ImplUID: "1.3.6.1.4.1.67686.9.3.146.9", ImplVersion: "PARITY_PEER",
			Contexts: func(c fakepeer.RQContext) fakepeer.ContextReply {
				if c.AbstractSyntax == "1.2.840.10008.5.1.4.31" {
					return fakepeer.ContextReply{Result: 3, TransferSyntax: c.TransferSyntaxes[0]}
				}
				return fakepeer.ContextReply{TransferSyntax: c.TransferSyntaxes[0]}
			}})
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		port := strconv.Itoa(peer.Port)
		targets := write("dimse-targets.yaml", "targets:\n  - {name: pacs, class: lab, protocol: dimse, host: 127.0.0.1, port: "+port+", called_aet: FAKEPEER, tls: {ca_file: "+pki.Root.File+"}}\n")
		form := url.Values{"protocol": {"dimse"}, "name": {"pacs"}, "class": {"lab"}, "host": {"127.0.0.1"}, "port": {port},
			"called_aet": {"FAKEPEER"}, "calling_aet": {"LIBREDVT"}, "tls": {"on"}, "tls_ca_file": {pki.Root.File}}
		// The negotiation and refusal checks of the bundled smoke plan.
		planFile := write("dimse.yaml", `name: parity dimse
target: pacs
timeout: 20s
steps:
  - id: negotiate
    type: associate
    contexts:
      - {abstract_syntax: "1.2.840.10008.1.1", transfer_syntaxes: ["1.2.840.10008.1.2.1"], expect: accepted, expect_transfer_syntax: "1.2.840.10008.1.2.1"}
      - {abstract_syntax: "1.2.840.10008.5.1.4.1.1.2", transfer_syntaxes: ["1.2.840.10008.1.2.1"], expect: accepted, expect_transfer_syntax: "1.2.840.10008.1.2.1"}
      - {abstract_syntax: "1.2.840.10008.5.1.4.31", transfer_syntaxes: ["1.2.840.10008.1.2.1"], expect: rejected}
    expect: {impl_class_uid: "1.3.6.1.4.1.67686.9.3.146.9", impl_version: PARITY_PEER}
  - {id: plaintext-refused, type: tls_reject, mode: plaintext}
  - {id: tls11-refused, type: tls_reject, mode: tls_version, tls_version: "1.1"}
`)
		cli, _ := checkParity(t, cfg, planFile, targets, form)
		mustPass(t, cli, 3)
	})

	t.Run("dicomweb-tls-bearer", func(t *testing.T) {
		w, err := fakepeer.StartWeb(tlsCfg, fakepeer.WebScript{Instances: []fakepeer.WebInstance{{
			Study: parityStudy, Series: paritySeries, SOP: paritySOP, Class: "1.2.840.10008.5.1.4.1.1.2", TS: "1.2.840.10008.1.2.1",
			Attrs: map[string]string{"00100020": "PARITY-1", "00080060": "CT", "00080020": "20260101"}}}})
		if err != nil {
			t.Fatal(err)
		}
		defer w.Close()
		base := strings.Replace(w.URL, "localhost", "127.0.0.1", 1)
		targets := write("web-targets.yaml", fmt.Sprintf("targets:\n  - {name: web, class: lab, protocol: dicomweb, base_url: %q, auth: {type: bearer, token_file: %s}, tls: {ca_file: %s}}\n", base, tok, pki.Root.File))
		form := url.Values{"protocol": {"dicomweb"}, "name": {"web"}, "class": {"lab"}, "base_url": {base}, "auth_type": {"bearer"},
			"token_file": {tok}, "tls_ca_file": {pki.Root.File}}
		planFile := write("web.yaml", `name: parity dicomweb
target: web
timeout: 20s
steps:
  - {id: plaintext-refused, type: tls_reject, mode: plaintext}
  - {id: qido-patient, type: qido, level: studies, query: {PatientID: PARITY-1}, expect_uids: [`+parityStudy+`], require_attributes: [PatientID, StudyDate]}
  - {id: qido-instances, type: qido, level: instances, study: `+parityStudy+`, expect_uids: [`+paritySOP+`]}
  - {id: qido-none, type: qido, level: studies, query: {PatientID: NOBODY}, expect_none: true}
`)
		cli, webRun := checkParity(t, cfg, planFile, targets, form)
		mustPass(t, cli, 4)
		// The token went to the server, and into no artefact of either run.
		sawToken := false
		for _, r := range w.Requests() {
			sawToken = sawToken || r.Authorization == "Bearer "+synthToken
		}
		if !sawToken {
			t.Fatal("the test server never received the token")
		}
		for _, r := range []*result.Run{cli, webRun} {
			if strings.Contains(fmt.Sprintf("%+v", viewOf(r)), synthToken) {
				t.Fatal("token in a run result")
			}
		}
	})
}

func mustPass(t *testing.T, r *result.Run, steps int) {
	t.Helper()
	if r.Verdict != result.Pass || len(r.Steps) != steps {
		t.Fatalf("run %s with %d steps: %+v", r.Verdict, len(r.Steps), viewOf(r))
	}
	for _, s := range r.Steps {
		if s.Evidence == nil || len(s.Evidence.Assertions) == 0 {
			t.Fatalf("step %s has no assertions", s.ID)
		}
	}
}
