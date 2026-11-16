package web

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/mulcahyio/libre-rws/modules/wt/internal/dcmclient"
)

// The licence page of one LibreDCM instance and the four licence actions:
// acknowledge the unlicensed notice, make an activation request file,
// install a licence file and activate online (WT-SRS-027, -031, -032).
//
// Nothing here can stop, delay or refuse a DICOM service: LibreDCM keeps
// its licence code out of the DIMSE gateway (DCM-SRS-058, HAZ-010), and
// every page says so.

// licKind is one licence action.
type licKind string

const (
	licAck      licKind = "ack"
	licRequest  licKind = "request"
	licInstall  licKind = "install"
	licActivate licKind = "activate"
)

const (
	// maxLicenceFile bounds an uploaded licence file. Licence files are a
	// few KiB; LibreDCM accepts a request body of 256 KiB.
	maxLicenceFile = 128 << 10
	// maxLicenceKey and maxLicenceSite are LibreDCM's limits.
	maxLicenceKey  = 128
	maxLicenceSite = 200
)

// licInfo names an action for the audit log and the pages.
var licInfo = map[licKind]struct{ act, title string }{
	licAck:      {"license.ack", "Acknowledge the unlicensed notice"},
	licRequest:  {"license.request", "Make an activation request file"},
	licInstall:  {"license.install", "Install a licence file"},
	licActivate: {"license.activate", "Activate online"},
}

// licencePending is what a licence warning page showed and what the apply
// step will send. It stays in memory on the server with the single-use
// confirmation (at most 10 minutes); the licence key is never written
// anywhere.
type licencePending struct {
	key, site string
	text      string // licence file content
	fileName  string // as the browser named it, for the pages only
	size      int
	sum       string // SHA-256 of the file
}

func masked(key string) string {
	if l4 := dcmclient.KeyLast4(key); l4 != "" {
		return "••••" + l4
	}
	return "••••"
}

// cleanSite checks the site name typed for a request or an activation.
func cleanSite(s string) (string, error) {
	s = strings.TrimSpace(s)
	if len(s) > maxLicenceSite {
		return "", fmt.Errorf("the site name is longer than %d characters", maxLicenceSite)
	}
	for _, c := range s {
		if unicode.IsControl(c) {
			return "", fmt.Errorf("the site name holds a control character")
		}
	}
	return s, nil
}

// cleanKey checks a licence key typed into the form. required: the key may
// not be empty.
func cleanKey(k string, required bool) (string, error) {
	k = strings.TrimSpace(k)
	switch {
	case k == "" && required:
		return "", fmt.Errorf("a licence key is required")
	case len(k) > maxLicenceKey || strings.ContainsAny(k, " \t\r\n"):
		return "", fmt.Errorf("this is not a licence key (at most %d characters, no spaces)", maxLicenceKey)
	}
	return k, nil
}

// licenceFile reads the uploaded licence file: text, not empty, at most
// maxLicenceFile bytes.
func licenceFile(r *http.Request) (*licencePending, error) {
	f, hdr, err := r.FormFile("license")
	if err != nil {
		return nil, fmt.Errorf("choose the licence file to upload")
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, maxLicenceFile+1))
	if err != nil {
		return nil, fmt.Errorf("the file could not be read")
	}
	switch {
	case len(b) > maxLicenceFile:
		return nil, errTooLarge
	case len(strings.TrimSpace(string(b))) == 0:
		return nil, fmt.Errorf("the file is empty")
	case !utf8.Valid(b) || strings.ContainsRune(string(b), 0):
		return nil, fmt.Errorf("this is not a licence file: a licence file is text")
	}
	sum := sha256.Sum256(b)
	name := hdr.Filename
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	return &licencePending{text: string(b), fileName: clip(name, 100), size: len(b), sum: hex.EncodeToString(sum[:])}, nil
}

var errTooLarge = fmt.Errorf("the licence file is larger than %d KiB", maxLicenceFile>>10)

