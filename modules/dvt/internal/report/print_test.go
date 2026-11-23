package report

import (
	"regexp"
	"strings"
	"testing"
)

// printBlock returns the body of the stylesheet's single @media print block.
func printBlock(t *testing.T, css string) string {
	t.Helper()
	if n := strings.Count(css, "@media print"); n != 1 {
		t.Fatalf("stylesheet has %d @media print blocks, want 1", n)
	}
	i := strings.Index(css, "@media print")
	depth, start := 0, -1
	for j := i; j < len(css); j++ {
		switch css[j] {
		case '{':
			if depth == 0 {
				start = j + 1
			}
			depth++
		case '}':
			depth--
			if depth == 0 {
				return css[start:j]
			}
		}
	}
	t.Fatal("@media print block not closed")
	return ""
}

// The HTML report is the client deliverable; printed to PDF from a browser
// it must show every evidence section (closed <details> would hide HTTP
// exchanges), use light colours whatever the reader's colour scheme, and
// not clip long values or names.
//
// Verifies: DVT-SRS-157
func TestReportPrintsAllEvidence(t *testing.T) {
	r, _ := loadRun(t, "v02-dicomweb-run.json")
	h, err := HTML(r)
	if err != nil {
		t.Fatal(err)
	}
	style := regexp.MustCompile(`(?s)<style>(.*?)</style>`).FindStringSubmatch(string(h))
	if style == nil {
		t.Fatal("report has no inline stylesheet")
	}
	pb := printBlock(t, style[1])
	for _, want := range []string{
		"details::details-content { content-visibility: visible; display: block; }",
		"pre { max-height: none; overflow: visible; }",
		".scroll { overflow: visible; }",
		"--surface-100: #FFFFFF;",
		"--ink: #0F1B24;",
		"tr, dl.meta, .badge { break-inside: avoid; }",
	} {
		if !strings.Contains(pb, want) {
			t.Errorf("print block lacks %q", want)
		}
	}
	if !strings.Contains(style[1], "td.mono:first-child { word-break: normal; overflow-wrap: break-word; }") {
		t.Error("step names may break mid-word")
	}
	if strings.Count(string(h), "<details>") == 0 {
		t.Fatal("test run has no <details> sections; the control would prove nothing")
	}
}
