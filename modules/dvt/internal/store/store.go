// Package store keeps LibreDVT's targets, uploads and results in the data
// directory (DVT-SRS-020, DVT-SRS-045). Everything is plain JSON and files,
// so results stay readable without LibreDVT.
package store

import (
	"archive/zip"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mulcahyio/libre-rws/modules/dvt/internal/plan"
	"github.com/mulcahyio/libre-rws/modules/dvt/internal/result"
)

// Target classes (DVT-SRS-020).
const (
	Lab        = "lab"
	Production = "production"
)

// Target is a system under test.
type Target struct {
	Name       string `json:"name"`
	Host       string `json:"host"`
	Port       int    `json:"port"`
	CalledAET  string `json:"called_aet"`
	CallingAET string `json:"calling_aet"`
	Class      string `json:"class"`
}

var nameRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// Normalize fills defaults and checks the target. A missing class means
// production (DVT-SRS-020), so a target is only "lab" when someone said so.
func (t *Target) Normalize() error {
	t.Name = strings.TrimSpace(t.Name)
	t.Host = strings.TrimSpace(t.Host)
	t.Class = strings.ToLower(strings.TrimSpace(t.Class))
	if t.Class == "" {
		t.Class = Production
	}
	var errs []error
	if !nameRe.MatchString(t.Name) {
		errs = append(errs, errors.New("name must be 1-64 letters, digits, '.', '_' or '-'"))
	}
	if t.Host == "" || len(t.Host) > 253 || strings.ContainsAny(t.Host, " \t/\\@") {
		errs = append(errs, errors.New("host must be a host name or IP address"))
	} else if strings.HasPrefix(t.Host, "-") {
		errs = append(errs, errors.New("host must not start with '-'"))
	} else if ip := net.ParseIP(t.Host); ip == nil && !regexp.MustCompile(`^[A-Za-z0-9.-]+$`).MatchString(t.Host) {
		errs = append(errs, errors.New("host must be a host name or IP address"))
	}
	if t.Port < 1 || t.Port > 65535 {
		errs = append(errs, errors.New("port must be 1-65535"))
	}
	if !plan.ValidAET(t.CalledAET) {
		errs = append(errs, errors.New("called AE title must be 1-16 printable characters"))
	}
	if !plan.ValidAET(t.CallingAET) {
		errs = append(errs, errors.New("calling AE title must be 1-16 printable characters"))
	}
	if t.Class != Lab && t.Class != Production {
		errs = append(errs, errors.New("class must be lab or production"))
	}
	return errors.Join(errs...)
}

// Describe returns a one-line description for reports.
func (t Target) Describe() string {
	return fmt.Sprintf("%s (%s:%d, called AE %s, calling AE %s, class %s)", t.Name, t.Host, t.Port, t.CalledAET, t.CallingAET, t.Class)
}

// Store is the data directory.
type Store struct {
	Dir string
	mu  sync.Mutex
}

// Open creates the directory layout.
func Open(dir string) (*Store, error) {
	for _, d := range []string{"", "runs", "validations", "uploads"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o750); err != nil {
			return nil, err
		}
	}
	return &Store{Dir: dir}, nil
}

// NewID returns a sortable unique ID: UTC timestamp plus random suffix.
func NewID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return time.Now().UTC().Format("20060102-150405") + "-" + hex.EncodeToString(b)
}

var idRe = regexp.MustCompile(`^\d{8}-\d{6}-[0-9a-f]{8}$`)

// ValidID reports whether id has the NewID form (protects paths).
func ValidID(id string) bool { return idRe.MatchString(id) }

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o640); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path) // #nosec G304 -- path built from the data dir and a validated ID
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// --- targets ---

// Targets returns all targets, sorted by name.
func (s *Store) Targets() ([]Target, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.targetsLocked()
}

