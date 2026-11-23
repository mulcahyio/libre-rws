package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/mulcahyio/libre-rws/modules/dvt/internal/report"
	"github.com/mulcahyio/libre-rws/modules/dvt/internal/result"
	"github.com/mulcahyio/libre-rws/modules/dvt/internal/store"
	"github.com/mulcahyio/libre-rws/modules/dvt/internal/web"
)

// CLI/web parity (DVT-SRS-146): the same plan and target, given to
// `libredvt run -targets FILE` and to the web UI (target saved through the
// target form, plan uploaded), must give the same step verdicts and
// assertions. Both build their runner with Config.runner.

const parityPassword = "parity-test-password"

// stepView is what must match: everything a reader of the report relies
// on, without times, durations and IDs.
type stepView struct {
	ID, Type   string
	Status     result.StepStatus
	Verdict    result.Verdict
	Messages   []string
	Assertions []result.Assertion
	Problems   []result.Problem
	Exchanges  []string
	Instances  []result.Instance
}

type runView struct {
	Verdict    result.Verdict
	Error      string
	TargetDesc string
	TargetInfo *result.TargetInfo
	Steps      []stepView
}

func viewOf(r *result.Run) runView {
	v := runView{Verdict: r.Verdict, Error: r.Error, TargetDesc: r.TargetDesc, TargetInfo: r.TargetInfo}
	for _, s := range r.Steps {
		sv := stepView{ID: s.ID, Type: s.Type, Status: s.Status, Verdict: s.Verdict, Messages: s.Messages}
		if e := s.Evidence; e != nil {
			sv.Assertions, sv.Problems, sv.Instances = e.Assertions, e.Errors, e.Instances
			for _, x := range e.Exchanges {
				sv.Exchanges = append(sv.Exchanges, fmt.Sprintf("%d %s %s %s %s %s", x.Seq, x.Protocol, x.Operation, x.Endpoint, x.Outcome, report.ExchangeResult(x)))
			}
		}
		v.Steps = append(v.Steps, sv)
	}
	return v
}

// runCLI runs the plan with -targets and returns the run from -json.
func runCLI(t *testing.T, planFile, targetsFile string) *result.Run {
	t.Helper()
	out := filepath.Join(t.TempDir(), "cli.json")
	runCmd([]string{"-targets", targetsFile, "-json", out, planFile})
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("CLI wrote no JSON: %v", err)
	}
	var r result.Run
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	return &r
}

var csrfRe = regexp.MustCompile(`name="csrf" value="([0-9a-f]+)"`)