// licencePage shows the licence state of an instance and, for an Admin,
// the licence actions (WT-SRS-027).
func (s *Server) licencePage(w http.ResponseWriter, r *http.Request, rc *reqCtx, ic *instCtx) {
	if !s.need(w, r, rc, ic, "license", "Licence") {
		return
	}
	v, err := ic.client.License(r.Context())
	if err != nil {
		s.readFailed(w, r, rc, ic, "license.view", "Licence", err)
		return
	}
	if !s.sameNode(w, r, rc, ic, v.Instance) {
		return
	}
	allowed, why := ic.snap.ActionsAllowed()
	s.renderI(w, r, http.StatusOK, "license", rc, ic, map[string]any{"L": v.License, "ReadAt": time.Now(),
		"Allowed": allowed, "Blocked": why, "CanAct": isAdmin(rc) && v.License.Enforced})
}

// licenceReview checks the form of a licence action, reads the licence
// state and shows the warning page with the single-use confirmation.
func (s *Server) licenceReview(kind licKind) func(http.ResponseWriter, *http.Request, *reqCtx, *instCtx) {
	info := licInfo[kind]
	return func(w http.ResponseWriter, r *http.Request, rc *reqCtx, ic *instCtx) {
		back := ic.base() + "/license"
		if !s.need(w, r, rc, ic, "license", "Licence") || s.blocked(w, r, rc, ic, info.act, string(kind), back) {
			return
		}
		bad := func(code int, msg string) {
			_ = s.auditI(r, rc.user.Username, ic.name, info.act+".review", string(kind), "failure", clip(msg, 300))
			s.renderI(w, r, code, "error", rc, ic, map[string]any{"Title": info.title + ": not possible", "Message": msg, "Back": back})
		}
		lp := &licencePending{}
		var err error
		switch kind {
		case licRequest, licActivate:
			if lp.key, err = cleanKey(r.PostFormValue("key"), kind == licActivate); err == nil {
				lp.site, err = cleanSite(r.PostFormValue("site"))
			}
		case licInstall:
			lp, err = licenceFile(r)
		}
		if err != nil {
			code := http.StatusUnprocessableEntity
			if err == errTooLarge {
				code = http.StatusRequestEntityTooLarge
			}
			bad(code, upperFirst(err.Error())+". Nothing was sent.")
			return
		}
		v, err := ic.client.License(r.Context())
		if err != nil {
			bad(http.StatusBadGateway, err.Error()+". Nothing was sent.")
			return
		}
		if v.Instance.Identity() != ic.snap.Reported {
			bad(http.StatusConflict, "Another node answered than the one this page is for. Nothing was sent.")
			return
		}
		p := &pending{target: string(kind), lic: lp}
		switch kind {
		case licAck:
			p.summary = "acknowledge the unlicensed notice; licence state " + orNone(v.License.State)
		case licRequest:
			p.shown = lp.site + "\x00" + dcmclient.KeyLast4(lp.key)
			p.summary = fmt.Sprintf("activation request file; key %s; site %q", keyNote(lp.key), lp.site)
		case licActivate:
			p.shown = lp.site + "\x00" + dcmclient.KeyLast4(lp.key)
			p.summary = fmt.Sprintf("online activation; key %s; site %q", masked(lp.key), lp.site)
		case licInstall:
			p.shown = lp.sum
			p.summary = fmt.Sprintf("install licence file %q, %d bytes, sha256 %s", lp.fileName, lp.size, lp.sum)
		}
		tok := s.issueAction(r, rc, ic, info.act, p)
		data := map[string]any{"Kind": string(kind), "Title": info.title,
			"L": v.License, "Confirm": tok, "Back": back, "Key": keyNote(lp.key), "Site": lp.site}
		if kind == licInstall {
			data["File"] = map[string]any{"Name": lp.fileName, "Size": lp.size, "SHA256": lp.sum}
		}
		s.renderI(w, r, http.StatusOK, "licensereview", rc, ic, data)
	}
}

