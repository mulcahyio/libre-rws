// Package request defines the activation request a product creates on the
// machine to be licensed. Online it is sent to the LibreKey server; offline
// (air-gapped sites) it is saved as a file and uploaded in the customer portal.
package request

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/mulcahyio/librekey/fingerprint"
)

const (
	armorBegin = "-----BEGIN LIBREKEY REQUEST-----"
	armorEnd   = "-----END LIBREKEY REQUEST-----"
)

// Request describes the machine and product asking for a license.
type Request struct {
	Version     int       `json:"v"`
	ID          string    `json:"id"`
	Product     string    `json:"product"`
	Key         string    `json:"key,omitempty"` // license key, if the customer already has one
	Fingerprint string    `json:"fingerprint"`
	Hostname    string    `json:"hostname"`
	OS          string    `json:"os"`
	AppVersion  string    `json:"app_version,omitempty"`
	Site        string    `json:"site,omitempty"`
	Created     time.Time `json:"created"`
}

// New builds a request for this machine.
func New(product, appVersion, key, fp string) Request {
	h, _ := os.Hostname()
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return Request{Version: 1, ID: hex.EncodeToString(b), Product: product, Key: key, Fingerprint: fp,
		Hostname: h, OS: runtime.GOOS + "/" + runtime.GOARCH, AppVersion: appVersion, Created: time.Now().UTC()}
}

// Validate checks required fields.
func (r Request) Validate() error {
	if r.Product == "" {
		return errors.New("request: product is required")
	}
	if !fingerprint.Valid(r.Fingerprint) {
		return errors.New("request: machine fingerprint is missing or invalid")
	}
	return nil
}

// Encode returns the request as an armored text block.
func (r Request) Encode() string {
	raw, _ := json.Marshal(r)
	enc := base64.StdEncoding.EncodeToString(raw)
	var b strings.Builder
	b.WriteString(armorBegin + "\n")
	for len(enc) > 64 {
		b.WriteString(enc[:64] + "\n")
		enc = enc[64:]
	}
	b.WriteString(enc + "\n" + armorEnd + "\n")
	return b.String()
}

// Decode parses an armored request (or plain JSON).
func Decode(text string) (Request, error) {
	var r Request
	text = strings.TrimSpace(text)
	var raw []byte
	if strings.HasPrefix(text, "{") {
		raw = []byte(text)
	} else {
		i, j := strings.Index(text, armorBegin), strings.Index(text, armorEnd)
		if i < 0 || j < i {
			return r, errors.New("request: not a LibreKey request")
		}
		var err error
		raw, err = base64.StdEncoding.DecodeString(strings.Join(strings.Fields(text[i+len(armorBegin):j]), ""))
		if err != nil {
			return r, errors.New("request: damaged request (base64)")
		}
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return r, errors.New("request: damaged request (json)")
	}
	return r, r.Validate()
}
