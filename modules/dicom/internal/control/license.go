package control

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/mulcahyio/libre-rws/modules/dicom/internal/licensing"
)

// Licence endpoints (control-api-v1.md §4.3, DCM-SRS-089). They call the
// node's licensing code, the same code as the -license-* flags. This file
// and the licensing package are outside the DIMSE gateway, which cannot
// import either (TestGatewayHasNoLicenseDependency, DCM-SRS-058): nothing
// here can refuse, delay or interrupt a DICOM service.

const (
	// LicenseActivateTimeout bounds an online activation.
	LicenseActivateTimeout = 30 * time.Second
	// licenseBodyLimit is the body limit of licence install (a licence file).
	licenseBodyLimit = 256 << 10
	maxLicenseKeyLen = 128
	maxLicenseSite   = 200
)

// Licence is the node's licensing, as the control API uses it. It is
// implemented by *licensing.Licensing. A nil Licence means a source build:
// no licence is needed.
type Licence interface {
	Detail() licensing.Detail
	AcknowledgeBy(by string) (licensing.Detail, error)
	RequestText(key, site string) (string, error)
	InstallText(text string) (licensing.Detail, error)
	ActivateOnline(ctx context.Context, key, site string) (licensing.Detail, error)
}

// LicenseAckRequest is the body of POST /control/v1/license/ack. The
// acknowledgement is recorded under the envelope's operator.
type LicenseAckRequest struct {
	Envelope
}

// LicenseKeyRequest is the body of POST /control/v1/license/request and
// .../license/activate.
type LicenseKeyRequest struct {
	Envelope
	Key  string `json:"key,omitempty"`
	Site string `json:"site,omitempty"`
}

// LicenseInstallRequest is the body of POST /control/v1/license/install.
type LicenseInstallRequest struct {
	Envelope
	License string `json:"license"`
}

func (a *API) licenseStatus(w http.ResponseWriter, _ *http.Request) {
	d := licensing.Detail{Enforced: false, Note: "Source build: no licence is needed."}
	if a.Licence != nil {
		d = a.Licence.Detail()
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"instance": a.instance(), "license": d})
}

// notEnforced answers a licence action on a node that needs no licence.
func notEnforced() actionResult {
	return refusal(http.StatusUnprocessableEntity, "license_not_enforced",
		"this is a source build: no licence is needed and there is nothing to change", true)
}

// keySecrets lists the forms of a licence key that must not appear in a
// response.
func keySecrets(key string) []string {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil
	}
	return []string{key, strings.ToUpper(key), strings.ToLower(key)}
}

func (a *API) licenseAck(_ context.Context, req *LicenseAckRequest) actionResult {
	if a.Licence == nil {
		return notEnforced()
	}
	before := a.Licence.Detail()
	d, err := a.Licence.AcknowledgeBy(req.Operator)
	if err != nil {
		return refusal(http.StatusInternalServerError, "ack_failed",
			"the acknowledgement could not be recorded", false, "license_action", "ack", "state_before", before.State)
	}
	return actionResult{status: http.StatusOK, outcome: outcomeSuccess,
		body:  map[string]any{"acknowledged": true, "license": d},
		attrs: []any{"license_action", "ack", "state_before", before.State, "state_after", d.State, "acknowledged_by", req.Operator}}
}

func checkKeyRequest(req *LicenseKeyRequest, keyRequired bool) (actionResult, bool) {
	req.Key, req.Site = strings.TrimSpace(req.Key), cleanText(req.Site)
	switch {
	case keyRequired && req.Key == "":
		return refusal(http.StatusUnprocessableEntity, "license_key_required", "key is required", true), false
	case len(req.Key) > maxLicenseKeyLen || strings.ContainsAny(req.Key, " \t\r\n"):
		return refusal(http.StatusUnprocessableEntity, "license_key_invalid", "key is not a licence key", true), false
	case len(req.Site) > maxLicenseSite:
		return refusal(http.StatusUnprocessableEntity, "license_site_invalid", "site is longer than 200 characters", true), false
	}
	return actionResult{}, true
}

func (a *API) licenseRequest(_ context.Context, req *LicenseKeyRequest) actionResult {
	if a.Licence == nil {
		return notEnforced()
	}
	if res, ok := checkKeyRequest(req, false); !ok {
		return res
	}
	state := a.Licence.Detail().State
	text, err := a.Licence.RequestText(req.Key, req.Site)
	if err != nil {
		return refusal(http.StatusInternalServerError, "request_failed",
			"the activation request could not be made", false, "license_action", "request", "state_before", state)
	}
	// The request text carries the key inside its encoded block: that is
	// what the licence portal needs. The key itself is not echoed.
	return actionResult{status: http.StatusOK, outcome: outcomeSuccess, secrets: keySecrets(req.Key),
		body: map[string]any{"request": text, "key_last4": licensing.KeyLast4(req.Key),
			"note": "Send this text to the licence portal, then install the licence file it returns."},
		attrs: []any{"license_action", "request", "state_before", state, "state_after", state,
			"key_last4", licensing.KeyLast4(req.Key)}}
}

func (a *API) licenseInstall(_ context.Context, req *LicenseInstallRequest) actionResult {
	if a.Licence == nil {
		return notEnforced()
	}
	if strings.TrimSpace(req.License) == "" {
		return refusal(http.StatusUnprocessableEntity, "license_required", "license is required: the text of the licence file", true)
	}
	before := a.Licence.Detail()
	d, err := a.Licence.InstallText(req.License)
	if err != nil {
		// Refused by the signature, product, machine or date check; the
		// installed licence is unchanged. The reason comes from the licence
		// check and names no key.
		res := refusal(http.StatusUnprocessableEntity, "license_invalid",
			"licence refused; the installed licence is unchanged: "+clip(err.Error(), 300), false,
			"license_action", "install", "state_before", before.State, "state_after", d.State,
			"error", clip(err.Error(), 300))
		res.body["license"] = d
		res.secrets = []string{strings.TrimSpace(req.License)}
		return res
	}
	return actionResult{status: http.StatusOK, outcome: outcomeSuccess,
		body: map[string]any{"installed": true, "license": d},
		// The licence file content is never returned (DCM-SRS-082).
		secrets: []string{strings.TrimSpace(req.License)},
		attrs: []any{"license_action", "install", "state_before", before.State, "state_after", d.State,
			"key_last4", d.KeyLast4, "customer", d.Customer}}
}

func (a *API) licenseActivate(ctx context.Context, req *LicenseKeyRequest) actionResult {
	if a.Licence == nil {
		return notEnforced()
	}
	if res, ok := checkKeyRequest(req, true); !ok {
		return res
	}
	before := a.Licence.Detail()
	d, err := a.Licence.ActivateOnline(ctx, req.Key, req.Site)
	if err != nil {
		res := refusal(http.StatusUnprocessableEntity, "activation_failed",
			"activation failed; the installed licence is unchanged: "+clip(err.Error(), 300), false,
			"license_action", "activate", "state_before", before.State, "state_after", d.State,
			"key_last4", licensing.KeyLast4(req.Key),
			"error", scrubString(clip(err.Error(), 300), keySecrets(req.Key)))
		res.body["license"] = d
		res.secrets = keySecrets(req.Key)
		return res
	}
	return actionResult{status: http.StatusOK, outcome: outcomeSuccess, secrets: keySecrets(req.Key),
		body: map[string]any{"activated": true, "license": d},
		attrs: []any{"license_action", "activate", "state_before", before.State, "state_after", d.State,
			"key_last4", d.KeyLast4, "customer", d.Customer}}
}
