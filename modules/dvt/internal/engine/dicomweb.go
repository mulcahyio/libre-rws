package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mulcahyio/libre-rws/modules/dvt/internal/result"
)

// Limits bound what the worker reads from a DICOMweb target
// (DVT-SRS-154, plan-schema.md §3.3).
type Limits struct {
	MaxResponseBytes    int64
	MaxParts            int64
	MaxPartBytes        int64
	MaxJSONBytes        int64
	MaxPages            int64
	MaxBodyExcerptBytes int64
}

// Web is a DICOMweb target as the worker needs it: the base URL, file
// paths and names only, never secret values (DVT-SRS-153). TLS is on when
// the base URL is https.
type Web struct {
	BaseURL   string
	Timeout   time.Duration
	TokenFile string // bearer token file (path only); empty = no authentication
	TLS       *TLS   // trust settings for an https base URL; nil = defaults
	Limits    Limits
}

func (w Web) timeout() time.Duration {
	if w.Timeout <= 0 {
		return 30 * time.Second
	}
	return w.Timeout
}

// processTimeout bounds a whole worker process doing ops requests.
func (w Web) processTimeout(ops int) time.Duration {
	return time.Duration(ops+2)*w.timeout() + 15*time.Second
}

// args are the options every DICOMweb worker command takes.
func (w Web) args(cmd string) []string {
	a := []string{cmd, "--base-url", w.BaseURL, "--timeout", strconv.FormatFloat(w.timeout().Seconds(), 'f', -1, 64)}
	if w.TokenFile != "" {
		a = append(a, "--token-file", w.TokenFile)
	}
	if strings.HasPrefix(w.BaseURL, "https://") {
		t := w.TLS
		if t == nil {
			t = &TLS{}
		}
		a = append(a, t.trustArgs()...)
		if t.ClientCert != "" {
			a = append(a, "--client-cert", t.ClientCert, "--client-key", t.ClientKey)
		}
	}
	l := w.Limits
	return append(a, "--max-response-bytes", strconv.FormatInt(l.MaxResponseBytes, 10),
		"--max-parts", strconv.FormatInt(l.MaxParts, 10), "--max-part-bytes", strconv.FormatInt(l.MaxPartBytes, 10),
		"--max-json-bytes", strconv.FormatInt(l.MaxJSONBytes, 10), "--max-pages", strconv.FormatInt(l.MaxPages, 10),
		"--max-excerpt", strconv.FormatInt(l.MaxBodyExcerptBytes, 10))
}

// HTTP operations the worker may record.
const (
	OpGet  = "GET"
	OpPost = "POST"
)

var webOps = map[string]string{OpGet: "http", OpPost: "http"}

var webKinds = map[string]bool{
	result.ProblemEngine: true, result.ProblemProtocol: true, result.ProblemTimeout: true,
	result.ProblemConnectionClosed: true, result.ProblemTLS: true, result.ProblemConfig: true, result.ProblemLimit: true,
}

// checkWebExchanges holds HTTP exchanges to the evidence model: seq 1, 2,
// 3 …; GET or POST over http with HTTP details; a closed outcome set; an
// error with a known problem kind; TLS details on every https request
// (DVT-SRS-118, -107). Anything else is an engine error.
func checkWebExchanges(xs []result.Exchange, https bool) error {
	for i, x := range xs {
		where := fmt.Sprintf("exchange %d", i+1)
		switch {
		case x.Seq != i+1:
			return fmt.Errorf("%s has seq %d", where, x.Seq)
		case webOps[x.Operation] == "" || x.Protocol != "http":
			return fmt.Errorf("%s: operation %q with protocol %q", where, x.Operation, x.Protocol)
		case x.Outcome != result.ExchangeCompleted && x.Outcome != result.ExchangeError:
			return fmt.Errorf("%s: outcome %q", where, x.Outcome)
		case x.Outcome == result.ExchangeError && x.Error == nil:
			return fmt.Errorf("%s: outcome error without a problem", where)
		case x.Error != nil && (!webKinds[x.Error.Kind] || x.Error.Message == ""):
			return fmt.Errorf("%s: problem kind %q or empty message", where, x.Error.Kind)
		case x.HTTP == nil:
			return fmt.Errorf("%s: no HTTP details", where)
		case x.HTTP.Method != x.Operation || x.HTTP.URL == "" || x.HTTP.URL != x.Endpoint:
			return fmt.Errorf("%s: HTTP method or URL does not match the exchange", where)
		case x.Association != nil:
			return fmt.Errorf("%s: association details in an HTTP exchange", where)
		case x.Outcome == result.ExchangeCompleted && (x.HTTP.Status < 100 || x.HTTP.Status > 599 || x.HTTP.Body == nil):
			return fmt.Errorf("%s: completed without an HTTP status and body record", where)
		case https && x.TLS == nil && (x.Outcome == result.ExchangeCompleted || x.Error.Kind == result.ProblemTLS):
			return fmt.Errorf("%s: https request without TLS details", where)
		case !https && x.TLS != nil:
			return fmt.Errorf("%s: TLS details on an http request", where)
		}
		if x.Outcome == result.ExchangeCompleted && https && !x.TLS.Verified {
			return fmt.Errorf("%s: completed https request without a verified TLS connection", where)
		}
		for name, vals := range x.HTTP.RequestHeaders {
			if strings.EqualFold(name, "Authorization") {
				for _, v := range vals {
					if v != result.Redacted {
						return fmt.Errorf("%s: Authorization header value not redacted", where)
					}
				}
			}
		}
	}
	return nil
}

