package dcmclient

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
)

// Licence and index backup (control-api-v1.md §4.3, §4.4; WT-SRS-027,
// -028). None of these calls can affect DICOM services: LibreDCM keeps its
// licence code out of the DIMSE gateway (DCM-SRS-058).

const (
	// LicenseActivateWait bounds the wait for an online activation.
	// LibreDCM gives the licence server 30 seconds; Libre WT waits a little
	// longer, so it sees LibreDCM's answer rather than its own timeout.
	LicenseActivateWait = 45 * time.Second
	// DefaultBackupTimeout bounds the wait for a backup. LibreDCM lets a
	// snapshot run for up to 10 minutes and finishes it when Libre WT stops
	// waiting; Libre WT stays below its own HTTP client timeout (2 minutes)
	// and the write timeout of its web server (3 minutes).
	DefaultBackupTimeout = 110 * time.Second
)

// KeyLast4 is what Libre WT ever shows of a licence key: its last four
// characters. A key of four characters or fewer shows nothing.
func KeyLast4(key string) string {
	key = strings.TrimSpace(key)
	if len(key) <= 4 {
		return ""
	}
	return key[len(key)-4:]
}

// last4 keeps at most the last four characters of a value LibreDCM reports
// as the end of a key, so a full key sent by mistake is never kept
// (WT-SRS-032).
func last4(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 4 {
		return s[len(s)-4:]
	}
	return s
}

// LicenseAck is the acknowledgement of the unlicensed notice on file.
type LicenseAck struct {
	By         string    `json:"by"`
	At         time.Time `json:"at"`
	State      string    `json:"license_state"`
	ValidUntil time.Time `json:"valid_until"`
	Valid      bool      `json:"valid"`
}

// LicenseDetail is the licence status of a node, as GET
// /control/v1/license returns it. It never holds the licence key: only its
// last four characters.
type LicenseDetail struct {
	Enforced    bool        `json:"enforced"`
	State       string      `json:"state"`
	Licensed    bool        `json:"licensed"`
	AckRequired bool        `json:"ack_required"`
	Message     string      `json:"message"`
	DaysLeft    int         `json:"days_left"`
	Fingerprint string      `json:"machine_fingerprint"`
	Customer    string      `json:"customer"`
	Site        string      `json:"site"`
	Term        string      `json:"term"`
	KeyLast4    string      `json:"key_last4"`
	Expires     *time.Time  `json:"expires"`
	Ack         *LicenseAck `json:"acknowledgement"`
	Note        string      `json:"note"`
}

func (d *LicenseDetail) clean() { d.KeyLast4 = last4(d.KeyLast4) }

// LicenseView is the answer of GET /control/v1/license.
type LicenseView struct {
	Instance InstanceInfo  `json:"instance"`
	License  LicenseDetail `json:"license"`
}

// License reads the licence status of the node.
func (c *Client) License(ctx context.Context) (LicenseView, error) {
	var v LicenseView
	err := c.get(ctx, "/control/v1/license", &v)
	v.License.clean()
	return v, err
}

// LicenseResult is the answer of a licence acknowledgement, installation or
// activation: the licence status after it.
type LicenseResult struct {
	Instance     InstanceInfo  `json:"instance"`
	Acknowledged bool          `json:"acknowledged"`
	Installed    bool          `json:"installed"`
	Activated    bool          `json:"activated"`
	License      LicenseDetail `json:"license"`
}

// LicenseRequestResult is the answer of POST /control/v1/license/request.
// Request holds the licence key inside its encoded block (the licence
// portal needs it): it is offered as a file and never shown or logged.
type LicenseRequestResult struct {
	Instance InstanceInfo `json:"instance"`
	Request  string       `json:"request"`
	KeyLast4 string       `json:"key_last4"`
	Note     string       `json:"note"`
}

