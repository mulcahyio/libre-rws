package dcmclient

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"time"
)

// Configuration view, C-MOVE destinations, calling-AE allow-list, C-ECHO
// test, reload, configuration edit and rollback (control-api-v1.md §3.2,
// §3.3, §4.1, §4.2, §5.3; WT-SRS-019…022, -024).

// DefaultActionTimeout bounds how long Libre WT waits for the answer to an
// action. The action itself is bounded by LibreDCM.
const DefaultActionTimeout = 30 * time.Second

// Code returns LibreDCM's machine code of a refusal, or "".
func Code(err error) string {
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.Code
	}
	return ""
}

// HTTPStatus returns the HTTP status of a refusal by LibreDCM, or 0 when the
// error is not an answer from LibreDCM (no connection, no answer in time).
func HTTPStatus(err error) int {
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.Status
	}
	return 0
}

// NoAnswer reports whether a request got no answer from LibreDCM: the
// connection failed or the time ran out. After an action, the outcome is
// then not known.
func NoAnswer(err error) bool {
	var ae *APIError
	return err != nil && !errors.As(err, &ae)
}

// NewRequestID makes the random ID an action carries (DCM-SRS-086): 32
// hexadecimal characters.
func NewRequestID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand doesn't fail on supported platforms
	}
	return hex.EncodeToString(b)
}

// Envelope is what every action carries: who asked, why, the request ID and
// the node Libre WT expects (WT-SRS-031).
type Envelope struct {
	Operator  string
	Reason    string
	RequestID string
	Expect    Identity
}

func (e Envelope) body(extra map[string]any) map[string]any {
	if extra == nil {
		extra = map[string]any{}
	}
	extra["operator"], extra["reason"], extra["request_id"] = e.Operator, e.Reason, e.RequestID
	return expect(extra, e.Expect)
}

// read runs a GET with the short read timeout.
func (c *Client) get(ctx context.Context, path string, out any) error {
	ctx, cancel := context.WithTimeout(ctx, c.read)
	defer cancel()
	return c.do(ctx, http.MethodGet, path, nil, out)
}

// plan runs a plan request (it changes nothing) with the read timeout.
func (c *Client) plan(ctx context.Context, path string, body map[string]any, out any) error {
	ctx, cancel := context.WithTimeout(ctx, c.read)
	defer cancel()
	return c.do(ctx, http.MethodPost, path, body, out)
}

// act runs an action with the action timeout.
func (c *Client) act(ctx context.Context, path string, body map[string]any, out any) error {
	ctx, cancel := context.WithTimeout(ctx, c.action)
	defer cancel()
	return c.do(ctx, http.MethodPost, path, body, out)
}

// Setting is one running setting of a node (DCM-SRS-082).
type Setting struct {
	Key           string   `json:"key"`
	Value         any      `json:"value"`
	Redacted      bool     `json:"redacted"`
	Set           *bool    `json:"set"`
	RedactedParts []string `json:"redacted_parts"`
	Source        string   `json:"source"`
	Env           string   `json:"env"`
	Reloadable    bool     `json:"reloadable"`
}

// ConfigView is the answer of GET /control/v1/config.
type ConfigView struct {
	Instance    InstanceInfo `json:"instance"`
	Settings    []Setting    `json:"settings"`
	RestartNote string       `json:"restart_required_note"`
}

// Config reads the settings the node runs with. LibreDCM redacts secrets;
// Libre WT also drops the value of every setting marked redacted, so a
// value sent by mistake is never kept (WT-SRS-032).
func (c *Client) Config(ctx context.Context) (ConfigView, error) {
	var v ConfigView
	err := c.get(ctx, "/control/v1/config", &v)
	for i := range v.Settings {
		if v.Settings[i].Redacted {
			v.Settings[i].Value = nil
		}
	}
	return v, err
}

// RemoteAE is one C-MOVE destination.
type RemoteAE struct {
	Name    string `json:"name"`
	Host    string `json:"host"`
	Port    int    `json:"port"`
	AETitle string `json:"ae_title"`
	Source  string `json:"source,omitempty"`
}

// RemoteAEList is the answer of GET /control/v1/remote-aes.
type RemoteAEList struct {
	Instance  InstanceInfo `json:"instance"`
	RemoteAEs []RemoteAE   `json:"remote_aes"`
}

// RemoteAEs reads the C-MOVE destinations in force.
func (c *Client) RemoteAEs(ctx context.Context) (RemoteAEList, error) {
	var v RemoteAEList
	err := c.get(ctx, "/control/v1/remote-aes", &v)
	return v, err
}

