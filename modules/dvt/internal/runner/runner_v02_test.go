package runner

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mulcahyio/libre-rws/modules/dvt/internal/engine"
	"github.com/mulcahyio/libre-rws/modules/dvt/internal/fakepeer"
	"github.com/mulcahyio/libre-rws/modules/dvt/internal/plan"
	"github.com/mulcahyio/libre-rws/modules/dvt/internal/result"
	"github.com/mulcahyio/libre-rws/modules/dvt/internal/store"
)

const synthToken = "dvt-synthetic-bearer-runner-0123456789" // gitleaks:allow (synthetic test value)

func secretFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func webTarget(class, tokenFile string) *store.Target {
	tg := &store.Target{Name: "web", Class: class, Protocol: store.DICOMweb, BaseURL: "https://pacs.lab.example/dicom-web"}
	if tokenFile != "" {
		tg.Auth = &store.Auth{Type: store.AuthBearer, TokenFile: tokenFile}
	}
	return tg
}

const dataPlan = `
name: web data
target: web
steps:
  - {id: stow, type: stow, files: ["fx/*.dcm"]}
  - {id: qido, type: qido, level: studies, from_store: true}
  - {id: wado, type: wado, from_store: true}
`

// Verifies: DVT-SRS-134
func TestDICOMwebDataStepsLabOnly(t *testing.T) {
	for _, class := range []string{store.Production, ""} {
		f := &fake{}
		tg := webTarget(class, "")
		if class == "" {
			if err := tg.Normalize(); err != nil { // unclassified means production
				t.Fatal(err)
			}
		}
		r := run(t, f, setup(t, dataPlan), tg)
		if r.Verdict != result.Fail || len(f.calls) != 0 {
			t.Fatalf("class %q: verdict %s, engine calls %v", class, r.Verdict, f.calls)
		}
		for _, s := range r.Steps {
			ev := s.Evidence
			if s.Status != result.StepFailed || ev == nil || len(ev.Errors) != 1 || ev.Errors[0].Kind != result.ProblemGuard || len(ev.Exchanges) != 0 {
				t.Fatalf("class %q step %s: %+v %+v", class, s.ID, s, ev)
			}
			if !strings.Contains(ev.Errors[0].Message, "only run against targets of class lab") || ev.Target.Protocol != store.DICOMweb {
				t.Fatalf("guard reason missing: %+v", ev)
			}
		}
	}
	// The same plan on a lab target passes the guard and reaches the
	// DICOMweb engine.
	f := &fake{}
	r := run(t, f, setup(t, dataPlan), webTarget(store.Lab, ""))
	for _, s := range r.Steps {
		if s.Evidence != nil && len(s.Evidence.Errors) > 0 && s.Evidence.Errors[0].Kind == result.ProblemGuard {
			t.Fatalf("lab step %s refused by the guard: %+v", s.ID, s.Evidence)
		}
	}
	if len(f.calls) == 0 || !strings.Contains(strings.Join(f.calls, ","), "stow") {
		t.Fatalf("lab target: engine calls %v", f.calls)
	}
}

// Verifies: DVT-SRS-142
func TestPlanMustFitTargetProtocol(t *testing.T) {
	// A DICOMweb plan on a DIMSE target: rejected before any step, also the
	// validate step does not run.
	src := "name: x\ntarget: t\nsteps:\n  - {id: v, type: validate, files: [\"fx/*.dcm\"]}\n  - {id: q, type: qido, level: studies, expect_none: true}\n"
	f := &fake{}
	r := run(t, f, setup(t, src), lab())
	if r.Verdict != result.Fail || !strings.Contains(r.Error, "a qido step needs a dicomweb target") || len(f.calls) != 0 {
		t.Fatalf("qido on dimse: %+v calls %v", r, f.calls)
	}
	for _, s := range r.Steps {
		if s.Status != result.StepNotRun {
			t.Fatalf("step %s ran: %s", s.ID, s.Status)
		}
	}
	// A DIMSE plan on a DICOMweb target.
	f = &fake{echo: engine.EchoResult{Associated: true, Status: ip(0)}}
	r = run(t, f, setup(t, "name: x\ntarget: web\nsteps:\n  - {id: e, type: echo}\n"), webTarget(store.Lab, ""))
	if r.Verdict != result.Fail || !strings.Contains(r.Error, "needs a dimse target") || len(f.calls) != 0 {
		t.Fatalf("echo on dicomweb: %+v calls %v", r, f.calls)
	}
	// tls_reject on a target without TLS.
	r = run(t, &fake{}, setup(t, "name: x\ntarget: t\nsteps:\n  - {id: p, type: tls_reject, mode: plaintext}\n"), lab())
	if r.Verdict != result.Fail || !strings.Contains(r.Error, "needs a target with TLS") {
		t.Fatalf("tls_reject without TLS: %+v", r)
	}
	// The v0.1 plan on a v0.1 target still runs every step.
	f = &fake{echo: engine.EchoResult{Associated: true, Status: ip(0)}, reject: engine.RejectResult{Rejected: true}}
	r = run(t, f, setup(t, fullPlan), lab())
	if r.Verdict != result.Pass || r.Error != "" {
		t.Fatalf("v0.1 plan: %+v", r)
	}
	for _, s := range r.Steps {
		if s.Evidence != nil || s.Verdict != "" {
			t.Fatalf("v0.1 step %s gained v0.2 fields", s.ID)
		}
	}
}