func (s *Store) targetsLocked() ([]Target, error) {
	var ts []Target
	err := readJSON(filepath.Join(s.Dir, "targets.json"), &ts)
	if errors.Is(err, os.ErrNotExist) {
		return []Target{}, nil
	}
	if err != nil {
		return nil, err
	}
	for i := range ts {
		if ts[i].Class == "" {
			ts[i].Class = Production // DVT-SRS-020, also for hand-edited files
		}
	}
	sort.Slice(ts, func(i, j int) bool { return ts[i].Name < ts[j].Name })
	return ts, nil
}

// Target returns the named target.
func (s *Store) Target(name string) (Target, bool, error) {
	ts, err := s.Targets()
	if err != nil {
		return Target{}, false, err
	}
	for _, t := range ts {
		if t.Name == name {
			return t, true, nil
		}
	}
	return Target{}, false, nil
}

// PutTarget adds or replaces a target.
func (s *Store) PutTarget(t Target) error {
	if err := t.Normalize(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ts, err := s.targetsLocked()
	if err != nil {
		return err
	}
	out := []Target{t}
	for _, x := range ts {
		if x.Name != t.Name {
			out = append(out, x)
		}
	}
	return writeJSON(filepath.Join(s.Dir, "targets.json"), out)
}

// DeleteTarget removes a target.
func (s *Store) DeleteTarget(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ts, err := s.targetsLocked()
	if err != nil {
		return err
	}
	out := []Target{}
	for _, x := range ts {
		if x.Name != name {
			out = append(out, x)
		}
	}
	return writeJSON(filepath.Join(s.Dir, "targets.json"), out)
}

// --- results ---

// RunDir is the directory of a run (report.html, junit.xml, plan copy).
func (s *Store) RunDir(id string) string { return filepath.Join(s.Dir, "runs", id) }

// ValidationDir is the directory of a validation.
func (s *Store) ValidationDir(id string) string { return filepath.Join(s.Dir, "validations", id) }

// UploadDir is the directory holding uploaded files for id.
func (s *Store) UploadDir(id string) string { return filepath.Join(s.Dir, "uploads", id) }

// SaveRun writes run.json.
func (s *Store) SaveRun(r *result.Run) error {
	if err := os.MkdirAll(s.RunDir(r.ID), 0o750); err != nil {
		return err
	}
	return writeJSON(filepath.Join(s.RunDir(r.ID), "run.json"), r)
}

// Run loads a run.
func (s *Store) Run(id string) (*result.Run, error) {
	if !ValidID(id) {
		return nil, os.ErrNotExist
	}
	var r result.Run
	return &r, readJSON(filepath.Join(s.RunDir(id), "run.json"), &r)
}

// SaveValidation writes validation.json.
func (s *Store) SaveValidation(v *result.Validation) error {
	if err := os.MkdirAll(s.ValidationDir(v.ID), 0o750); err != nil {
		return err
	}
	return writeJSON(filepath.Join(s.ValidationDir(v.ID), "validation.json"), v)
}

// Validation loads a validation.
func (s *Store) Validation(id string) (*result.Validation, error) {
	if !ValidID(id) {
		return nil, os.ErrNotExist
	}
	var v result.Validation
	return &v, readJSON(filepath.Join(s.ValidationDir(id), "validation.json"), &v)
}

func (s *Store) ids(kind string) []string {
	ents, _ := os.ReadDir(filepath.Join(s.Dir, kind))
	var ids []string
	for _, e := range ents {
		if e.IsDir() && ValidID(e.Name()) {
			ids = append(ids, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(ids))) // newest first (DVT-SRS-045)
	return ids
}

// Runs lists runs, newest first.
func (s *Store) Runs() []*result.Run {
	var out []*result.Run
	for _, id := range s.ids("runs") {
		if r, err := s.Run(id); err == nil {
			out = append(out, r)
		}
	}
	return out
}

// Validations lists validations, newest first.
func (s *Store) Validations() []*result.Validation {
	var out []*result.Validation
	for _, id := range s.ids("validations") {
		if v, err := s.Validation(id); err == nil {
			out = append(out, v)
		}
	}
	return out
}

// Delete removes a run or validation with its uploads (DVT-SRS-045).
func (s *Store) Delete(id string) error {
	if !ValidID(id) {
		return os.ErrNotExist
	}
	for _, d := range []string{s.RunDir(id), s.ValidationDir(id), s.UploadDir(id)} {
		if err := os.RemoveAll(d); err != nil {
			return err
		}
	}
	return nil
}

// --- uploads ---

// Limits for uploads and zip expansion.
const (
	MaxUploadBytes = 2 << 30 // 2 GiB expanded per upload
	MaxZipEntries  = 20000
)

// SafeName reduces an uploaded file name to a safe relative path, or ""
// when it is absolute or climbs out of the upload directory.
func SafeName(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	if strings.HasPrefix(name, "/") || filepath.IsAbs(name) {
		return ""
	}
	clean := filepath.Clean(name)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return ""
	}
	for _, part := range strings.Split(clean, "/") {
		if part == ".." || strings.HasPrefix(part, "-") {
			return ""
		}
	}
	return clean
}

// SaveUpload writes one uploaded stream under dir/name. Zip archives
// (by extension) are expanded instead, with entry checks and size limits.
// It returns the written file paths and their display names.
func SaveUpload(dir, name string, r io.Reader, budget *int64) (map[string]string, error) {
	safe := SafeName(name)
	if safe == "" {
		return nil, fmt.Errorf("refused file name %q", name)
	}
	if strings.EqualFold(filepath.Ext(safe), ".zip") {
		tmp, err := os.CreateTemp(dir, ".upload-*.zip")
		if err != nil {
			return nil, err
		}
		defer os.Remove(tmp.Name())
		defer tmp.Close()
		n, err := io.Copy(tmp, io.LimitReader(r, *budget+1))
		if err != nil {
			return nil, err
		}
		if n > *budget {
			return nil, errors.New("upload too large")
		}
		return expandZip(tmp.Name(), filepath.Join(dir, strings.TrimSuffix(safe, filepath.Ext(safe))), safe, budget)
	}
	dst := filepath.Join(dir, safe)
	if err := writeLimited(dst, r, budget); err != nil {
		return nil, err
	}
	return map[string]string{dst: safe}, nil
}

func writeLimited(dst string, r io.Reader, budget *int64) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return err
	}
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640) // #nosec G304 -- dst is under the upload dir, name checked by SafeName
	if err != nil {
		return err
	}
	n, err := io.Copy(f, io.LimitReader(r, *budget+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if n > *budget {
		return errors.New("upload too large")
	}
	*budget -= n
	return nil
}

func expandZip(path, dir, display string, budget *int64) (map[string]string, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("%s: not a readable zip archive: %w", display, err)
	}
	defer zr.Close()
	if len(zr.File) > MaxZipEntries {
		return nil, fmt.Errorf("%s: more than %d entries", display, MaxZipEntries)
	}
	out := map[string]string{}
	for _, zf := range zr.File {
		if zf.FileInfo().IsDir() {
			continue
		}
		safe := SafeName(zf.Name)
		if safe == "" {
			return nil, fmt.Errorf("%s: refused entry name %q", display, zf.Name)
		}
		if strings.HasPrefix(filepath.Base(safe), ".") || strings.HasPrefix(safe, "__MACOSX/") {
			continue // macOS resource forks and hidden files
		}
		rc, err := zf.Open()
		if err != nil {
			return nil, err
		}
		dst := filepath.Join(dir, safe)
		err = writeLimited(dst, rc, budget)
		rc.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: %s: %w", display, zf.Name, err)
		}
		out[dst] = display + "/" + safe
	}
	return out, nil
}

// CollectFiles walks dir and returns every regular file, sorted.
func CollectFiles(dir string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() && !strings.HasPrefix(d.Name(), ".") {
			files = append(files, p)
		}
		return nil
	})
	sort.Strings(files)
	return files, err
}
