package control

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// Action endpoints (control-api-v1.md §2.3 and §4). Every action goes
// through one runner, so the rules of DCM-SRS-086, -096 and -097 hold for
// each of them in the same way:
//
//  1. the body is decoded strictly (JSON, unknown fields refused, size limit);
//  2. operator, reason and request ID are required;
//  3. an expected node ID or AE title that does not match refuses the request;
//  4. a request ID seen in the last 10 minutes returns the first result and
//     does nothing again;
//  5. a limit on how many actions of one kind run at once;
//  6. an audit record is written before the action starts; when it cannot
//     be written the action is not done;
//  7. the action runs under a timeout;
//  8. a second audit record carries the outcome.

// Limits of the action envelope.
const (
	maxOperatorLen  = 128
	maxReasonLen    = 500
	minRequestIDLen = 16
	maxRequestIDLen = 64

	defaultBodyLimit = 64 << 10
	// IdempotencyWindow is how long a request ID is remembered.
	IdempotencyWindow = 10 * time.Minute
	// maxRemembered bounds the request IDs held in memory (DCM-SRS-096).
	// When the table is full of results that are still inside the window,
	// new actions are refused as busy; nothing is forgotten early.
	maxRemembered = 1000
)

// Audit event names written by the control API (control-api-v1.md §6).
const (
	auditRefused      = "SecurityAlert.control-refused"
	auditVerification = "NetworkEntry.verification"
	auditConfigChange = "SecurityAlert.configuration-changed"
	auditLicense      = "SecurityAlert.license-action"
	auditBackup       = "SecurityAlert.backup"
)

// Audit outcomes, as in the gateway's audit records.
const (
	outcomeSuccess = "success"
	outcomeMinor   = "minor-failure"
	outcomeSerious = "serious-failure"
	outcomePending = "pending" // the record written before the action starts
)

var reRequestID = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// Envelope is carried by every action request (DCM-SRS-086, -097).
type Envelope struct {
	Operator      string `json:"operator"`
	Reason        string `json:"reason"`
	RequestID     string `json:"request_id"`
	ExpectNodeID  string `json:"expect_node_id,omitempty"`
	ExpectAETitle string `json:"expect_ae_title,omitempty"`
}

func (e *Envelope) envelope() *Envelope { return e }

// enveloped is implemented by every action request type (by embedding Envelope).
type enveloped interface{ envelope() *Envelope }

// actionSpec describes one action endpoint.
type actionSpec struct {
	name      string        // short name: "echo", "backup", "license/install"
	event     string        // audit event of the action
	bodyLimit int64         // 0: defaultBodyLimit
	timeout   time.Duration // the action is cancelled when it expires
	// slots limits how many actions of this kind run at once; nil: no limit.
	slots chan struct{}
	// forget: the response is not kept for replay (it holds a value that
	// must not stay in memory). A repeated request ID then runs again; only
	// actions that change nothing may set it.
	forget bool
}

// actionResult is what an action hands back to the runner.
type actionResult struct {
	status  int
	body    map[string]any
	outcome string // audit outcome
	event   string // audit event override ("" = the spec's event)
	attrs   []any  // audit fields of the action (never a secret)
	// secrets are values of this request that must not appear in the
	// response (a licence key), on top of the node's own secrets.
	secrets []string
}

// refusal builds an error result. Its audit record is a refusal record when
// refused is true (the request was not allowed), else a failure of the action.
func refusal(status int, code, msg string, refused bool, attrs ...any) actionResult {
	res := actionResult{status: status, body: map[string]any{"error": msg, "code": code},
		outcome: outcomeMinor, attrs: append([]any{"code", code}, attrs...)}
	if refused {
		res.event = auditRefused
	}
	return res
}

// remembered is one request ID in the idempotency table.
type remembered struct {
	fingerprint string // endpoint and body of the first request
	at          time.Time
	done        bool
	status      int
	body        []byte
}

// idempotency remembers the result of each request ID for IdempotencyWindow.
type idempotency struct {
	mu  sync.Mutex
	m   map[string]*remembered
	now func() time.Time
}

