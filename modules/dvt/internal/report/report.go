// Package report writes LibreDVT runs as JUnit XML (DVT-SRS-043) and as a
// self-contained HTML report (DVT-SRS-044).
package report

import (
	"bytes"
	_ "embed"
	"encoding/xml"
	"fmt"
	"html/template"
	"sort"
	"strings"
	"time"

	"github.com/mulcahyio/libre-rws/modules/dvt/internal/result"
)

type junitSuites struct {
	XMLName  xml.Name     `xml:"testsuites"`
	Name     string       `xml:"name,attr"`
	Tests    int          `xml:"tests,attr"`
	Failures int          `xml:"failures,attr"`
	Time     string       `xml:"time,attr"`
	Suites   []junitSuite `xml:"testsuite"`
}

type junitSuite struct {
	Name       string          `xml:"name,attr"`
	Tests      int             `xml:"tests,attr"`
	Failures   int             `xml:"failures,attr"`
	Timestamp  string          `xml:"timestamp,attr"`
	Time       string          `xml:"time,attr"`
	Properties []junitProperty `xml:"properties>property"`
	Cases      []junitCase     `xml:"testcase"`
}

type junitProperty struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

type junitCase struct {
	Name       string          `xml:"name,attr"`
	Classname  string          `xml:"classname,attr"`
	Time       string          `xml:"time,attr"`
	Properties []junitProperty `xml:"properties>property,omitempty"`
	Failure    *junitFailure   `xml:"failure,omitempty"`
	SystemOut  string          `xml:"system-out,omitempty"`
}

type junitFailure struct {
	Message string `xml:"message,attr"`
	Type    string `xml:"type,attr"`
	Text    string `xml:",chardata"`
}

func secs(d time.Duration) string { return fmt.Sprintf("%.3f", d.Seconds()) }

// JUnit renders a run as JUnit XML: one test case per step, with the
// verified requirement IDs as properties.
func JUnit(r *result.Run) ([]byte, error) {
	s := junitSuite{Name: r.Plan, Timestamp: r.Started.Format(time.RFC3339), Time: secs(r.Finished.Sub(r.Started))}
	s.Properties = append(s.Properties,
		junitProperty{"run_id", r.ID}, junitProperty{"plan_sha256", r.PlanSHA256},
		junitProperty{"target", r.TargetDesc}, junitProperty{"verdict", string(r.Verdict)})
	for _, k := range sortedKeys(r.Versions) {
		s.Properties = append(s.Properties, junitProperty{"version." + k, r.Versions[k]})
	}
	if r.Error != "" {
		s.Properties = append(s.Properties, junitProperty{"error", r.Error})
	}
	for _, st := range r.Steps {
		c := junitCase{Name: st.ID, Classname: "libredvt." + st.Type, Time: secs(st.Duration), SystemOut: strings.Join(st.Messages, "\n")}
		if len(st.Verifies) > 0 {
			c.Properties = []junitProperty{{"verifies", strings.Join(st.Verifies, ", ")}}
		}
		if st.Status != result.StepPassed {
			msg := string(st.Status)
			if len(st.Messages) > 0 {
				msg = st.Messages[0]
			}
			c.Failure = &junitFailure{Message: msg, Type: string(st.Status), Text: strings.Join(st.Messages, "\n")}
			s.Failures++
		}
		s.Cases = append(s.Cases, c)
		s.Tests++
	}
	if len(r.Steps) == 0 || r.Error != "" {
		// Make a plan-level failure visible to CI even without steps.
		s.Cases = append(s.Cases, junitCase{Name: "plan", Classname: "libredvt.plan",
			Failure: &junitFailure{Message: firstNonEmpty(r.Error, "no steps"), Type: "plan"}})
		s.Tests++
		s.Failures++
	}
	doc := junitSuites{Name: "LibreDVT", Tests: s.Tests, Failures: s.Failures, Time: s.Time, Suites: []junitSuite{s}}
	b, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), append(b, '\n')...), nil
}

func firstNonEmpty(a ...string) string {
	for _, s := range a {
		if s != "" {
			return s
		}
	}
	return ""
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

//go:embed report.html.tmpl
var reportTmpl string

// CSS is the shared stylesheet (also served by the web UI), inlined into
// reports so they are self-contained.
//
//go:embed style.css
var CSS string

// Funcs are template helpers shared with the web UI.
var Funcs = template.FuncMap{
	"dict2":    func(k1 string, v1 any, k2 string, v2 any) map[string]any { return map[string]any{k1: v1, k2: v2} },
	"versions": sortedKeys,
	"join":     strings.Join,
	"ts":       func(t time.Time) string { return t.UTC().Format("2006-01-02 15:04:05 UTC") },
	"dur":      func(d time.Duration) string { return d.Round(time.Millisecond).String() },
	"verdictClass": func(v result.Verdict) string {
		switch v {
		case result.Pass:
			return "pass"
		case result.PassWithWarnings:
			return "warn"
		default:
			return "fail"
		}
	},
	"stepClass": func(s result.StepStatus) string {
		if s == result.StepPassed {
			return "pass"
		}
		return "fail"
	},
	"counts": func(v *result.Validation) string {
		e, w, ee := v.Counts()
		return fmt.Sprintf("%d errors, %d warnings, %d engine errors", e, w, ee)
	},
}

// cssSafe marks the embedded stylesheet as trusted CSS for the template.
var cssSafe = template.CSS(CSS) // #nosec G203 -- compile-time embedded constant, never user input

var tmpl = template.Must(template.New("report").Funcs(Funcs).Parse(reportTmpl))

// HTML renders a run as one self-contained HTML page.
func HTML(r *result.Run) ([]byte, error) {
	var b bytes.Buffer
	err := tmpl.ExecuteTemplate(&b, "run-page", map[string]any{"Run": r, "CSS": cssSafe})
	return b.Bytes(), err
}

// ValidationHTML renders a validation as one self-contained HTML page.
func ValidationHTML(v *result.Validation) ([]byte, error) {
	var b bytes.Buffer
	err := tmpl.ExecuteTemplate(&b, "validation-page", map[string]any{"V": v, "CSS": cssSafe})
	return b.Bytes(), err
}

// Template returns the parsed report templates (the web UI embeds the
// "run-body" and "validation-body" blocks).
func Template() *template.Template { return tmpl }
