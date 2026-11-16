package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Index backup (control-api-v1.md §4.4, DCM-SRS-090).
//
// The client chooses nothing about where the snapshot goes. The directory
// comes from the node's own configuration, the file name is made here, and
// the file is created exclusively, so no existing file is ever replaced.
// There is no endpoint to download or delete a snapshot.

const (
	// BackupTimeout bounds one snapshot.
	BackupTimeout = 10 * time.Minute
	// backupSpaceFactor: free space needed, as a multiple of the index size
	// in percent (index size plus 10 %).
	backupSpacePercent = 110
	// backupListLimit bounds GET /control/v1/backups.
	backupListLimit = 500

	backupNote = "The snapshot holds the index only. Images are not included: back up the image store as well. " +
		"The file holds patient data; protect the directory."
)

// reBackupName matches the snapshot names this package makes.
var reBackupName = regexp.MustCompile(`^index-[A-Za-z0-9._-]{1,64}-\d{8}T\d{6}Z\.db$`)

// Backup is what the backup endpoints need from the node.
type Backup struct {
	// Dir is control.backup_dir. Empty: backups through the API are off.
	Dir string
	// Driver is the index driver. Only "sqlite" can be snapshotted here.
	Driver string
	// Snapshot writes a consistent point-in-time copy of the index to dest
	// while the node runs (index.Backup).
	Snapshot func(ctx context.Context, dest string) error
	// IndexBytes is the size of the index now.
	IndexBytes func() (int64, error)
	// FreeBytes is the free space in dir; nil uses the platform's own call.
	FreeBytes func(dir string) (uint64, error)
	// Now is the clock; nil uses time.Now.
	Now func() time.Time
}

// BackupRequest is the body of POST /control/v1/backup: the envelope only.
// There is no path or file name field.
type BackupRequest struct {
	Envelope
}

// safeNodeID turns the node ID into a file-name part: only letters, digits,
// '.', '_' and '-' stay, so the ID can never add a path element.
func safeNodeID(id string) string {
	var b strings.Builder
	for _, c := range []byte(id) {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '_', c == '-':
			b.WriteByte(c)
		case c == '.':
			b.WriteByte('_') // no "." or ".." element, no hidden file
		default:
			b.WriteByte('_')
		}
	}
	s := b.String()
	if len(s) > 64 {
		s = s[:64]
	}
	if s == "" {
		s = "node"
	}
	return s
}

func (a *API) backupOff() (code, msg string) {
	switch {
	case a.Backup == nil || strings.TrimSpace(a.Backup.Dir) == "":
		return "backup_not_configured", "no backup directory is configured on this node (control.backup_dir, LIBRE_DCM_CONTROL_BACKUP_DIR)"
	case a.Backup.Driver != "sqlite":
		return "backup_unsupported", "the index is not SQLite: back it up with the database's own tools (pg_dump)"
	case a.Backup.Snapshot == nil:
		return "internal", "backup is not available on this node"
	}
	return "", ""
}