// errors of idempotency.begin
var (
	errReplay     = errors.New("replay")
	errInProgress = errors.New("in progress")
	errReused     = errors.New("request id reused")
	errTableFull  = errors.New("table full")
)

// begin reserves id. It returns errReplay with the stored entry when the
// same request finished before, errInProgress when it is still running,
// errReused when the ID was used for another request, errTableFull when no
// more IDs can be remembered.
func (t *idempotency) begin(id, fingerprint string) (*remembered, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.clock()
	if t.m == nil {
		t.m = map[string]*remembered{}
	}
	for k, e := range t.m {
		if e.done && now.Sub(e.at) >= IdempotencyWindow {
			delete(t.m, k)
		}
	}
	if e, ok := t.m[id]; ok {
		switch {
		case e.fingerprint != fingerprint:
			return nil, errReused
		case !e.done:
			return nil, errInProgress
		default:
			return e, errReplay
		}
	}
	if len(t.m) >= maxRemembered {
		return nil, errTableFull
	}
	t.m[id] = &remembered{fingerprint: fingerprint, at: now}
	return nil, nil
}

// finish stores the result of id.
func (t *idempotency) finish(id string, status int, body []byte) {
	t.mu.Lock()
	if e, ok := t.m[id]; ok {
		e.done, e.status, e.body, e.at = true, status, body, t.clock()
	}
	t.mu.Unlock()
}

// forget drops id, so that the same request may be sent again.
func (t *idempotency) forget(id string) {
	t.mu.Lock()
	delete(t.m, id)
	t.mu.Unlock()
}

func (t *idempotency) clock() time.Time {
	if t.now != nil {
		return t.now()
	}
	return time.Now()
}

// errNoAuditLog: the API was built without a logger, so no audit record can
// be written and every action is refused (fail closed).
var errNoAuditLog = errors.New("no audit log configured")

// writeAudit writes one audit record through the node's log handler and
// returns the handler's error. It does not go through the level check: an
// action record is written also when the log level is warn or error.
func (a *API) writeAudit(ctx context.Context, event, outcome string, attrs ...any) error {
	if a.AuditLog == nil {
		return errNoAuditLog
	}
	rec := slog.NewRecord(time.Now(), slog.LevelInfo, "audit", 0)
	rec.AddAttrs(slog.Bool("audit", true), slog.String("event", event), slog.String("outcome", outcome),
		slog.String("component", "control"))
	for i := 0; i+1 < len(attrs); i += 2 {
		k, _ := attrs[i].(string)
		if k == "" {
			continue
		}
		v := attrs[i+1]
		if s, ok := v.(string); ok {
			// An error text or a name may carry a secret of the node: the
			// audit record goes to the log, so it is filtered like a response.
			v = scrubString(s, longSecrets(a.Secrets))
		}
		rec.AddAttrs(slog.Any(k, v))
	}
	return a.AuditLog.Handler().Handle(ctx, rec)
}

// longSecrets drops the values that are too short to replace by value
// (minSecretLen), as scrub does.
func longSecrets(secrets []string) []string {
	var use []string
	for _, s := range secrets {
		if len(s) >= minSecretLen {
			use = append(use, s)
		}
	}
	return use
}

// cleanText trims s and replaces control characters by spaces, so a name or
// reason cannot break a log line apart.
func cleanText(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	return strings.TrimSpace(s)
}

// clip shortens s for an audit record of a refused request.
func clip(s string, n int) string {
	s = cleanText(s)
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}