// scrub removes every form of a licence key from LibreDCM's error text, in
// case a licence server repeats it (LibreDCM removes it too). The key is
// replaced by its masked form.
func scrub(err error, key string) error {
	key = strings.TrimSpace(key)
	var ae *APIError
	if key == "" || err == nil || !errors.As(err, &ae) {
		return err
	}
	masked := "••••" + KeyLast4(key)
	for _, form := range []string{key, strings.ToUpper(key), strings.ToLower(key)} {
		ae.Message = strings.ReplaceAll(ae.Message, form, masked)
	}
	return ae
}

func keyBody(key, site string) map[string]any {
	body := map[string]any{}
	if k := strings.TrimSpace(key); k != "" {
		body["key"] = k
	}
	if site != "" {
		body["site"] = site
	}
	return body
}

// LicenseAck records the acknowledgement of the unlicensed notice under the
// envelope's operator (the logged-in user).
func (c *Client) LicenseAck(ctx context.Context, env Envelope) (LicenseResult, error) {
	var r LicenseResult
	err := c.act(ctx, "/control/v1/license/ack", env.body(nil), &r)
	r.License.clean()
	return r, err
}

// LicenseRequest asks the node for an activation request text (offline
// activation). key and site are optional.
func (c *Client) LicenseRequest(ctx context.Context, key, site string, env Envelope) (LicenseRequestResult, error) {
	var r LicenseRequestResult
	err := c.act(ctx, "/control/v1/license/request", env.body(keyBody(key, site)), &r)
	r.KeyLast4 = last4(r.KeyLast4)
	return r, scrub(err, key)
}

// LicenseInstall sends a licence file. LibreDCM checks its signature,
// product, machine and dates; a refused file leaves the installed licence
// as it was.
func (c *Client) LicenseInstall(ctx context.Context, text string, env Envelope) (LicenseResult, error) {
	var r LicenseResult
	err := c.act(ctx, "/control/v1/license/install", env.body(map[string]any{"license": text}), &r)
	r.License.clean()
	return r, err
}

// LicenseActivate activates the node online with a licence key.
func (c *Client) LicenseActivate(ctx context.Context, key, site string, env Envelope) (LicenseResult, error) {
	var r LicenseResult
	ctx, cancel := context.WithTimeout(ctx, LicenseActivateWait)
	defer cancel()
	err := c.do(ctx, http.MethodPost, "/control/v1/license/activate", env.body(keyBody(key, site)), &r)
	r.License.clean()
	return r, scrub(err, key)
}

// BackupFile is one index snapshot in the node's backup directory.
type BackupFile struct {
	File  string    `json:"file"`
	Bytes int64     `json:"bytes"`
	Time  time.Time `json:"time"`
}

// BackupList is the answer of GET /control/v1/backups. Configured is false
// when the node has no backup directory or a PostgreSQL index; Reason then
// says why.
type BackupList struct {
	Instance   InstanceInfo `json:"instance"`
	Configured bool         `json:"configured"`
	Directory  string       `json:"directory"`
	Backups    []BackupFile `json:"backups"`
	Count      int          `json:"count"`
	More       bool         `json:"more"`
	Note       string       `json:"note"`
	Reason     string       `json:"reason"`
	Code       string       `json:"code"`
}

// Backups lists the index snapshots of the node.
func (c *Client) Backups(ctx context.Context) (BackupList, error) {
	var v BackupList
	err := c.get(ctx, "/control/v1/backups", &v)
	return v, err
}

// BackupResult is the answer of POST /control/v1/backup.
type BackupResult struct {
	Instance   InstanceInfo `json:"instance"`
	File       string       `json:"file"`
	Bytes      int64        `json:"bytes"`
	SHA256     string       `json:"sha256"`
	DurationMS int64        `json:"duration_ms"`
	Note       string       `json:"note"`
}

// Backup starts an online snapshot of the index. The request carries the
// envelope only: LibreDCM chooses the directory and the file name.
func (c *Client) Backup(ctx context.Context, env Envelope) (BackupResult, error) {
	var r BackupResult
	ctx, cancel := context.WithTimeout(ctx, c.backup)
	defer cancel()
	err := c.do(ctx, http.MethodPost, "/control/v1/backup", env.body(nil), &r)
	return r, err
}
