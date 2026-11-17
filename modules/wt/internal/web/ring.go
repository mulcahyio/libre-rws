package web

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mulcahyio/libre-rws/modules/wt/internal/dcmclient"
)

// LibreDCM's recent audit records and log lines, with filters and CSV
// export (WT-SRS-029, -030, -033, -037). Both come from fixed-size buffers
// in the node's memory: the site's log collector keeps the full record.

const (
	actLDCMAudit = "ldcm-audit"
	actLogs      = "logs"
)

var (
	reEventType = regexp.MustCompile(`^[A-Za-z0-9._-]{0,64}$`)
	outcomes    = []string{"", "success", "minor-failure", "serious-failure", "major-failure", "pending"}
	logLevels   = []string{"debug", "info", "warn", "error"}
	periods     = []period{{"15m", "15 minutes", 15 * time.Minute}, {"1h", "1 hour", time.Hour}, {"6h", "6 hours", 6 * time.Hour},
		{"24h", "24 hours", 24 * time.Hour}, {"7d", "7 days", 7 * 24 * time.Hour}, {"all", "all held", 0}}
	auditLimits = []int{100, 250, 500}
	logLimits   = []int{200, 500, 1000}
)

type period struct {
	Code, Label string
	D           time.Duration
}

// ringFilter is a filter of the audit or log page, from the address. Every
// value is checked against a fixed list or pattern; anything else falls
// back to the default.
type ringFilter struct {
	Type, Outcome, Level, Period string
	Limit                        int
}

func oneOf(v string, list []string, def string) string {
	for _, x := range list {
		if v == x {
			return v
		}
	}
	return def
}

func limitOf(v string, list []int) int {
	n, _ := strconv.Atoi(v)
	for _, x := range list {
		if n == x {
			return n
		}
	}
	return list[0]
}

func readFilter(r *http.Request, audit bool) ringFilter {
	q := r.URL.Query()
	codes := make([]string, 0, len(periods))
	for _, p := range periods {
		codes = append(codes, p.Code)
	}
	f := ringFilter{Period: oneOf(q.Get("period"), codes, "24h")}
	if audit {
		f.Outcome, f.Limit = oneOf(q.Get("outcome"), outcomes, ""), limitOf(q.Get("limit"), auditLimits)
		if t := strings.TrimSpace(q.Get("type")); reEventType.MatchString(t) {
			f.Type = t
		}
	} else {
		f.Level, f.Limit = oneOf(strings.ToLower(q.Get("level")), logLevels, "info"), limitOf(q.Get("limit"), logLimits)
	}
	return f
}

func (f ringFilter) query(now time.Time) dcmclient.RingQuery {
	q := dcmclient.RingQuery{Type: f.Type, Outcome: f.Outcome, Level: f.Level, Limit: f.Limit}
	for _, p := range periods {
		if p.Code == f.Period && p.D > 0 {
			q.Since = now.Add(-p.D)
		}
	}
	return q
}

// String describes the filter for the audit log.
func (f ringFilter) String() string {
	var parts []string
	for _, kv := range [][2]string{{"type", f.Type}, {"outcome", f.Outcome}, {"level", f.Level}, {"period", f.Period}} {
		if kv[1] != "" {
			parts = append(parts, kv[0]+"="+kv[1])
		}
	}
	return strings.Join(append(parts, "limit="+strconv.Itoa(f.Limit)), " ")
}

// encode is the filter as address parameters, for the export link.
func (f ringFilter) encode() string {
	v := []string{"period=" + f.Period, "limit=" + strconv.Itoa(f.Limit)}
	if f.Type != "" {
		v = append(v, "type="+f.Type)
	}
	if f.Outcome != "" {
		v = append(v, "outcome="+f.Outcome)
	}
	if f.Level != "" {
		v = append(v, "level="+f.Level)
	}
	return strings.Join(v, "&")
}

// entry is one audit record or log line as the pages show it.
type entry struct {
	Time    string
	Level   string
	Message string // log message, or the event name of an audit record
	Outcome string
	Who     string // operator, or the calling AE of DICOM traffic
	Fields  []string
}