// checkEnvelope validates and normalises the envelope. code is "" when it is
// acceptable.
func checkEnvelope(e *Envelope) (code, msg string) {
	e.Operator, e.Reason = cleanText(e.Operator), cleanText(e.Reason)
	switch {
	case e.Operator == "":
		return "operator_required", "operator is required: the name of the person who asked for this action"
	case utf8.RuneCountInString(e.Operator) > maxOperatorLen:
		return "operator_invalid", "operator is longer than 128 characters"
	case e.Reason == "":
		return "reason_required", "reason is required"
	case utf8.RuneCountInString(e.Reason) > maxReasonLen:
		return "reason_invalid", "reason is longer than 500 characters"
	case len(e.RequestID) < minRequestIDLen || len(e.RequestID) > maxRequestIDLen || !reRequestID.MatchString(e.RequestID):
		return "request_id_invalid", "request_id is required: 16 to 64 characters from A-Z, a-z, 0-9, '.', '_' and '-'"
	}
	return "", ""
}

// decodeStrict reads one JSON object of at most limit bytes into v. Unknown
// fields, a longer body and anything after the object are refused.
func decodeStrict(r *http.Request, limit int64, v any) error {
	raw, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil {
		return err
	}
	if int64(len(raw)) > limit {
		return errors.New("body too large")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("unexpected data after the JSON object")
	}
	return nil
}

// respond writes an action response and returns the bytes sent. The body
// gets the instance block and passes the secret filter with the node's
// secrets and the request's own.
func (a *API) respond(w http.ResponseWriter, status int, body map[string]any, extra []string) (int, []byte) {
	if body == nil {
		body = map[string]any{}
	}
	body["instance"] = a.instance()
	raw, err := json.Marshal(body)
	var out []byte
	ok := err == nil
	if ok {
		out, ok = scrub(raw, append(append([]string(nil), a.Secrets...), extra...))
	}
	if !ok {
		status, out = http.StatusInternalServerError, []byte(`{"error":"internal error","code":"internal"}`+"\n")
	}
	a.sendRaw(w, status, out, false)
	return status, out
}

func (a *API) sendRaw(w http.ResponseWriter, status int, body []byte, replay bool) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if replay {
		w.Header().Set("Idempotent-Replay", "true")
	}
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// action builds the handler of one action endpoint. T is the request type;
// *T embeds Envelope.
func action[T any, P interface {
	*T
	enveloped
}](a *API, spec actionSpec, run func(ctx context.Context, req *T) actionResult) http.HandlerFunc {
	limit := spec.bodyLimit
	if limit == 0 {
		limit = defaultBodyLimit
	}
	endpoint := "/control/v1/" + spec.name
	return func(w http.ResponseWriter, r *http.Request) {
		// The action outlives a client that goes away: its result is kept
		// for the repeated request.
		ctx := context.WithoutCancel(r.Context())
		req := new(T)
		env := P(req).envelope()

		// refuse answers a request that is not allowed and records it.
		refuse := func(status int, code, msg string) {
			_ = a.writeAudit(ctx, auditRefused, outcomeMinor, "endpoint", endpoint, "code", code,
				"operator", clip(env.Operator, maxOperatorLen), "reason", clip(env.Reason, maxReasonLen),
				"request_id", clip(env.RequestID, maxRequestIDLen))
			a.respond(w, status, map[string]any{"error": msg, "code": code}, nil)
		}

		if err := decodeStrict(r, limit, req); err != nil {
			*env = Envelope{} // nothing of a malformed body is recorded
			refuse(http.StatusBadRequest, "invalid_body", "invalid JSON body (unknown field, wrong type or too large)")
			return
		}
		if code, msg := checkEnvelope(env); code != "" {
			refuse(http.StatusUnprocessableEntity, code, msg)
			return
		}
		if (env.ExpectNodeID != "" && env.ExpectNodeID != a.Node.ID) ||
			(env.ExpectAETitle != "" && env.ExpectAETitle != a.Node.AETitle) {
			refuse(http.StatusConflict, "instance_mismatch",
				"this node is not the one the request names; nothing was changed")
			return
		}

		// The fingerprint covers the endpoint and the whole request, so a
		// request ID cannot be reused for something else.
		canon, _ := json.Marshal(req)
		sum := sha256.Sum256(append([]byte(endpoint+"\n"), canon...))
		prev, err := a.requests.begin(env.RequestID, hex.EncodeToString(sum[:]))
		switch {
		case errors.Is(err, errReplay):
			a.sendRaw(w, prev.status, prev.body, true)
			return
		case errors.Is(err, errInProgress):
			a.respond(w, http.StatusConflict, map[string]any{"code": "request_in_progress",
				"error": "this request is still running; send it again later to get its result"}, nil)
			return
		case errors.Is(err, errReused):
			refuse(http.StatusConflict, "request_id_reused", "this request_id was used for another request")
			return
		case err != nil:
			refuse(http.StatusTooManyRequests, "busy", "too many recent actions; try again later")
			return
		}

		if spec.slots != nil {
			select {
			case spec.slots <- struct{}{}:
				defer func() { <-spec.slots }()
			default:
				a.requests.forget(env.RequestID)
				refuse(http.StatusTooManyRequests, "busy", "another "+spec.name+" is running; try again when it has finished")
				return
			}
		}

		common := []any{"endpoint", endpoint, "operator", env.Operator, "reason", env.Reason, "request_id", env.RequestID}
		if err := a.writeAudit(ctx, spec.event, outcomePending, append([]any{"phase", "start"}, common...)...); err != nil {
			// No audit record, no action (DCM-SRS-086).
			a.requests.forget(env.RequestID)
			a.respond(w, http.StatusInternalServerError, map[string]any{"code": "audit_unavailable",
				"error": "the audit record could not be written; the action was not done"}, nil)
			return
		}

		runCtx, cancel := context.WithTimeout(ctx, spec.timeout)
		res := guarded(run, runCtx, req)
		cancel()

		event := res.event
		if event == "" {
			event = spec.event
		}
		attrs := append([]any{"phase", "result", "action", spec.name}, common...)
		auditErr := a.writeAudit(ctx, event, res.outcome, append(attrs, res.attrs...)...)
		if auditErr != nil && res.body != nil {
			// The action is done and cannot be undone; say so.
			res.body["audit_warning"] = "the result audit record could not be written; the start record was"
		}
		status, sent := a.respond(w, res.status, res.body, res.secrets)
		if spec.forget {
			a.requests.forget(env.RequestID)
		} else {
			a.requests.finish(env.RequestID, status, sent)
		}
	}
}

