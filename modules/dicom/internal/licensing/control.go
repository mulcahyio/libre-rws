package licensing

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/mulcahyio/librekey/client"
)

// Licence functions for the control API (DCM-SRS-089). They are the same
// operations as the -license-* command-line flags, on the running node.
// None of them is reachable from the DIMSE gateway, and none of them can
// stop, delay or refuse DICOM services (DCM-SRS-058).

// ErrNoManager is returned when the node has no licence manager to act on.
var ErrNoManager = errors.New("licensing is not active on this node")

// AckView is the acknowledgement on file.
type AckView struct {
	By         string    `json:"by"`
	At         time.Time `json:"at"`
	State      string    `json:"license_state"`
	ValidUntil time.Time `json:"valid_until"`
	Valid      bool      `json:"valid"`
}

// Detail is the licence status shown to an administrator. It never holds
// the licence key: only its last four characters.
type Detail struct {
	Enforced    bool       `json:"enforced"`
	State       string     `json:"state"`
	Licensed    bool       `json:"licensed"`
	AckRequired bool       `json:"ack_required"`
	Message     string     `json:"message"`
	DaysLeft    int        `json:"days_left"`
	Fingerprint string     `json:"machine_fingerprint,omitempty"`
	Customer    string     `json:"customer,omitempty"`
	Site        string     `json:"site,omitempty"`
	Term        string     `json:"term,omitempty"`
	KeyLast4    string     `json:"key_last4,omitempty"`
	Expires     *time.Time `json:"expires,omitempty"`
	Ack         *AckView   `json:"acknowledgement,omitempty"`
	Note        string     `json:"note"`
}

// dicomNote is repeated in every status: licensing never stops DICOM.
const dicomNote = "DICOM services run in every licence state. Licensing never stops them."

// KeyLast4 returns the last four characters of a licence key, for display.
// A key of four characters or fewer is not shown at all.
func KeyLast4(key string) string {
	key = strings.TrimSpace(key)
	if len(key) <= 4 {
		return ""
	}
	return key[len(key)-4:]
}

// Detail reports the licence status of the running node.
func (l *Licensing) Detail() Detail {
	if l == nil {
		return Detail{Enforced: false, Note: "Source build: no licence is needed."}
	}
	return l.detail(l.status())
}

func (l *Licensing) detail(st client.Status) Detail {
	now := l.now()
	d := Detail{Enforced: true, State: string(st.State), Licensed: Licensed(st), Message: st.Message,
		DaysLeft: st.DaysLeft, Fingerprint: l.fingerprint(), Expires: st.Expires, Note: dicomNote,
		AckRequired: l.ackRequiredAt(st, now)}
	if st.License != nil {
		d.Customer, d.Site, d.Term, d.KeyLast4 = st.License.Customer, st.License.Site, st.License.Term, KeyLast4(st.License.Key)
	}
	if a, err := readAck(l.ackFile); err == nil && !a.At.IsZero() {
		d.Ack = &AckView{By: a.By, At: a.At, State: a.State, ValidUntil: a.At.Add(AckValidity), Valid: a.ValidAt(now)}
	}
	return d
}

// AcknowledgeBy records the acknowledgement of the named administrator and
// reports it at once (audit record, metrics), as -license-ack does within a
// minute.
func (l *Licensing) AcknowledgeBy(by string) (Detail, error) {
	if _, err := l.Acknowledge(by); err != nil {
		return l.Detail(), err
	}
	l.tick()
	return l.Detail(), nil
}

// RequestText returns an activation request for this machine, for offline
// activation. The text holds the key it was given: that is its purpose.
func (l *Licensing) RequestText(key, site string) (string, error) {
	if l.mgr == nil {
		return "", ErrNoManager
	}
	return l.mgr.Request(key, site).Encode(), nil
}

// InstallText verifies a licence file given as text and, when it is usable
// on this machine, installs it. An invalid, expired or wrong-machine licence
// is refused and the installed licence stays (the manager writes nothing).
func (l *Licensing) InstallText(text string) (Detail, error) {
	if l.mgr == nil {
		return l.Detail(), ErrNoManager
	}
	l.opMu.Lock()
	defer l.opMu.Unlock()
	if _, err := l.mgr.Install(text); err != nil {
		return l.Detail(), err
	}
	l.tick()
	return l.Detail(), nil
}

// ActivateOnline activates this machine with a licence key at the LibreKey
// server and installs the licence it returns.
func (l *Licensing) ActivateOnline(ctx context.Context, key, site string) (Detail, error) {
	if l.mgr == nil {
		return l.Detail(), ErrNoManager
	}
	l.opMu.Lock()
	defer l.opMu.Unlock()
	if _, err := l.mgr.Activate(ctx, key, site); err != nil {
		return l.Detail(), err
	}
	l.tick()
	return l.Detail(), nil
}