// AllowRule is one calling-AE allow-list rule in force.
type AllowRule struct {
	Rule   string `json:"rule"`
	Source string `json:"source"`
}

// AllowList is the answer of GET /control/v1/allow-list. Enforced false
// means the list is empty and every calling AE may connect. Invalid means
// the configured list was invalid and the node refuses every association.
type AllowList struct {
	Instance InstanceInfo `json:"instance"`
	Enforced bool         `json:"enforced"`
	Entries  []AllowRule  `json:"entries"`
	Invalid  bool         `json:"invalid"`
	Note     string       `json:"note"`
}

// AllowList reads the calling-AE allow-list in force.
func (c *Client) AllowList(ctx context.Context) (AllowList, error) {
	var v AllowList
	err := c.get(ctx, "/control/v1/allow-list", &v)
	return v, err
}

// PlanWarning is one warning of a plan. A warning with MustAcknowledge
// weakens a risk control: LibreDCM refuses the action unless its code is
// acknowledged (DCM-SRS-095).
type PlanWarning struct {
	Code            string `json:"code"`
	Message         string `json:"message"`
	MustAcknowledge bool   `json:"must_acknowledge"`
}

// RemoteAEChange is a destination whose address or AE title changes.
type RemoteAEChange struct {
	Name string   `json:"name"`
	From RemoteAE `json:"from"`
	To   RemoteAE `json:"to"`
}

// ChangePlan is LibreDCM's plan of a reload, a configuration edit or a
// rollback: the difference between the settings in force and the settings
// the action would put in force.
type ChangePlan struct {
	Changed   bool `json:"changed"`
	RemoteAEs struct {
		Added   []RemoteAE       `json:"added"`
		Removed []RemoteAE       `json:"removed"`
		Changed []RemoteAEChange `json:"changed"`
	} `json:"remote_aes"`
	AllowList struct {
		Added          []string `json:"added"`
		Removed        []string `json:"removed"`
		EnforcedBefore bool     `json:"enforced_before"`
		EnforcedAfter  bool     `json:"enforced_after"`
	} `json:"allow_list"`
	Warnings   []PlanWarning `json:"warnings"`
	PlanDigest string        `json:"plan_digest"`
}

// OverlayVersion describes one version of the settings made from a console.
type OverlayVersion struct {
	Time      string   `json:"time"`
	Operator  string   `json:"operator"`
	Reason    string   `json:"reason"`
	RequestID string   `json:"request_id"`
	Kind      string   `json:"kind"`
	Digest    string   `json:"digest"`
	Settings  []string `json:"settings"`
}

// ChangeResult is the answer of a reload, a configuration edit or a rollback.
type ChangeResult struct {
	Instance     InstanceInfo    `json:"instance"`
	Applied      bool            `json:"applied"`
	Changed      bool            `json:"changed"`
	Plan         ChangePlan      `json:"plan"`
	DigestBefore string          `json:"config_digest_before"`
	DigestAfter  string          `json:"config_digest_after"`
	Note         string          `json:"note"`
	Current      *OverlayVersion `json:"current"`
	Previous     *OverlayVersion `json:"previous"`
}

type planAnswer struct {
	Instance InstanceInfo    `json:"instance"`
	Plan     ChangePlan      `json:"plan"`
	Previous *OverlayVersion `json:"previous"`
}

// approved is the part of an apply request that says what the person
// approved: LibreDCM's digest of the plan they saw and the warnings they
// acknowledged.
func approved(digest string, acks []string) map[string]any {
	body := map[string]any{"confirm": true, "plan_digest": digest}
	if len(acks) > 0 {
		body["acknowledge_warnings"] = acks
	}
	return body
}

// ReloadPlan asks what a reload would change. Nothing changes.
func (c *Client) ReloadPlan(ctx context.Context, exp Identity) (ChangePlan, error) {
	var a planAnswer
	err := c.plan(ctx, "/control/v1/reload/plan", expect(map[string]any{}, exp), &a)
	return a.Plan, err
}

// Reload applies the reload the administrator approved. LibreDCM refuses it
// when its plan no longer has this digest.
func (c *Client) Reload(ctx context.Context, digest string, acks []string, env Envelope) (ChangeResult, error) {
	var r ChangeResult
	err := c.act(ctx, "/control/v1/reload", env.body(approved(digest, acks)), &r)
	return r, err
}

// ConfigEdit is the whole new value of one or both live settings. A nil
// setting is not changed.
type ConfigEdit struct {
	RemoteAEs  *[]RemoteAE
	AllowedAEs *[]string
}