// runWeb starts the web UI as `libredvt serve` does, saves the target
// through the target form, uploads the plan and returns the stored run.
func runWeb(t *testing.T, cfg Config, planFile string, form url.Values) *result.Run {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	srv, err := web.New(context.Background(), web.Options{Store: st, Runner: cfg.runner(t.TempDir(), cfg.SecretsDir),
		Validator: cfg.engines(), Password: parityPassword, Version: version})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	cl := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := cl.PostForm(ts.URL+"/login", url.Values{"password": {parityPassword}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	cookies := resp.Cookies()
	do := func(req *http.Request) (*http.Response, string) {
		t.Helper()
		for _, c := range cookies {
			req.AddCookie(c)
		}
		resp, err := cl.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp, string(b)
	}
	req, _ := http.NewRequest("GET", ts.URL+"/targets", nil)
	_, page := do(req)
	m := csrfRe.FindStringSubmatch(page)
	if m == nil {
		t.Fatal("no CSRF token")
	}
	form.Set("csrf", m[1])
	req, _ = http.NewRequest("POST", ts.URL+"/targets", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if resp, body := do(req); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("target form refused: %d %s", resp.StatusCode, body)
	}
	data, err := os.ReadFile(planFile)
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	_ = mw.WriteField("csrf", m[1])
	fw, _ := mw.CreateFormFile("plan", filepath.Base(planFile))
	_, _ = fw.Write(data)
	_ = mw.Close()
	req, _ = http.NewRequest("POST", ts.URL+"/plans/run", &b)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, body := do(req)
	loc := resp.Header.Get("Location")
	if resp.StatusCode != http.StatusSeeOther || !strings.HasPrefix(loc, "/runs/") {
		t.Fatalf("plan run refused: %d %s", resp.StatusCode, body)
	}
	srv.Wait()
	r, err := st.Run(strings.TrimPrefix(loc, "/runs/"))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// checkParity runs the plan both ways and compares the runs.
func checkParity(t *testing.T, cfg Config, planFile, targetsFile string, form url.Values) (cli, webRun *result.Run) {
	t.Helper()
	cli = runCLI(t, planFile, targetsFile)
	webRun = runWeb(t, cfg, planFile, form)
	a, b := viewOf(cli), viewOf(webRun)
	if !reflect.DeepEqual(a, b) {
		ja, _ := json.MarshalIndent(a, "", " ")
		jb, _ := json.MarshalIndent(b, "", " ")
		t.Fatalf("CLI and web UI differ\nCLI: %s\nweb: %s", ja, jb)
	}
	n := 0
	for _, s := range a.Steps {
		n += len(s.Assertions)
	}
	t.Logf("parity: %d steps, %d assertions, verdict %s, identical from CLI and web UI", len(a.Steps), n, a.Verdict)
	return cli, webRun
}

// Verifies: DVT-SRS-146, DVT-SRS-041
func TestCLIAndWebGiveTheSameResult(t *testing.T) {
	dir := setupFakes(t)
	secrets := filepath.Join(dir, "secrets")
	_ = os.MkdirAll(secrets, 0o750)
	tok := filepath.Join(secrets, "token")
	_ = os.WriteFile(tok, []byte(synthToken+"\n"), 0o600)
	t.Setenv("LIBRE_DVT_SECRETS_DIR", secrets)
	cfg, err := loadConfig(flag.NewFlagSet("parity", flag.ContinueOnError), nil)
	if err != nil {
		t.Fatal(err)
	}
	targets := filepath.Join(dir, "targets.yaml")
	_ = os.WriteFile(targets, []byte("targets:\n  - {name: web, class: lab, protocol: dicomweb, base_url: \"https://pacs.lab.example/dicom-web\", auth: {type: bearer, token_file: "+tok+"}}\n"), 0o600)
	form := url.Values{"protocol": {"dicomweb"}, "name": {"web"}, "class": {"lab"}, "base_url": {"https://pacs.lab.example/dicom-web"}, "auth_type": {"bearer"}, "token_file": {tok}}
	// A validate step that passes and one that fails, and a DICOMweb step
	// the fake worker cannot answer (fails as an engine error).
	planFile := filepath.Join(dir, "parity.yaml")
	_ = os.WriteFile(planFile, []byte(fmt.Sprintf("name: parity\ntarget: web\nsteps:\n  - {id: good, type: validate, files: [%s]}\n  - {id: bad, type: validate, files: [%s]}\n  - {id: q, type: qido, level: studies, expect_none: true}\n",
		filepath.Join(dir, "fx", "good.dcm"), filepath.Join(dir, "fx", "bad.dcm"))), 0o644)
	cli, _ := checkParity(t, cfg, planFile, targets, form)
	if len(cli.Steps) != 3 || cli.Steps[0].Status != result.StepPassed || cli.Steps[1].Status != result.StepFailed || cli.Steps[2].Verdict != result.Fail {
		t.Fatalf("unexpected run: %+v", viewOf(cli))
	}
	if cli.TargetInfo == nil || cli.TargetInfo.Protocol != "dicomweb" || cli.TargetInfo.Endpoint != "https://pacs.lab.example/dicom-web" {
		t.Fatalf("target info: %+v", cli.TargetInfo)
	}
	// A mutant of either path (different target, different runner
	// settings) shows up as a difference: the target file and the form
	// disagree on the class here, and the comparison must notice.
	form.Set("class", "production")
	if a, b := viewOf(runCLI(t, planFile, targets)), viewOf(runWeb(t, cfg, planFile, form)); reflect.DeepEqual(a, b) {
		t.Fatal("parity check cannot see a different target")
	}
}