func (a *API) backup(ctx context.Context, _ *BackupRequest) actionResult {
	if code, msg := a.backupOff(); code != "" {
		return refusal(http.StatusUnprocessableEntity, code, msg, true)
	}
	b := a.Backup
	dir := filepath.Clean(b.Dir)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return refusal(http.StatusInternalServerError, "backup_failed", "the backup directory cannot be used", false, "stage", "directory")
	}

	// Free space: at least the index size plus 10 %. When the size or the
	// free space cannot be read, no backup is started.
	var need uint64
	if b.IndexBytes != nil {
		n, err := b.IndexBytes()
		if err != nil || n < 0 {
			return refusal(http.StatusUnprocessableEntity, "space_check_failed",
				"the index size could not be read; no backup was started", false)
		}
		need = uint64(n) / 100 * backupSpacePercent
		if need < uint64(n) {
			need = uint64(n)
		}
	}
	free := b.FreeBytes
	if free == nil {
		free = freeBytes
	}
	avail, err := free(dir)
	if err != nil {
		return refusal(http.StatusUnprocessableEntity, "space_check_failed",
			"the free space in the backup directory could not be read; no backup was started", false)
	}
	if avail < need {
		return refusal(http.StatusUnprocessableEntity, "insufficient_space",
			"not enough free space in the backup directory; no backup was started", false,
			"needed_bytes", need, "free_bytes", avail)
	}

	now := time.Now
	if b.Now != nil {
		now = b.Now
	}
	start := now()
	name := "index-" + safeNodeID(a.Node.ID) + "-" + start.UTC().Format("20060102T150405Z") + ".db"
	dest := filepath.Join(dir, name)
	if filepath.Dir(dest) != dir || !reBackupName.MatchString(name) {
		// Cannot happen with safeNodeID; kept as a second check.
		return refusal(http.StatusInternalServerError, "backup_failed", "backup file name refused", false)
	}
	// Reserve the name. O_EXCL fails when anything is there already, also
	// a symbolic link: an existing file is never written to or replaced.
	f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- dest is the configured directory plus a name made above from a fixed pattern
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return refusal(http.StatusConflict, "backup_exists",
				"a snapshot with this second's name exists already; it was not replaced. Try again.", false, "file", name)
		}
		return refusal(http.StatusInternalServerError, "backup_failed", "the backup file could not be created", false, "stage", "create")
	}
	_ = f.Close()
	fail := func(stage string) actionResult {
		_ = os.Remove(dest)
		_ = os.Remove(dest + ".partial")
		msg := "the snapshot failed; nothing was kept"
		if ctx.Err() != nil {
			msg = "the snapshot did not finish in time; nothing was kept"
		}
		return refusal(http.StatusInternalServerError, "backup_failed", msg, false, "stage", stage, "file", name)
	}
	// The snapshot is written next to dest and moved over the reservation
	// made above, which only this request created.
	if err := b.Snapshot(ctx, dest); err != nil {
		return fail("snapshot")
	}
	if err := os.Chmod(dest, 0o600); err != nil {
		return fail("chmod")
	}
	size, sum, err := fileDigest(dest)
	if err != nil || size == 0 {
		return fail("checksum")
	}
	ms := now().Sub(start).Milliseconds()
	return actionResult{status: http.StatusOK, outcome: outcomeSuccess,
		body:  map[string]any{"file": name, "bytes": size, "sha256": sum, "duration_ms": ms, "note": backupNote},
		attrs: []any{"file", name, "bytes", size, "sha256", sum, "duration_ms", ms}}
}

// fileDigest returns the size and SHA-256 of a file.
func fileDigest(path string) (int64, string, error) {
	f, err := os.Open(path) // #nosec G304 -- path is the snapshot this package just wrote
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return 0, "", err
	}
	return n, hex.EncodeToString(h.Sum(nil)), nil
}

// BackupFile is one snapshot in GET /control/v1/backups.
type BackupFile struct {
	File  string    `json:"file"`
	Bytes int64     `json:"bytes"`
	Time  time.Time `json:"time"`
}

// backups lists the snapshots in the configured directory: regular files
// with a name this package makes. Nothing else in the directory is shown.
func (a *API) backups(w http.ResponseWriter, _ *http.Request) {
	body := map[string]any{"instance": a.instance(), "backups": []BackupFile{}, "configured": false, "note": backupNote}
	if code, msg := a.backupOff(); code != "" {
		body["reason"], body["code"] = msg, code
		a.writeJSON(w, http.StatusOK, body)
		return
	}
	dir := filepath.Clean(a.Backup.Dir)
	body["configured"], body["directory"] = true, dir
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		a.failCode(w, http.StatusInternalServerError, "backup_list_failed", "the backup directory could not be read")
		return
	}
	files := []BackupFile{}
	for _, e := range entries {
		if !e.Type().IsRegular() || !reBackupName.MatchString(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, BackupFile{File: e.Name(), Bytes: info.Size(), Time: info.ModTime().UTC()})
	}
	sort.Slice(files, func(i, j int) bool {
		if !files[i].Time.Equal(files[j].Time) {
			return files[i].Time.After(files[j].Time)
		}
		return files[i].File > files[j].File
	})
	body["count"], body["more"] = len(files), false
	if len(files) > backupListLimit {
		files, body["more"] = files[:backupListLimit], true
	}
	body["backups"] = files
	a.writeJSON(w, http.StatusOK, body)
}