// Setting keys of the two settings that can change without a restart.
const (
	KeyRemoteAEs  = "dicom.remote_aes"
	KeyAllowedAEs = "dicom.allowed_aes"
)

func (e ConfigEdit) settings() map[string]any {
	out := map[string]any{}
	if e.RemoteAEs != nil {
		list := make([]map[string]any, 0, len(*e.RemoteAEs))
		for _, r := range *e.RemoteAEs {
			list = append(list, map[string]any{"name": r.Name, "host": r.Host, "port": r.Port, "ae_title": r.AETitle})
		}
		out[KeyRemoteAEs] = list
	}
	if e.AllowedAEs != nil {
		out[KeyAllowedAEs] = append([]string{}, *e.AllowedAEs...)
	}
	return out
}

// ConfigPlan asks what an edit would change. Nothing changes.
func (c *Client) ConfigPlan(ctx context.Context, edit ConfigEdit, exp Identity) (ChangePlan, error) {
	var a planAnswer
	err := c.plan(ctx, "/control/v1/config/plan", expect(map[string]any{"settings": edit.settings()}, exp), &a)
	return a.Plan, err
}

// ConfigApply applies the edit the administrator approved.
func (c *Client) ConfigApply(ctx context.Context, edit ConfigEdit, digest string, acks []string, env Envelope) (ChangeResult, error) {
	var r ChangeResult
	body := approved(digest, acks)
	body["settings"] = edit.settings()
	err := c.act(ctx, "/control/v1/config", env.body(body), &r)
	return r, err
}

// RollbackPlan asks what putting the previous console-made version back
// would change, and which version that is. Nothing changes.
func (c *Client) RollbackPlan(ctx context.Context, exp Identity) (ChangePlan, *OverlayVersion, error) {
	var a planAnswer
	err := c.plan(ctx, "/control/v1/config/rollback/plan", expect(map[string]any{}, exp), &a)
	return a.Plan, a.Previous, err
}

// Rollback applies the rollback the administrator approved.
func (c *Client) Rollback(ctx context.Context, digest string, acks []string, env Envelope) (ChangeResult, error) {
	var r ChangeResult
	err := c.act(ctx, "/control/v1/config/rollback", env.body(approved(digest, acks)), &r)
	return r, err
}

// ConfigHistory is the answer of GET /control/v1/config/history.
type ConfigHistory struct {
	Instance    InstanceInfo    `json:"instance"`
	Enabled     bool            `json:"enabled"`
	Current     *OverlayVersion `json:"current"`
	Previous    *OverlayVersion `json:"previous"`
	FromOverlay []string        `json:"in_force_from_overlay"`
	Note        string          `json:"note"`
}

// ConfigHistory reads the current and the previous console-made version.
func (c *Client) ConfigHistory(ctx context.Context) (ConfigHistory, error) {
	var v ConfigHistory
	err := c.get(ctx, "/control/v1/config/history", &v)
	return v, err
}

// EchoTarget is where a C-ECHO test goes: a configured destination by name
// (RemoteAE), or a free host, port and AE title.
type EchoTarget struct {
	RemoteAE string
	Host     string
	Port     int
	AETitle  string
	TLS      bool
}

// EchoResult is the answer of POST /control/v1/echo. A test that ran and
// failed has OK false, the stage that failed and a short fixed error text.
type EchoResult struct {
	Instance    InstanceInfo `json:"instance"`
	OK          bool         `json:"ok"`
	DurationMS  int64        `json:"duration_ms"`
	Target      string       `json:"target"`
	CalledAE    string       `json:"called_ae"`
	CallingAE   string       `json:"calling_ae"`
	TLS         bool         `json:"tls"`
	TLSVerified *bool        `json:"tls_verified"`
	Source      string       `json:"source"`
	Stage       string       `json:"stage"`
	Error       string       `json:"error"`
	Note        string       `json:"note"`
}

// Echo runs a C-ECHO test from the node to the target.
func (c *Client) Echo(ctx context.Context, t EchoTarget, env Envelope) (EchoResult, error) {
	body := map[string]any{}
	if t.RemoteAE != "" {
		body["remote_ae"] = t.RemoteAE
	} else {
		body["host"], body["port"], body["ae_title"] = t.Host, t.Port, t.AETitle
		if t.TLS {
			body["tls"] = true
		}
	}
	var r EchoResult
	err := c.act(ctx, "/control/v1/echo", env.body(body), &r)
	return r, err
}