// seqs is the set of exchange sequence numbers.
func seqs(xs []result.Exchange) map[int]bool {
	m := map[int]bool{}
	for _, x := range xs {
		m[x.Seq] = true
	}
	return m
}

// decodeStrict decodes worker output with unknown fields rejected.
func decodeStrict(raw string, v any) error {
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// --- QIDO-RS (DVT-SRS-113…-118) ---

// QidoRequest is one qido step as the worker needs it.
type QidoRequest struct {
	Level        string // studies, series or instances
	Study        string
	Series       string
	Query        [][2]string // key (keyword or tag), value; in plan order
	IncludeField []string
	Fuzzy        bool
	Limit        int // 0 = no paging
	Offset       int
	Keywords     []string // keywords to resolve (require_attributes)
	UIDFilter    []string // from_store: one search per UID on the level's unique key
}

// QidoQuery is a query key as the worker resolved it.
type QidoQuery struct {
	Key   string `json:"key"`
	Tag   string `json:"tag"`
	VR    string `json:"vr"`
	Value string `json:"value"`
	Exact bool   `json:"exact"` // single value, no wildcard, list or range (DVT-SRS-116)
}

// QidoObject is one returned DICOM JSON object: the tags present and the
// string values of the tags LibreDVT checks.
type QidoObject struct {
	Present []string            `json:"present"`
	Values  map[string][]string `json:"values"`
}

// QidoPage is one search response.
type QidoPage struct {
	Search      string       `json:"search"`
	Offset      *int         `json:"offset"`
	Exchange    int          `json:"exchange"`
	Status      int          `json:"status"`
	ContentType string       `json:"content_type"`
	JSON        string       `json:"json"` // array, empty, invalid, not_array, not_dicom_json, none
	Detail      string       `json:"detail"`
	Objects     []QidoObject `json:"objects"`
}

// QidoResult is the worker's record of a qido step.
type QidoResult struct {
	Exchanges        []result.Exchange `json:"exchanges"`
	Keywords         map[string]string `json:"keywords"`
	Query            []QidoQuery       `json:"query"`
	Pages            []QidoPage        `json:"pages"`
	PageLimitReached bool              `json:"page_limit_reached"`
	ConfigError      string            `json:"config_error,omitempty"`
}

var jsonKinds = map[string]bool{"array": true, "empty": true, "invalid": true, "not_array": true, "not_dicom_json": true, "none": true}

// Qido searches a DICOMweb target (DVT-SRS-113). Output that does not
// match the schema is an engine error (DVT-SRS-150).
func (c Config) Qido(ctx context.Context, w Web, q QidoRequest) (QidoResult, string, error) {
	var r QidoResult
	args := append(w.args("qido"), "--level", q.Level)
	if q.Study != "" {
		args = append(args, "--study", q.Study)
	}
	if q.Series != "" {
		args = append(args, "--series", q.Series)
	}
	for _, kv := range q.Query {
		args = append(args, "--query", kv[0]+"="+kv[1])
	}
	for _, f := range q.IncludeField {
		args = append(args, "--includefield", f)
	}
	if q.Fuzzy {
		args = append(args, "--fuzzymatching")
	}
	if q.Limit > 0 {
		args = append(args, "--limit", strconv.Itoa(q.Limit), "--offset", strconv.Itoa(q.Offset))
	}
	for _, k := range q.Keywords {
		args = append(args, "--keyword", k)
	}
	for _, u := range q.UIDFilter {
		args = append(args, "--uid-filter", u)
	}
	searches := len(q.UIDFilter)
	if searches == 0 {
		searches = 1
	}
	pages := 1
	if q.Limit > 0 {
		pages = int(max(w.Limits.MaxPages, 1))
	}
	raw, err := c.workerCallStrict(ctx, w.processTimeout(searches*pages), &r, args...)
	if err != nil {
		return r, raw, err
	}
	return r, raw, checkQido(r, q, strings.HasPrefix(w.BaseURL, "https://"))
}

func checkQido(r QidoResult, q QidoRequest, https bool) error {
	if err := checkWebExchanges(r.Exchanges, https); err != nil {
		return fmt.Errorf("worker qido: %w", err)
	}
	if r.ConfigError != "" {
		if len(r.Exchanges) > 0 || len(r.Pages) > 0 {
			return errors.New("worker qido: requests after a configuration error")
		}
		return nil
	}
	if len(r.Query) != len(q.Query) {
		return fmt.Errorf("worker qido: %d resolved query keys for %d", len(r.Query), len(q.Query))
	}
	for i, x := range r.Query {
		if x.Key != q.Query[i][0] || x.Value != q.Query[i][1] || !tagRe(x.Tag) {
			return fmt.Errorf("worker qido: query key %d does not match the plan", i+1)
		}
	}
	for _, k := range q.Keywords {
		if !tagRe(r.Keywords[k]) {
			return fmt.Errorf("worker qido: keyword %q not resolved to a tag", k)
		}
	}
	have := seqs(r.Exchanges)
	if len(r.Pages) != len(r.Exchanges) {
		return fmt.Errorf("worker qido: %d pages for %d exchanges", len(r.Pages), len(r.Exchanges))
	}
	if len(r.Pages) == 0 {
		return errors.New("worker qido: no search was recorded")
	}
	for i, p := range r.Pages {
		switch {
		case !have[p.Exchange] || r.Exchanges[p.Exchange-1].Seq != i+1:
			return fmt.Errorf("worker qido: page %d refers to exchange %d", i+1, p.Exchange)
		case !jsonKinds[p.JSON]:
			return fmt.Errorf("worker qido: page %d json %q", i+1, p.JSON)
		case p.JSON != "array" && len(p.Objects) > 0:
			return fmt.Errorf("worker qido: page %d has objects without a JSON array", i+1)
		case p.Status != r.Exchanges[i].HTTP.Status:
			return fmt.Errorf("worker qido: page %d status differs from its exchange", i+1)
		}
	}
	return nil
}

func tagRe(s string) bool {
	if len(s) != 8 {
		return false
	}
	for _, c := range s {
		if !strings.ContainsRune("0123456789ABCDEF", c) {
			return false
		}
	}
	return true
}

// --- WADO-RS (DVT-SRS-119…-127) ---

// WadoRequest is one wado retrieval as the worker needs it.
type WadoRequest struct {
	Resources      []string // paths under the base URL: /studies/S[/series/R[/instances/I]]
	TransferSyntax string   // requested transfer syntax, "" = none
	OutDir         string   // where received parts are written
	Metadata       bool     // retrieve .../metadata instead of instances
	Attributes     []string // compare_attributes keywords
	ExpectFiles    []string // originals for compare_attributes
}

// WadoPart is the worker's record of one received part.
type WadoPart struct {
	Exchange          int    `json:"exchange"`
	Index             int    `json:"index"`
	ContentType       string `json:"content_type"`
	MediaType         string `json:"media_type"`
	TSParam           string `json:"ts_param"`
	Size              int64  `json:"size"`
	SHA256            string `json:"sha256"`
	Error             string `json:"error"`
	TransferSyntax    string `json:"transfer_syntax"`
	SOPInstanceUID    string `json:"sop_instance_uid"`
	SOPClassUID       string `json:"sop_class_uid"`
	StudyInstanceUID  string `json:"study_instance_uid"`
	SeriesInstanceUID string `json:"series_instance_uid"`
	File              string `json:"file"`
}

// MetaObject is one object of a metadata response.
type MetaObject struct {
	Exchange          int               `json:"exchange"`
	StudyInstanceUID  string            `json:"study_instance_uid"`
	SeriesInstanceUID string            `json:"series_instance_uid"`
	SOPInstanceUID    string            `json:"sop_instance_uid"`
	Attrs             map[string]string `json:"attrs"`
}

// Original is an expected file's attribute values for compare_attributes.
type Original struct {
	File           string            `json:"file"`
	Error          string            `json:"error"`
	SOPInstanceUID string            `json:"sop_instance_uid"`
	Attrs          map[string]string `json:"attrs"`
}

// WadoResult is the worker's record of a wado retrieval.
type WadoResult struct {
	Exchanges   []result.Exchange `json:"exchanges"`
	Parts       []WadoPart        `json:"parts"`
	Metadata    []MetaObject      `json:"metadata"`
	Originals   []Original        `json:"originals"`
	Keywords    map[string]string `json:"keywords"`
	ConfigError string            `json:"config_error,omitempty"`
}

// Wado retrieves instances or metadata (DVT-SRS-119, -124).
func (c Config) Wado(ctx context.Context, w Web, q WadoRequest) (WadoResult, string, error) {
	var r WadoResult
	args := append(w.args("wado"), "--out", q.OutDir)
	for _, res := range q.Resources {
		args = append(args, "--resource", res)
	}
	if q.TransferSyntax != "" {
		args = append(args, "--transfer-syntax", q.TransferSyntax)
	}
	if q.Metadata {
		args = append(args, "--metadata")
		for _, a := range q.Attributes {
			args = append(args, "--attr", a)
		}
		for _, f := range q.ExpectFiles {
			args = append(args, "--expect-file", f)
		}
	}
	raw, err := c.workerCallStrict(ctx, w.processTimeout(len(q.Resources)), &r, args...)
	if err != nil {
		return r, raw, err
	}
	return r, raw, checkWado(r, q, strings.HasPrefix(w.BaseURL, "https://"))
}

func checkWado(r WadoResult, q WadoRequest, https bool) error {
	if err := checkWebExchanges(r.Exchanges, https); err != nil {
		return fmt.Errorf("worker wado: %w", err)
	}
	if r.ConfigError != "" {
		if len(r.Exchanges) > 0 {
			return errors.New("worker wado: requests after a configuration error")
		}
		return nil
	}
	if len(r.Exchanges) != len(q.Resources) {
		return fmt.Errorf("worker wado: %d exchanges for %d resources", len(r.Exchanges), len(q.Resources))
	}
	for i, x := range r.Exchanges {
		want := q.Resources[i]
		if q.Metadata {
			want += "/metadata"
		}
		if x.Operation != OpGet || !strings.HasSuffix(x.Endpoint, want) {
			return fmt.Errorf("worker wado: exchange %d is not GET …%s", i+1, want)
		}
	}
	have := seqs(r.Exchanges)
	for i, p := range r.Parts {
		if !have[p.Exchange] || p.Index < 1 || p.SHA256 == "" {
			return fmt.Errorf("worker wado: part record %d is incomplete", i+1)
		}
		if p.Error == "" && (p.SOPInstanceUID == "" && p.File == "") {
			return fmt.Errorf("worker wado: part record %d has neither an error nor DICOM facts", i+1)
		}
	}
	for i, m := range r.Metadata {
		if !have[m.Exchange] {
			return fmt.Errorf("worker wado: metadata record %d refers to exchange %d", i+1, m.Exchange)
		}
	}
	if q.Metadata {
		if len(r.Originals) != len(q.ExpectFiles) {
			return fmt.Errorf("worker wado: %d original records for %d files", len(r.Originals), len(q.ExpectFiles))
		}
		for i, o := range r.Originals {
			if o.File != q.ExpectFiles[i] {
				return fmt.Errorf("worker wado: original %d is for %q, expected %q", i+1, o.File, q.ExpectFiles[i])
			}
		}
		for _, k := range q.Attributes {
			if !tagRe(r.Keywords[k]) {
				return fmt.Errorf("worker wado: keyword %q not resolved to a tag", k)
			}
		}
	} else if len(r.Metadata) > 0 || len(r.Originals) > 0 {
		return errors.New("worker wado: metadata records for an instance retrieval")
	}
	return nil
}

// --- STOW-RS (DVT-SRS-128…-136) ---

// StowFile is the worker's record of one selected file.
type StowFile struct {
	File              string `json:"file"`
	Error             string `json:"error"`
	SOPInstanceUID    string `json:"sop_instance_uid"`
	SOPClassUID       string `json:"sop_class_uid"`
	StudyInstanceUID  string `json:"study_instance_uid"`
	SeriesInstanceUID string `json:"series_instance_uid"`
	TransferSyntax    string `json:"transfer_syntax"`
	Size              int64  `json:"size"`
	SHA256            string `json:"sha256"`
}

// StowRef is one Referenced SOP Sequence item.
type StowRef struct {
	SOPClassUID    string `json:"sop_class_uid"`
	SOPInstanceUID string `json:"sop_instance_uid"`
	RetrieveURL    string `json:"retrieve_url"`
	WarningReason  string `json:"warning_reason"`
}

// StowFailed is one Failed SOP Sequence item.
type StowFailed struct {
	SOPClassUID    string `json:"sop_class_uid"`
	SOPInstanceUID string `json:"sop_instance_uid"`
	FailureReason  string `json:"failure_reason"`
}

// StowRequestRecord is one STOW-RS request and the target's answer.
type StowRequestRecord struct {
	Files         []int        `json:"files"` // indexes into StowResult.Files
	Exchange      int          `json:"exchange"`
	Status        int          `json:"status"`
	JSON          string       `json:"json"` // object, empty, other_type, not_object, not_dicom_json, invalid
	Detail        string       `json:"detail"`
	Referenced    []StowRef    `json:"referenced"`
	Failed        []StowFailed `json:"failed"`
	RetrieveURL   string       `json:"retrieve_url"`
	WarningReason string       `json:"warning_reason"`
}

// StowResult is the worker's record of a stow step.
type StowResult struct {
	Exchanges   []result.Exchange   `json:"exchanges"`
	Files       []StowFile          `json:"files"`
	Requests    []StowRequestRecord `json:"requests"`
	ConfigError string              `json:"config_error,omitempty"`
}

var stowJSON = map[string]bool{"": true, "object": true, "empty": true, "other_type": true, "not_object": true, "not_dicom_json": true, "invalid": true}

// Stow sends files with STOW-RS, batchSize parts per request
// (DVT-SRS-128).
func (c Config) Stow(ctx context.Context, w Web, study string, batchSize int, files []string) (StowResult, string, error) {
	var r StowResult
	args := w.args("stow")
	if study != "" {
		args = append(args, "--study", study)
	}
	args = append(args, "--batch-size", strconv.Itoa(batchSize))
	args = append(args, files...)
	batches := (len(files) + batchSize - 1) / max(batchSize, 1)
	raw, err := c.workerCallStrict(ctx, w.processTimeout(batches), &r, args...)
	if err != nil {
		return r, raw, err
	}
	return r, raw, checkStow(r, files, batchSize, strings.HasPrefix(w.BaseURL, "https://"))
}

func checkStow(r StowResult, files []string, batchSize int, https bool) error {
	if err := checkWebExchanges(r.Exchanges, https); err != nil {
		return fmt.Errorf("worker stow: %w", err)
	}
	if len(r.Files) != len(files) {
		return fmt.Errorf("worker stow: %d file records for %d files", len(r.Files), len(files))
	}
	bad := false
	for i, f := range r.Files {
		if f.File != files[i] {
			return fmt.Errorf("worker stow: file record %d is for %q, expected %q", i+1, f.File, files[i])
		}
		if f.Error != "" {
			bad = true
		} else if f.SOPInstanceUID == "" || f.SHA256 == "" || f.TransferSyntax == "" {
			return fmt.Errorf("worker stow: file record %d is incomplete", i+1)
		}
	}
	if bad || r.ConfigError != "" {
		if len(r.Exchanges) > 0 || len(r.Requests) > 0 {
			return errors.New("worker stow: requests sent although a file or setting was unusable")
		}
		return nil
	}
	want := (len(files) + batchSize - 1) / batchSize
	if len(r.Requests) != want || len(r.Exchanges) != want {
		return fmt.Errorf("worker stow: %d requests and %d exchanges for %d batches", len(r.Requests), len(r.Exchanges), want)
	}
	next := 0
	for i, q := range r.Requests {
		if q.Exchange != i+1 || r.Exchanges[i].Operation != OpPost {
			return fmt.Errorf("worker stow: request %d is not POST exchange %d", i+1, i+1)
		}
		if !stowJSON[q.JSON] || q.Status != r.Exchanges[i].HTTP.Status {
			return fmt.Errorf("worker stow: request %d json %q or status mismatch", i+1, q.JSON)
		}
		for _, k := range q.Files {
			if k != next {
				return fmt.Errorf("worker stow: request %d does not hold the next files in order", i+1)
			}
			next++
		}
		if len(q.Files) == 0 || len(q.Files) > batchSize {
			return fmt.Errorf("worker stow: request %d holds %d files (batch size %d)", i+1, len(q.Files), batchSize)
		}
	}
	if next != len(files) {
		return fmt.Errorf("worker stow: %d of %d files sent", next, len(files))
	}
	return nil
}

// --- tls_reject over HTTP (DVT-SRS-105, -106) ---

// HTTP probe observations, besides the TLS ones.
const (
	ObsHTTPReply    = "http_reply"
	ObsNonHTTPReply = "non_http_reply"
)

var webProbeOps = map[string]string{OpConnect: "http", OpTLS: "tls", OpGet: "http"}

// TLSProbeWeb breaks the TLS policy on purpose against a DICOMweb target
// (plaintext to the https port, an old TLS version, no client
// certificate) with one bare GET: no token, no client certificate
// (DVT-SRS-106).
func (c Config) TLSProbeWeb(ctx context.Context, w Web, mode, version string) (ProbeResult, string, error) {
	var r ProbeResult
	args := []string{"tls-probe", "--base-url", w.BaseURL, "--timeout", strconv.FormatFloat(w.timeout().Seconds(), 'f', -1, 64)}
	t := w.TLS
	if t == nil {
		t = &TLS{}
	}
	args = append(args, t.trustArgs()...)
	args = append(args, "--mode", mode)
	if mode == "tls_version" {
		args = append(args, "--tls-version", version)
	}
	raw, err := c.workerCallStrict(ctx, w.processTimeout(1), &r, args...)
	if err != nil {
		return r, raw, err
	}
	switch {
	case r.Mode != mode:
		return r, raw, fmt.Errorf("worker tls-probe: result for mode %q, expected %q", r.Mode, mode)
	case !observations[r.Observation] && r.Observation != ObsHTTPReply && r.Observation != ObsNonHTTPReply:
		return r, raw, fmt.Errorf("worker tls-probe: unknown observation %q", r.Observation)
	case r.Detail == "":
		return r, raw, errors.New("worker tls-probe: observation without detail")
	}
	for i, x := range r.Exchanges {
		where := fmt.Sprintf("worker tls-probe: exchange %d", i+1)
		switch {
		case x.Seq != i+1 || webProbeOps[x.Operation] != x.Protocol:
			return r, raw, fmt.Errorf("%s: seq %d, operation %q, protocol %q", where, x.Seq, x.Operation, x.Protocol)
		case x.Outcome == result.ExchangeError && x.Error == nil:
			return r, raw, fmt.Errorf("%s: outcome error without a problem", where)
		case x.Endpoint == "":
			return r, raw, fmt.Errorf("%s: no endpoint", where)
		case x.TLS != nil && x.TLS.ClientCertSent:
			return r, raw, errors.New("worker tls-probe: a probe must not send a client certificate")
		case x.HTTP != nil && len(x.HTTP.RequestHeaders) > 0:
			return r, raw, errors.New("worker tls-probe: a probe sends no recorded headers (no Authorization)")
		case x.Operation == OpGet && x.HTTP == nil:
			return r, raw, fmt.Errorf("%s: GET without HTTP details", where)
		}
	}
	if r.Observation == ObsHTTPReply && ProbeHTTPStatus(r) == 0 {
		return r, raw, errors.New("worker tls-probe: HTTP reply without a status")
	}
	return r, raw, nil
}

// ProbeHTTPStatus is the HTTP status a probe received, or 0.
func ProbeHTTPStatus(r ProbeResult) int {
	for _, x := range r.Exchanges {
		if x.Operation == OpGet && x.HTTP != nil {
			return x.HTTP.Status
		}
	}
	return 0
}