// fieldText prints one value of a record as text.
func fieldText(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case nil:
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

// parseEntries turns the JSON lines of LibreDCM into rows, newest first. A
// line that is not a JSON object is shown as it is.
func parseEntries(raw []json.RawMessage, audit bool) []entry {
	out := make([]entry, 0, len(raw))
	for i := len(raw) - 1; i >= 0; i-- {
		var m map[string]any
		dec := json.NewDecoder(bytes.NewReader(raw[i]))
		dec.UseNumber()
		if err := dec.Decode(&m); err != nil {
			out = append(out, entry{Message: string(raw[i])})
			continue
		}
		e := entry{Level: fieldText(m["level"]), Outcome: fieldText(m["outcome"])}
		if t, err := time.Parse(time.RFC3339Nano, fieldText(m["time"])); err == nil {
			e.Time = t.Local().Format("2006-01-02 15:04:05")
		} else {
			e.Time = fieldText(m["time"])
		}
		skip := map[string]bool{"time": true, "level": true, "outcome": true}
		if audit {
			e.Message = fieldText(m["event"])
			skip["event"], skip["audit"], skip["msg"] = true, true, true
			if op := fieldText(m["operator"]); op != "" {
				e.Who, skip["operator"] = op, true
			} else if ae := fieldText(m["calling_ae"]); ae != "" {
				e.Who, skip["calling_ae"] = "AE "+ae, true
			}
		} else {
			e.Message, skip["msg"] = fieldText(m["msg"]), true
		}
		keys := make([]string, 0, len(m))
		for k := range m {
			if !skip[k] {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			e.Fields = append(e.Fields, k+"="+fieldText(m[k]))
		}
		out = append(out, e)
	}
	return out
}

// csvCell protects a cell against formula injection: a spreadsheet program
// runs a cell that starts with = + - @ (or a tab or carriage return) as a
// formula, so such a cell gets a leading apostrophe (WT-SRS-037).
func csvCell(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

// csvOf writes the rows as CSV.
func csvOf(head []string, rows [][]string) ([]byte, error) {
	var b bytes.Buffer
	cw := csv.NewWriter(&b)
	if err := cw.Write(head); err != nil {
		return nil, err
	}
	for _, row := range rows {
		cells := make([]string, len(row))
		for i, c := range row {
			cells[i] = csvCell(c)
		}
		if err := cw.Write(cells); err != nil {
			return nil, err
		}
	}
	cw.Flush()
	return b.Bytes(), cw.Error()
}

// ringPage serves the audit and the log page and their CSV export.
func (s *Server) ringPage(audit, export bool) func(http.ResponseWriter, *http.Request, *reqCtx, *instCtx) {
	return func(w http.ResponseWriter, r *http.Request, rc *reqCtx, ic *instCtx) {
		feature, label, act := "logs", "Logs", actLogs
		if audit {
			feature, label, act = "audit", "LibreDCM audit records", actLDCMAudit
		}
		if !s.need(w, r, rc, ic, feature, label) {
			return
		}
		f := readFilter(r, audit)
		var v dcmclient.Ring
		var err error
		if audit {
			v, err = ic.client.AuditRecords(r.Context(), f.query(time.Now()))
		} else {
			v, err = ic.client.LogLines(r.Context(), f.query(time.Now()))
		}
		if err != nil {
			s.readFailed(w, r, rc, ic, act+".view", label, err)
			return
		}
		if !s.sameNode(w, r, rc, ic, v.Instance) {
			return
		}
		raw := v.Lines
		if audit {
			raw = v.Records
		}
		entries := parseEntries(raw, audit)
		if export {
			s.ringExport(w, r, rc, ic, act, f, entries, audit)
			return
		}
		// No record, no page (WT-SRS-033).
		if !s.viewed(w, r, rc, ic, act+".view", f.String()) {
			return
		}
		s.renderI(w, r, http.StatusOK, "ring", rc, ic, map[string]any{"Audit": audit, "Label": label, "F": f, "V": v, "E": entries,
			"Outcomes": outcomes, "Levels": logLevels, "Periods": periods, "Limits": map[bool][]int{true: auditLimits, false: logLimits}[audit],
			"ReadAt": time.Now(), "Export": ic.base() + "/" + feature + ".csv?" + f.encode(), "CanExport": isAdmin(rc)})
	}
}

// ringExport sends the shown entries as a CSV file. The export is recorded
// in the Libre WT audit log first; when the record can't be written, no
// file is sent (WT-SRS-037).
func (s *Server) ringExport(w http.ResponseWriter, r *http.Request, rc *reqCtx, ic *instCtx, act string, f ringFilter, entries []entry, audit bool) {
	head := []string{"time", "level", "message", "details"}
	if audit {
		head = []string{"time", "event", "outcome", "who", "details"}
	}
	rows := make([][]string, 0, len(entries))
	for _, e := range entries {
		if audit {
			rows = append(rows, []string{e.Time, e.Message, e.Outcome, e.Who, strings.Join(e.Fields, " ")})
		} else {
			rows = append(rows, []string{e.Time, e.Level, e.Message, strings.Join(e.Fields, " ")})
		}
	}
	body, err := csvOf(head, rows)
	if err != nil {
		s.readFailed(w, r, rc, ic, act+".export", "Export", err)
		return
	}
	if err := s.auditI(r, rc.user.Username, ic.name, act+".export", f.String(), "success", fmt.Sprintf("%d rows exported as CSV", len(rows))); err != nil {
		s.renderI(w, r, http.StatusInternalServerError, "error", rc, ic, map[string]any{"Title": "Export not made",
			"Message": "The audit log can't be written, so nothing was exported."})
		return
	}
	kind := "logs"
	if audit {
		kind = "audit"
	}
	// The instance name is a configured name of letters, digits, - and _.
	name := fmt.Sprintf("librewt-%s-%s-%s.csv", ic.name, kind, time.Now().UTC().Format("20060102T150405Z"))
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.WriteHeader(http.StatusOK)
	// #nosec G705 -- a CSV attachment (text/csv, nosniff, Content-Disposition attachment), never rendered as HTML; cells are formula-protected
	if _, err := w.Write(body); err != nil {
		s.log.Warn("export not delivered", "instance", ic.name, "err", err)
	}
}
