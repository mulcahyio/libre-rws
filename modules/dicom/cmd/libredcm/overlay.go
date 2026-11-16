package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/mulcahyio/libre-rws/modules/dicom/internal/control"
	"github.com/mulcahyio/libre-rws/modules/dicom/internal/gateway"
	"github.com/mulcahyio/libre-rws/platform/config"
)

// The overlay file (DCM-SRS-093, -094). The control API never rewrites the
// administrator's config.yaml. A change made through it is written to one
// extra file that LibreDCM owns, and the loader reads that file between
// config.yaml and the environment:
//
//	defaults -> config.yaml -> overlay -> environment variables
//
// The overlay can hold only the two settings that change without a restart:
// dicom.remote_aes and dicom.allowed_aes. A setting it holds replaces the
// value of config.yaml as a whole. Any other key in the file is an error:
// the overlay can never change a setting that needs a restart.
//
// The previous version is kept next to it as "<overlay>.prev".

const (
	overlayVersion  = 1
	overlayMaxBytes = 1 << 20
	// Bounds of the two lists, so a plan and the file stay small (DCM-SRS-096).
	maxRemoteAEs  = 200
	maxAllowedAEs = 500
)

// overlayMeta says who wrote an overlay version, when and why.
type overlayMeta struct {
	Time      string `yaml:"time"`
	Operator  string `yaml:"operator"`
	Reason    string `yaml:"reason"`
	RequestID string `yaml:"request_id"`
	// Kind: "edit", "rollback", or "initial" for the state before the
	// first change (no overlay).
	Kind string `yaml:"kind"`
}

// overlayDoc is the content of the overlay file. A nil setting is not held
// by the overlay; an empty one is held and empty.
type overlayDoc struct {
	Version int         `yaml:"version"`
	Meta    overlayMeta `yaml:"meta"`
	DICOM   struct {
		RemoteAEs  *map[string]gateway.RemoteAE `yaml:"remote_aes,omitempty"`
		AllowedAEs *[]string                    `yaml:"allowed_aes,omitempty"`
	} `yaml:"dicom"`
}

// validate checks the settings of an overlay the way the API checks a request.
func (o *overlayDoc) validate() error {
	if o.Version != overlayVersion {
		return fmt.Errorf("version %d is not supported (want %d)", o.Version, overlayVersion)
	}
	if o.DICOM.RemoteAEs != nil {
		if len(*o.DICOM.RemoteAEs) > maxRemoteAEs {
			return fmt.Errorf("more than %d remote AEs", maxRemoteAEs)
		}
		for name, r := range *o.DICOM.RemoteAEs {
			if err := gateway.ValidateRemoteAE(name, r); err != nil {
				return err
			}
		}
	}
	if o.DICOM.AllowedAEs != nil {
		if len(*o.DICOM.AllowedAEs) > maxAllowedAEs {
			return fmt.Errorf("more than %d allow-list entries", maxAllowedAEs)
		}
		if _, err := gateway.NormalizeAllowedAEs(*o.DICOM.AllowedAEs); err != nil {
			return err
		}
	}
	return nil
}

