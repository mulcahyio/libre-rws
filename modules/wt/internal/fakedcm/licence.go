package fakedcm

import (
	"encoding/base64"
	"net/http"
	"strings"
	"time"

	"github.com/mulcahyio/libre-rws/modules/wt/internal/dcmclient"
)

// The licence and backup part of the fake (control-api-v1.md §4.3, §4.4).
// It behaves like LibreDCM where Libre WT depends on it: a source build
// refuses every licence action, a licence file holding "INVALID" is refused
// and leaves the installed licence as it was, the key never comes back in
// full, and a backup needs a backup directory.

const backupNote = "The snapshot holds the index only. Images are not included: back up the image store as well. " +
	"The file holds patient data; protect the directory."

// licence serves the licence endpoints. The caller holds the lock and has
// checked the envelope of a POST.
func (s *Server) licence(w http.ResponseWriter, path string, body map[string]any, inst dcmclient.InstanceInfo) {
	lic := &s.Cfg.Lic
	if path == "/control/v1/license" {
		write(w, 200, map[string]any{"instance": inst, "license": *lic})
		return
	}
	if !lic.Enforced {
		fail(w, http.StatusUnprocessableEntity, "license_not_enforced", "this is a source build: no licence is needed and there is nothing to change")
		return
	}
	key, _ := body["key"].(string)
	site, _ := body["site"].(string)
	installed := func(last4, customer string) {
		exp := time.Date(2027, 11, 16, 0, 0, 0, 0, time.UTC)
		lic.State, lic.Licensed, lic.AckRequired, lic.DaysLeft = "valid", true, false, 365
		lic.Message, lic.Customer, lic.KeyLast4, lic.Expires, lic.Term = "licensed until 2027-11-16", customer, last4, &exp, "annual"
		if site != "" {
			lic.Site = site
		}
	}
	switch path {
	case "/control/v1/license/ack":
		op, _ := body["operator"].(string)
		lic.AckRequired = false
		lic.Ack = &dcmclient.LicenseAck{By: op, At: time.Now().UTC(), State: lic.State, ValidUntil: time.Now().UTC().Add(24 * time.Hour), Valid: true}
		write(w, 200, map[string]any{"instance": inst, "acknowledged": true, "license": *lic})
	case "/control/v1/license/request":
		text := "-----BEGIN LIBREKEY ACTIVATION REQUEST-----\n" +
			base64.StdEncoding.EncodeToString([]byte(key+"|"+site+"|"+lic.Fingerprint)) +
			"\n-----END LIBREKEY ACTIVATION REQUEST-----\n"
		write(w, 200, map[string]any{"instance": inst, "request": text, "key_last4": dcmclient.KeyLast4(key),
			"note": "Send this text to the licence portal, then install the licence file it returns."})
	case "/control/v1/license/install":
		text, _ := body["license"].(string)
		switch {
		case strings.TrimSpace(text) == "":
			fail(w, http.StatusUnprocessableEntity, "license_required", "license is required: the text of the licence file")
		case strings.Contains(text, "INVALID"):
			fail(w, http.StatusUnprocessableEntity, "license_invalid", "licence refused; the installed licence is unchanged: signature does not verify")
		default:
			installed("WXYZ", "Example Clinic (synthetic)")
			write(w, 200, map[string]any{"instance": inst, "installed": true, "license": *lic})
		}
	case "/control/v1/license/activate":
		if strings.TrimSpace(key) == "" {
			fail(w, http.StatusUnprocessableEntity, "license_key_required", "key is required")
			return
		}
		installed(dcmclient.KeyLast4(key), "Example Clinic (synthetic)")
		write(w, 200, map[string]any{"instance": inst, "activated": true, "license": *lic})
	}
}

// backup serves the backup endpoints.
func (s *Server) backup(w http.ResponseWriter, path string, inst dcmclient.InstanceInfo) {
	off := s.Cfg.BackupDir == ""
	reason := s.Cfg.BackupReason
	if reason == "" {
		reason = "no backup directory is configured on this node (control.backup_dir, LIBRE_DCM_CONTROL_BACKUP_DIR)"
	}
	if path == "/control/v1/backups" {
		list := s.Cfg.Backups
		if list == nil {
			list = []dcmclient.BackupFile{}
		}
		out := map[string]any{"instance": inst, "configured": !off, "backups": list, "count": len(list), "more": false, "note": backupNote}
		if off {
			out["backups"], out["reason"], out["code"] = []dcmclient.BackupFile{}, reason, "backup_not_configured"
		} else {
			out["directory"] = s.Cfg.BackupDir
		}
		write(w, 200, out)
		return
	}
	if off {
		fail(w, http.StatusUnprocessableEntity, "backup_not_configured", reason)
		return
	}
	now := time.Now().UTC()
	f := dcmclient.BackupFile{File: "index-" + s.NodeID + "-" + now.Format("20060102T150405Z") + ".db", Bytes: 8192, Time: now}
	s.Cfg.Backups = append([]dcmclient.BackupFile{f}, s.Cfg.Backups...)
	write(w, 200, map[string]any{"instance": inst, "file": f.File, "bytes": f.Bytes,
		"sha256": "5f3c0b8e1d2a4c6e8f0a1b3c5d7e9f1a2b4c6d8e0f1a3b5c7d9e1f2a4b6c8d0e", "duration_ms": 12, "note": backupNote})
}