func keyNote(key string) string {
	if strings.TrimSpace(key) == "" {
		return "none (the request names this machine only)"
	}
	return masked(key)
}

func orNone(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// licenceApply sends an approved licence action. The acknowledgement also
// needs the user to type their own user name: LibreDCM records the
// acknowledgement under the logged-in user, and the typed name shows that
// the person means it (D47).
func (s *Server) licenceApply(kind licKind) func(http.ResponseWriter, *http.Request, *reqCtx, *instCtx) {
	info := licInfo[kind]
	return func(w http.ResponseWriter, r *http.Request, rc *reqCtx, ic *instCtx) {
		back := ic.base() + "/license"
		var check func(*pending) (int, string)
		if kind == licAck {
			check = func(*pending) (int, string) {
				if strings.ToLower(strings.TrimSpace(r.PostFormValue("signature"))) != rc.user.Username {
					return http.StatusUnprocessableEntity, "Type your own user name (" + rc.user.Username +
						") to sign the acknowledgement. Nothing was sent."
				}
				return 0, ""
			}
		}
		p, env, _, ok := s.approveAction(w, r, rc, ic, info.act, info.title, back, check)
		if !ok {
			return
		}
		lp := p.lic
		var res dcmclient.LicenseResult
		var err error
		switch kind {
		case licAck:
			res, err = ic.client.LicenseAck(r.Context(), env)
		case licInstall:
			res, err = ic.client.LicenseInstall(r.Context(), lp.text, env)
		case licActivate:
			res, err = ic.client.LicenseActivate(r.Context(), lp.key, lp.site, env)
		case licRequest:
			s.licenceRequest(w, r, rc, ic, p, env)
			return
		}
		if err != nil {
			s.actionFailed(w, r, rc, ic, info.act, info.title, back, p, env, err)
			return
		}
		l := res.License
		_ = s.auditI(r, rc.user.Username, ic.name, info.act, p.target, "success",
			clip(fmt.Sprintf("%s; licence state now %s, licensed %t, acknowledgement needed %t; request_id=%s",
				p.summary, orNone(l.State), l.Licensed, l.AckRequired, env.RequestID), 1000))
		// The licence state is part of the status: read it again for the
		// fleet page and the dashboard.
		ic.snap, _ = s.fleet.Fresh(r.Context(), ic.name)
		s.renderI(w, r, http.StatusOK, "licenseresult", rc, ic, map[string]any{"Kind": string(kind), "Title": info.title,
			"L": l, "RequestID": env.RequestID, "Back": back})
	}
}

// licenceRequest asks LibreDCM for the activation request text and hands
// it to the browser as a file. The text holds the licence key in its
// encoded block (the licence portal needs it): it is never shown in a page,
// logged or written to the audit log.
func (s *Server) licenceRequest(w http.ResponseWriter, r *http.Request, rc *reqCtx, ic *instCtx, p *pending, env dcmclient.Envelope) {
	info := licInfo[licRequest]
	res, err := ic.client.LicenseRequest(r.Context(), p.lic.key, p.lic.site, env)
	if err == nil && strings.TrimSpace(res.Request) == "" {
		err = fmt.Errorf("LibreDCM sent an empty activation request")
	}
	if err != nil {
		s.actionFailed(w, r, rc, ic, info.act, info.title, ic.base()+"/license", p, env, err)
		return
	}
	_ = s.auditI(r, rc.user.Username, ic.name, info.act, p.target, "success",
		clip(fmt.Sprintf("%s; request file of %d bytes handed out; request_id=%s", p.summary, len(res.Request), env.RequestID), 1000))
	name := "libredcm-activation-request-" + ic.name + "-" + time.Now().UTC().Format("20060102T150405Z") + ".txt"
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	// The instance name has only letters, digits, '-' and '_' (WT-SRS-013).
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.WriteHeader(http.StatusOK)
	// #nosec G705 -- sent as a text/plain attachment with nosniff and a script-blocking CSP, never as a page
	_, _ = io.WriteString(w, res.Request)
}