// digest covers the settings of the overlay, not who wrote it or when.
func (o *overlayDoc) digest() string {
	type dest struct {
		Name, Host string
		Port       int
		AETitle    string
	}
	var v struct {
		HasRemote, HasAllowed bool
		RemoteAEs             []dest
		AllowedAEs            []string
	}
	if o != nil && o.DICOM.RemoteAEs != nil {
		v.HasRemote = true
		for name, r := range *o.DICOM.RemoteAEs {
			v.RemoteAEs = append(v.RemoteAEs, dest{name, r.Host, r.Port, r.AETitle})
		}
		sort.Slice(v.RemoteAEs, func(i, j int) bool { return v.RemoteAEs[i].Name < v.RemoteAEs[j].Name })
	}
	if o != nil && o.DICOM.AllowedAEs != nil {
		v.HasAllowed = true
		v.AllowedAEs = append([]string{}, *o.DICOM.AllowedAEs...)
	}
	raw, _ := json.Marshal(v)
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func (o *overlayDoc) marshal() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString("# Written by LibreDCM (control API). Do not edit by hand.\n" +
		"# It holds settings changed from the management console. To drop these\n" +
		"# changes, delete this file and reload or restart LibreDCM.\n")
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(o); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// parseOverlay reads an overlay strictly: unknown keys are refused.
func parseOverlay(raw []byte) (*overlayDoc, error) {
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	var o overlayDoc
	if err := dec.Decode(&o); err != nil {
		return nil, err
	}
	if err := o.validate(); err != nil {
		return nil, err
	}
	return &o, nil
}

// readOverlay reads the overlay at path. A missing file gives nil: no
// overlay. Any other problem is an error; the caller must not go on with
// the settings of config.yaml alone, which may be weaker (fail closed).
func readOverlay(path string) (*overlayDoc, []byte, error) {
	if path == "" {
		return nil, nil, nil
	}
	f, err := os.Open(path) // #nosec G304 -- path is the node's own control.overlay_file setting
	if os.IsNotExist(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("config overlay %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, overlayMaxBytes+1))
	if err != nil {
		return nil, nil, fmt.Errorf("config overlay %s: %w", path, err)
	}
	if len(raw) > overlayMaxBytes {
		return nil, nil, fmt.Errorf("config overlay %s: larger than %d bytes", path, overlayMaxBytes)
	}
	o, err := parseOverlay(raw)
	if err != nil {
		return nil, nil, fmt.Errorf("config overlay %s: %w", path, err)
	}
	return o, raw, nil
}

// writeFileAtomic replaces path with data: temporary file in the same
// directory, sync, rename, sync of the directory. A reader sees the old
// content or the new, never a part.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	fail := func(err error) error {
		_ = tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		return fail(err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fail(err)
	}
	if err := tmp.Sync(); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return err
	}
	if d, err := os.Open(dir); err == nil { // #nosec G304 -- directory of the node's own overlay file
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

// overlayUse says which settings of a loaded configuration came from the overlay.
type overlayUse struct {
	RemoteAEs, AllowedAEs bool
	Digest                string
}

// loadConfigOverlay loads the configuration in the order defaults,
// config.yaml, overlay, environment. overlayPath nil takes the overlay path
// from the configuration itself (start-up); otherwise the given path is
// used ("" for none). candidate, when not nil, stands in for the content of
// the overlay file: this is how a change is validated by the normal loader
// before it is written.
func loadConfigOverlay(cfgPath string, overlayPath *string, candidate *overlayDoc) (Config, overlayUse, error) {
	var use overlayUse
	cfg := defaults()
	if err := config.Load(cfgPath, &cfg); err != nil {
		return cfg, use, err
	}
	path := cfg.Control.OverlayFile
	if overlayPath != nil {
		path = *overlayPath
	}
	ov := candidate
	if ov == nil {
		var err error
		if ov, _, err = readOverlay(path); err != nil {
			return cfg, use, err
		}
	} else if err := ov.validate(); err != nil {
		return cfg, use, fmt.Errorf("config overlay: %w", err)
	}
	use.Digest = ov.digest()
	if ov != nil {
		// The overlay replaces the file's value. An environment variable
		// still wins (DCM-SRS-050): the allow-list of the environment was
		// applied by config.Load and stays; destinations of the environment
		// are merged in below.
		if ov.DICOM.AllowedAEs != nil && config.Getenv("LIBRE_DCM_ALLOWED_AES") == "" {
			cfg.DICOM.AllowedAEs = append([]string{}, *ov.DICOM.AllowedAEs...)
			use.AllowedAEs = true
		}
		if ov.DICOM.RemoteAEs != nil {
			cfg.DICOM.RemoteAEs = map[string]gateway.RemoteAE{}
			for k, v := range *ov.DICOM.RemoteAEs {
				cfg.DICOM.RemoteAEs[k] = v
			}
			use.RemoteAEs = true
		}
	}
	if v := config.Getenv("LIBRE_DCM_REMOTE_AES", "RWS_DICOM_REMOTE_AES"); v != "" {
		aes, err := parseRemoteAEs(v)
		if err != nil {
			return cfg, use, err
		}
		if cfg.DICOM.RemoteAEs == nil {
			cfg.DICOM.RemoteAEs = map[string]gateway.RemoteAE{}
		}
		for k, a := range aes {
			cfg.DICOM.RemoteAEs[k] = a
		}
	}
	return cfg, use, nil
}

// ---- the reloader as control.ConfigEditor ----

var errOverlayOff = errors.New("configuration edit is off on this node: control.overlay_file is empty")

// Enabled reports whether this node has an overlay file to write to.
func (r *reloader) Enabled() bool { return r.overlayPath != "" }

// Managed reports which live settings the overlay supplied at the last
// load that was put in force.
func (r *reloader) Managed() (remoteAEs, allowedAEs bool) {
	r.useMu.Lock()
	defer r.useMu.Unlock()
	return r.use.RemoteAEs, r.use.AllowedAEs
}

// candidate builds the overlay an edit would write: the current overlay
// with the edited settings replaced.
func (r *reloader) candidate(edit control.ConfigEdit) (*overlayDoc, []byte, error) {
	cur, raw, err := readOverlay(r.overlayPath)
	if err != nil {
		return nil, nil, err
	}
	next := &overlayDoc{Version: overlayVersion}
	if cur != nil {
		next.DICOM = cur.DICOM
	}
	if edit.RemoteAEs != nil {
		m := map[string]gateway.RemoteAE{}
		for k, v := range *edit.RemoteAEs {
			m[k] = v
		}
		next.DICOM.RemoteAEs = &m
	}
	if edit.AllowedAEs != nil {
		l := append([]string{}, *edit.AllowedAEs...)
		next.DICOM.AllowedAEs = &l
	}
	return next, raw, nil
}

// PreviewEdit validates an edit with the normal loader and returns what
// would be in force. Nothing changes.
func (r *reloader) PreviewEdit(edit control.ConfigEdit) (control.LiveSettings, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.Enabled() {
		return control.LiveSettings{}, errOverlayOff
	}
	cand, _, err := r.candidate(edit)
	if err != nil {
		return control.LiveSettings{}, err
	}
	next, _, err := r.load(cand)
	return next, err
}

// ApplyEdit validates an edit, lets check refuse it, writes it to the
// overlay and puts it in force through the normal reload.
func (r *reloader) ApplyEdit(edit control.ConfigEdit, meta control.OverlayMeta, check func(control.LiveSettings) error) (control.LiveSettings, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.Enabled() {
		return control.LiveSettings{}, errOverlayOff
	}
	cand, curRaw, err := r.candidate(edit)
	if err != nil {
		return control.LiveSettings{}, err
	}
	return r.commit(cand, curRaw, meta, "edit", check)
}

// PreviewRollback returns what would be in force with the previous overlay
// version. Nothing changes.
func (r *reloader) PreviewRollback() (control.LiveSettings, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.Enabled() {
		return control.LiveSettings{}, errOverlayOff
	}
	prev, _, err := r.previous()
	if err != nil {
		return control.LiveSettings{}, err
	}
	next, _, err := r.load(prev)
	return next, err
}

// ApplyRollback puts the previous overlay version back, through the same
// steps as an edit. The version that was current becomes the previous one,
// so a rollback can itself be rolled back.
func (r *reloader) ApplyRollback(meta control.OverlayMeta, check func(control.LiveSettings) error) (control.LiveSettings, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.Enabled() {
		return control.LiveSettings{}, errOverlayOff
	}
	prev, _, err := r.previous()
	if err != nil {
		return control.LiveSettings{}, err
	}
	_, curRaw, err := readOverlay(r.overlayPath)
	if err != nil {
		return control.LiveSettings{}, err
	}
	cand := &overlayDoc{Version: overlayVersion, DICOM: prev.DICOM}
	return r.commit(cand, curRaw, meta, "rollback", check)
}

// previous reads the kept previous overlay version.
func (r *reloader) previous() (*overlayDoc, []byte, error) {
	prev, raw, err := readOverlay(r.overlayPath + ".prev")
	if err != nil {
		return nil, nil, err
	}
	if prev == nil {
		return nil, nil, control.ErrNoPreviousVersion
	}
	return prev, raw, nil
}

// commit is the write half of an edit or a rollback. The caller holds r.mu,
// so no other reload (SIGHUP included) runs in between. Steps:
//
//  1. validate cand with the normal loader (licence block included);
//  2. let check refuse it (plan digest, confirmation, warnings);
//  3. keep the current overlay as the previous version;
//  4. write the new overlay atomically;
//  5. reload from the files, as SIGHUP does, and put the result in force.
//
// When step 4 or 5 fails, the overlay and the previous version are put back
// as they were and the running settings are not touched.
func (r *reloader) commit(cand *overlayDoc, curRaw []byte, meta control.OverlayMeta, kind string, check func(control.LiveSettings) error) (control.LiveSettings, error) {
	next, _, err := r.load(cand)
	if err != nil {
		return next, err
	}
	if check != nil {
		if err := check(next); err != nil {
			r.log.Warn("configuration change refused; keeping current settings", "err", err)
			return next, err
		}
	}
	cand.Meta = overlayMeta{Time: meta.Time.UTC().Format(time.RFC3339), Operator: meta.Operator,
		Reason: meta.Reason, RequestID: meta.RequestID, Kind: kind}
	newRaw, err := cand.marshal()
	if err != nil {
		return next, fmt.Errorf("config overlay: %w", err)
	}
	if _, err := parseOverlay(newRaw); err != nil { // what is written must load
		return next, fmt.Errorf("config overlay: %w", err)
	}

	prevPath := r.overlayPath + ".prev"
	oldPrev, prevErr := os.ReadFile(prevPath) // #nosec G304 -- next to the node's own overlay file
	keep := curRaw
	if keep == nil {
		// There was no overlay: the previous version is "no change".
		initial := &overlayDoc{Version: overlayVersion, Meta: overlayMeta{Time: cand.Meta.Time, Kind: "initial",
			Reason: "state before the first change from the control API"}}
		if keep, err = initial.marshal(); err != nil {
			return next, fmt.Errorf("config overlay: %w", err)
		}
	}
	restore := func() {
		if curRaw == nil {
			_ = os.Remove(r.overlayPath)
		} else {
			_ = writeFileAtomic(r.overlayPath, curRaw)
		}
		if prevErr == nil {
			_ = writeFileAtomic(prevPath, oldPrev)
		} else {
			_ = os.Remove(prevPath)
		}
	}
	if err := writeFileAtomic(prevPath, keep); err != nil {
		return next, fmt.Errorf("%w: keep previous version: %v", control.ErrConfigWrite, err)
	}
	if err := r.writeOverlay(r.overlayPath, newRaw); err != nil {
		restore()
		return next, fmt.Errorf("%w: %v", control.ErrConfigWrite, err)
	}
	applied, use, err := r.load(nil)
	if err == nil && !sameLive(applied, next) {
		err = errors.New("the file read back differs from the validated change")
	}
	if err == nil {
		err = r.put(applied, use)
	}
	if err != nil {
		restore()
		r.log.Error("configuration change failed; overlay put back, keeping current settings", "err", err)
		return next, fmt.Errorf("%w: %v", control.ErrConfigWrite, err)
	}
	r.log.Info("configuration changed through the control API", "kind", kind,
		"remote_aes", len(applied.RemoteAEs), "allowed_aes", len(applied.AllowedAEs))
	return applied, nil
}

// sameLive compares two sets of live settings.
func sameLive(a, b control.LiveSettings) bool {
	if len(a.RemoteAEs) != len(b.RemoteAEs) || len(a.AllowedAEs) != len(b.AllowedAEs) {
		return false
	}
	for k, v := range a.RemoteAEs {
		if w, ok := b.RemoteAEs[k]; !ok || w != v {
			return false
		}
	}
	for i := range a.AllowedAEs {
		if a.AllowedAEs[i] != b.AllowedAEs[i] {
			return false
		}
	}
	return true
}

func overlayInfo(o *overlayDoc) *control.OverlayVersion {
	if o == nil {
		return nil
	}
	v := &control.OverlayVersion{Time: o.Meta.Time, Operator: o.Meta.Operator, Reason: o.Meta.Reason,
		RequestID: o.Meta.RequestID, Kind: o.Meta.Kind, Digest: o.digest(), Settings: []string{}}
	if o.DICOM.AllowedAEs != nil {
		v.Settings = append(v.Settings, "dicom.allowed_aes")
	}
	if o.DICOM.RemoteAEs != nil {
		v.Settings = append(v.Settings, "dicom.remote_aes")
	}
	return v
}

// History returns the current and the kept previous overlay version; nil
// where there is none.
func (r *reloader) History() (current, previous *control.OverlayVersion, err error) {
	if !r.Enabled() {
		return nil, nil, errOverlayOff
	}
	cur, _, err := readOverlay(r.overlayPath)
	if err != nil {
		return nil, nil, err
	}
	prev, _, err := readOverlay(r.overlayPath + ".prev")
	if err != nil {
		return nil, nil, err
	}
	return overlayInfo(cur), overlayInfo(prev), nil
}
