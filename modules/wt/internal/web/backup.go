package web

import (
	"fmt"
	"net/http"
	"time"
)

// Online backup of the index of one LibreDCM instance (WT-SRS-028, -031).
// Libre WT offers no path or file name, no download and no delete:
// LibreDCM writes a new file into the directory of its own configuration
// and never replaces one (DCM-SRS-090, WT-HAZ-10).

const (
	actBackup = "backup"
	// backupHistory is how many backup results of the audit log the page
	// shows.
	backupHistory = 50
)

// backupsPage lists the snapshots on the node and the backups started from
// Libre WT, from its audit log (who, when, reason, file, size, checksum).
func (s *Server) backupsPage(w http.ResponseWriter, r *http.Request, rc *reqCtx, ic *instCtx) {
	if !s.need(w, r, rc, ic, "backup", "Backup") {
		return
	}
	v, err := ic.client.Backups(r.Context())
	if err != nil {
		s.readFailed(w, r, rc, ic, "backup.view", "Backup", err)
		return
	}
	if !s.sameNode(w, r, rc, ic, v.Instance) {
		return
	}
	data := map[string]any{"B": v, "ReadAt": time.Now()}
	data["Allowed"], data["Blocked"] = ic.snap.ActionsAllowed()
	hist, err := s.acct.AuditActionLog(r.Context(), ic.name, actBackup, backupHistory)
	if err != nil {
		data["HistoryErr"] = err.Error()
	}
	data["History"] = hist
	s.renderI(w, r, http.StatusOK, "backups", rc, ic, data)
}

// backupReview shows what a backup does and on which instance, and issues
// the confirmation. A node without a backup directory, or with a
// PostgreSQL index, gets no confirmation.
func (s *Server) backupReview(w http.ResponseWriter, r *http.Request, rc *reqCtx, ic *instCtx) {
	back := ic.base() + "/backups"
	if !s.need(w, r, rc, ic, "backup", "Backup") || s.blocked(w, r, rc, ic, actBackup, "index", back) {
		return
	}
	bad := func(code int, msg string) {
		_ = s.auditI(r, rc.user.Username, ic.name, actBackup+".review", "index", "failure", clip(msg, 300))
		s.renderI(w, r, code, "error", rc, ic, map[string]any{"Title": "Backup: not possible", "Message": msg, "Back": back})
	}
	v, err := ic.client.Backups(r.Context())
	switch {
	case err != nil:
		bad(http.StatusBadGateway, err.Error()+". Nothing was started.")
		return
	case v.Instance.Identity() != ic.snap.Reported:
		bad(http.StatusConflict, "Another node answered than the one this page is for. Nothing was started.")
		return
	case !v.Configured:
		bad(http.StatusUnprocessableEntity, "Backups can't be made from the console on this instance: "+orNone(v.Reason)+". Nothing was started.")
		return
	}
	p := &pending{target: "index", shown: v.Directory, summary: "index backup into the node's backup directory " + v.Directory}
	tok := s.issueAction(r, rc, ic, actBackup, p)
	s.renderI(w, r, http.StatusOK, "backupreview", rc, ic, map[string]any{"B": v, "Confirm": tok, "Back": back})
}

// backupApply starts the approved backup and shows the file, its size and
// its checksum.
func (s *Server) backupApply(w http.ResponseWriter, r *http.Request, rc *reqCtx, ic *instCtx) {
	back := ic.base() + "/backups"
	const title = "Backup"
	p, env, _, ok := s.approveAction(w, r, rc, ic, actBackup, title, back, nil)
	if !ok {
		return
	}
	res, err := ic.client.Backup(r.Context(), env)
	if err == nil && (res.File == "" || res.SHA256 == "") {
		err = fmt.Errorf("LibreDCM's answer names no backup file or checksum")
	}
	if err != nil {
		s.actionFailed(w, r, rc, ic, actBackup, title, back, p, env, err)
		return
	}
	_ = s.auditI(r, rc.user.Username, ic.name, actBackup, p.target, "success",
		clip(fmt.Sprintf("file %s, %d bytes, sha256 %s, %d ms; request_id=%s; reason: %s",
			res.File, res.Bytes, res.SHA256, res.DurationMS, env.RequestID, env.Reason), 1000))
	s.renderI(w, r, http.StatusOK, "backupresult", rc, ic, map[string]any{"R": res, "RequestID": env.RequestID, "Back": back})
}

// byteSize prints a size for people.
func byteSize(n int64) string {
	const k = 1024
	switch {
	case n >= k*k*k:
		return fmt.Sprintf("%.1f GiB", float64(n)/(k*k*k))
	case n >= k*k:
		return fmt.Sprintf("%.1f MiB", float64(n)/(k*k))
	case n >= k:
		return fmt.Sprintf("%.1f KiB", float64(n)/k)
	}
	return fmt.Sprintf("%d bytes", n)
}