// Verifies: DVT-SRS-141, DVT-SRS-150
func TestSecretFileProblemsFailBeforeConnecting(t *testing.T) {
	dir := t.TempDir()
	_ = os.Mkdir(filepath.Join(dir, "d"), 0o750)
	cases := map[string]string{
		"missing":   filepath.Join(dir, "nope"),
		"empty":     secretFile(t, ""),
		"directory": filepath.Join(dir, "d"),
	}
	for name, p := range cases {
		f := &fake{}
		r := run(t, f, setup(t, dataPlan), webTarget(store.Lab, p))
		if r.Verdict != result.Fail || len(f.calls) != 0 {
			t.Fatalf("%s: %s %v", name, r.Verdict, f.calls)
		}
		ev := r.Steps[0].Evidence
		if ev == nil || ev.Errors[0].Kind != result.ProblemConfig || !strings.Contains(ev.Errors[0].Message, "auth.token_file") || len(ev.Exchanges) != 0 {
			t.Fatalf("%s: %+v", name, ev)
		}
	}
	// A DIMSE target with a missing CA fails a v0.1 echo before the engine.
	tg := lab()
	tg.TLS = &store.TLS{CAFile: filepath.Join(dir, "no-ca.pem")}
	f := &fake{echo: engine.EchoResult{Associated: true, Status: ip(0)}}
	r := run(t, f, setup(t, "name: x\ntarget: t\nsteps:\n  - {id: e, type: echo}\n"), tg)
	if r.Verdict != result.Fail || len(f.calls) != 0 || !strings.Contains(strings.Join(r.Steps[0].Messages, ";"), "tls.ca_file") {
		t.Fatalf("missing CA: %+v calls %v", r.Steps[0], f.calls)
	}
	// With a readable CA the echo runs over TLS: the worker gets the TLS
	// paths, and an echo whose engine reports no verified TLS connection
	// fails, so a clear-text fallback can never pass (DVT-SRS-100, -107).
	pki, err := fakepeer.NewPKI(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tg.TLS.CAFile = pki.Root.File
	r = run(t, f, setup(t, "name: x\ntarget: t\nsteps:\n  - {id: e, type: echo}\n"), tg)
	if r.Verdict != result.Fail || len(f.calls) != 1 || !strings.Contains(strings.Join(r.Steps[0].Messages, ";"), "TLS error") {
		t.Fatalf("TLS echo without TLS facts: %+v calls %v", r.Steps[0], f.calls)
	}
	f.echo.TLS = &result.TLSInfo{Version: "TLSv1.3", Cipher: "TLS_AES_128_GCM_SHA256", Verified: true,
		Peer: &result.PeerCert{Subject: "CN=peer", Issuer: "CN=test-root-ca", SANDNS: []string{"peer.test"}, SHA256: "ab"}}
	r = run(t, f, setup(t, "name: x\ntarget: t\nsteps:\n  - {id: e, type: echo}\n"), tg)
	msgs := strings.Join(r.Steps[0].Messages, ";")
	if r.Verdict != result.Pass || !strings.Contains(msgs, "TLS: TLSv1.3 TLS_AES_128_GCM_SHA256, certificate verified") || !strings.Contains(msgs, "peer.test") {
		t.Fatalf("TLS echo with TLS facts: %+v", r.Steps[0])
	}
	f.echo.TLS.Verified = false
	if r = run(t, f, setup(t, "name: x\ntarget: t\nsteps:\n  - {id: e, type: echo}\n"), tg); r.Verdict != result.Fail {
		t.Fatalf("unverified TLS passed: %+v", r.Steps[0])
	}
	// Web UI: a file outside the secrets directory is refused.
	tok := secretFile(t, synthToken+"\n")
	rn := &Runner{Engines: &fake{}, WorkDir: t.TempDir(), Version: "test", SecretsDir: "/run/secrets"}
	r = rn.Run(context.Background(), "id", setup(t, dataPlan), webTarget(store.Lab, tok), nil)
	if ev := r.Steps[0].Evidence; ev == nil || !strings.Contains(ev.Errors[0].Message, "secrets directory") {
		t.Fatalf("outside secrets dir: %+v", r.Steps[0])
	}
}

// leaky returns the token from the validation engine, as a misbehaving
// engine or a peer echoing a header into an error message would.
type leaky struct {
	fake
	msg string
}

func (l *leaky) Validate(_ context.Context, files []string, _ map[string]string) result.Validation {
	v := result.Validation{Files: []result.FileResult{{File: files[0], EngineErrors: []result.EngineError{{Engine: "x", Message: l.msg}}}}}
	v.Finish()
	return v
}

// Verifies: DVT-SRS-152
func TestSecretInjectedIntoOutputIsRedactedAndFailsTheRun(t *testing.T) {
	tok := secretFile(t, synthToken+"\n")
	forms := map[string]string{
		"raw":    "upstream said: Authorization: Bearer " + synthToken,
		"base64": "header " + base64.StdEncoding.EncodeToString([]byte(synthToken)),
	}
	for name, msg := range forms {
		var saved []*result.Run
		rn := &Runner{Engines: &leaky{msg: msg}, WorkDir: t.TempDir(), Version: "test"}
		src := "name: x\ntarget: web\nsteps:\n  - {id: v, type: validate, files: [\"fx/*.dcm\"]}\n"
		r := rn.Run(context.Background(), "id", setup(t, src), webTarget(store.Lab, tok), func(p *result.Run) {
			b, _ := json.Marshal(p)
			var cp result.Run
			_ = json.Unmarshal(b, &cp)
			saved = append(saved, &cp)
		})
		all, _ := json.Marshal(r)
		for _, p := range saved {
			b, _ := json.Marshal(p)
			all = append(all, b...)
		}
		for _, f := range []string{synthToken, base64.StdEncoding.EncodeToString([]byte(synthToken))} {
			if strings.Contains(string(all), f) {
				t.Fatalf("%s: secret form left in the result or a progress snapshot", name)
			}
		}
		if r.Verdict != result.Fail || r.Steps[0].Status != result.StepFailed || !strings.Contains(string(all), "[REDACTED]") {
			t.Fatalf("%s: %+v", name, r)
		}
		if !strings.Contains(strings.Join(r.Steps[0].Messages, ";"), "secret value(s) found") {
			t.Fatalf("%s: reason missing: %v", name, r.Steps[0].Messages)
		}
	}
	// Without a leak the same target and plan pass: the control fails for
	// the secret, not for the target.
	rn := &Runner{Engines: &leaky{msg: ""}, WorkDir: t.TempDir(), Version: "test"}
	src := "name: x\ntarget: web\nsteps:\n  - {id: v, type: validate, files: [\"fx/*.dcm\"]}\n"
	r := rn.Run(context.Background(), "id", setup(t, src), webTarget(store.Lab, tok), nil)
	if r.Steps[0].Messages == nil || strings.Contains(strings.Join(r.Steps[0].Messages, ";"), "secret") {
		t.Fatalf("clean run flagged: %+v", r.Steps[0])
	}
	// A secret in the run-level error is caught too.
	r = &result.Run{ID: "x", Error: "target says " + synthToken, Steps: []result.Step{{Status: result.StepPassed}}}
	r.Finish()
	if n := r.Redact(SecretsFor(webTarget(store.Lab, tok), "")); n != 1 || strings.Contains(r.Error, synthToken) || r.Verdict != result.Fail {
		t.Fatalf("run-level secret: %d %+v", n, r)
	}
	// And a secret that only reaches run-level fields during a run (here
	// the plan name) fails the run the runner returns.
	src = "name: " + synthToken + "\ntarget: web\nsteps:\n  - {id: v, type: validate, files: [\"fx/*.dcm\"]}\n"
	r = rn.Run(context.Background(), "id", setup(t, src), webTarget(store.Lab, tok), nil)
	if b, _ := json.Marshal(r); strings.Contains(string(b), synthToken) || r.Verdict != result.Fail || !strings.Contains(r.Error, "secret value(s) found") {
		t.Fatalf("run-level field: %+v", r)
	}
}

// Verifies: DVT-SRS-134, DVT-SRS-142
func TestStepProtocolTableMatchesRunner(t *testing.T) {
	for _, typ := range []string{plan.Associate, plan.TLSReject, plan.Qido, plan.Wado, plan.Stow} {
		if !v02(typ) {
			t.Fatalf("%s not treated as a v0.2 step", typ)
		}
	}
	for _, typ := range []string{plan.Validate, plan.Echo, plan.Store, plan.Find, plan.Move, plan.Reject} {
		if v02(typ) {
			t.Fatalf("%s treated as a v0.2 step", typ)
		}
	}
}