// guarded runs an action and turns a panic into a failed result. Without
// it the request ID would stay "in progress" for good, the outcome record
// would be missing, and a repeat could not tell that the action may have
// started (DCM-SRS-086, -096). The result is remembered like any other: the
// same request ID does not run the action again.
func guarded[T any](run func(context.Context, *T) actionResult, ctx context.Context, req *T) (res actionResult) {
	defer func() {
		if r := recover(); r != nil {
			res = refusal(http.StatusInternalServerError, "internal",
				"the action failed unexpectedly and may be incomplete; check the node's log before trying again", false,
				"panic", true, "error", clip(fmt.Sprint(r), 300))
			res.outcome = outcomeSerious
		}
	}()
	return run(ctx, req)
}

// preview builds the handler of a plan endpoint: it changes nothing, so it
// needs no operator, reason or request ID and writes no audit record. It
// accepts the envelope fields (a client may send one body shape) and checks
// the expected identity.
func preview[T any, P interface {
	*T
	enveloped
}](a *API, run func(ctx context.Context, req *T) (int, map[string]any)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		req := new(T)
		if err := decodeStrict(r, defaultBodyLimit, req); err != nil {
			a.respond(w, http.StatusBadRequest, map[string]any{"code": "invalid_body",
				"error": "invalid JSON body (unknown field, wrong type or too large)"}, nil)
			return
		}
		env := P(req).envelope()
		if (env.ExpectNodeID != "" && env.ExpectNodeID != a.Node.ID) ||
			(env.ExpectAETitle != "" && env.ExpectAETitle != a.Node.AETitle) {
			a.respond(w, http.StatusConflict, map[string]any{"code": "instance_mismatch",
				"error": "this node is not the one the request names"}, nil)
			return
		}
		status, body := run(r.Context(), req)
		a.respond(w, status, body, nil)
	}
}
