package main

// Tests of the configuration overlay (overlay.go) with the real loader and
// the real reloader: where a change made through the control API is
// written, what the file may hold, in which order settings are loaded, and
// that a failed change leaves the files and the running settings as they
// were (DCM-SRS-093, -094).

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mulcahyio/libre-rws/modules/dicom/internal/control"
	"github.com/mulcahyio/libre-rws/modules/dicom/internal/gateway"
)

const baseConfig = `dicom:
  ae_title: OVERLAYTEST
  allowed_aes: [CT1, "MR1@192.168.1.20"]
  remote_aes:
    VIEWER: {host: 127.0.0.1, port: 11113}
`

// overlayEnv is a reloader on real files in a temporary directory.
type overlayEnv struct {
	dir, cfg, overlay string
	target            *fakeTarget
	r                 *reloader
}

func newOverlayEnv(t *testing.T) *overlayEnv {
	t.Helper()
	t.Setenv("LIBRE_DCM_REMOTE_AES", "")
	t.Setenv("LIBRE_DCM_ALLOWED_AES", "")
	t.Setenv("LIBRE_DCM_CONTROL_OVERLAY_FILE", "")
	dir := t.TempDir()
	e := &overlayEnv{dir: dir, cfg: filepath.Join(dir, "config.yaml"), overlay: filepath.Join(dir, "data", "config-overlay.yaml"), target: &fakeTarget{}}
	if err := os.WriteFile(e.cfg, []byte(baseConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	e.r = &reloader{path: e.cfg, overlayPath: e.overlay, gw: e.target, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := e.r.reload(); err != nil {
		t.Fatal(err)
	}
	return e
}

// state is a short text of what the target runs.
func (e *overlayEnv) state() string {
	var names []string
	for k, v := range e.target.remotes {
		names = append(names, fmt.Sprintf("%s=%s:%d/%s", k, v.Host, v.Port, v.AETitle))
	}
	sort.Strings(names)
	return fmt.Sprint(names, e.target.allowed)
}

// files returns the names and contents of every file under the directory.
func (e *overlayEnv) files(t *testing.T) string {
	t.Helper()
	var out []string
	err := filepath.Walk(e.dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		rel, _ := filepath.Rel(e.dir, p)
		out = append(out, rel+"\n"+string(b))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return strings.Join(out, "\n---\n")
}

func meta(op string) control.OverlayMeta {
	return control.OverlayMeta{Time: time.Date(2026, 11, 16, 9, 30, 0, 0, time.UTC), Operator: op, Reason: "test change", RequestID: "request-0000000001"}
}

func allowEdit(rules ...string) control.ConfigEdit {
	l := append([]string{}, rules...)
	return control.ConfigEdit{AllowedAEs: &l}
}

func remoteEdit(m map[string]gateway.RemoteAE) control.ConfigEdit {
	return control.ConfigEdit{RemoteAEs: &m}
}

// A change is written to the overlay file, never to config.yaml; it is in
// force at once, survives a reload and a restart, and the version before
// it is kept. A rollback puts that version back and can be rolled back.
// Verifies: DCM-SRS-093, DCM-SRS-094
func TestOverlayEditReloadRestartAndRollback(t *testing.T) {
	e := newOverlayEnv(t)
	start := e.state()
	if start != "[VIEWER=127.0.0.1:11113/] [CT1 MR1@192.168.1.20]" {
		t.Fatalf("start state = %s", start)
	}
	if ra, al := e.r.Managed(); ra || al || !e.r.Enabled() {
		t.Fatal("a node without an overlay file reports overlay settings")
	}
	if cur, prev, err := e.r.History(); err != nil || cur != nil || prev != nil {
		t.Fatalf("history before any change = %v %v %v", cur, prev, err)
	}

	// Preview changes nothing.
	next, err := e.r.PreviewEdit(allowEdit("CT1", "US1@10.0.0.0/24"))
	if err != nil || fmt.Sprint(next.AllowedAEs) != "[CT1 US1@10.0.0.0/24]" || len(next.RemoteAEs) != 1 {
		t.Fatalf("preview = %+v %v", next, err)
	}
	if _, err := os.Stat(e.overlay); !os.IsNotExist(err) || e.state() != start {
		t.Fatal("a preview wrote the overlay or changed the running settings")
	}

	// First edit: the allow-list.
	var seen control.LiveSettings
	if _, err := e.r.ApplyEdit(allowEdit("CT1", "US1@10.0.0.0/24"), meta("Pat Admin"), func(n control.LiveSettings) error { seen = n; return nil }); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(seen.AllowedAEs) != "[CT1 US1@10.0.0.0/24]" {
		t.Fatalf("the check saw %+v", seen)
	}
	first := e.state()
	if first != "[VIEWER=127.0.0.1:11113/] [CT1 US1@10.0.0.0/24]" {
		t.Fatalf("after the first edit: %s", first)
	}
	if b, _ := os.ReadFile(e.cfg); string(b) != baseConfig {
		t.Fatal("config.yaml was rewritten")
	}
	st, err := os.Stat(e.overlay)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("overlay file: %v, mode %v (want 0600)", err, st.Mode().Perm())
	}
	if ra, al := e.r.Managed(); ra || !al {
		t.Fatalf("managed = %v %v, want the allow-list only", ra, al)
	}
	cur, prev, err := e.r.History()
	if err != nil || cur.Operator != "Pat Admin" || cur.Reason != "test change" || cur.RequestID != "request-0000000001" || cur.Kind != "edit" ||
		cur.Time != "2026-11-16T09:30:00Z" || fmt.Sprint(cur.Settings) != "[dicom.allowed_aes]" || prev == nil || prev.Kind != "initial" || len(prev.Settings) != 0 {
		t.Fatalf("history = %+v / %+v (%v)", cur, prev, err)
	}

	// Second edit: the destinations. The allow-list of the first stays.
	if _, err := e.r.ApplyEdit(remoteEdit(map[string]gateway.RemoteAE{"VIEWER": {Host: "127.0.0.1", Port: 11113}, "NEWVIEW": {Host: "viewer-2.example", Port: 104, AETitle: "V2"}}),
		meta("Sam Operator"), nil); err != nil {
		t.Fatal(err)
	}
	second := e.state()
	if second != "[NEWVIEW=viewer-2.example:104/V2 VIEWER=127.0.0.1:11113/] [CT1 US1@10.0.0.0/24]" {
		t.Fatalf("after the second edit: %s", second)
	}
	cur, prev, _ = e.r.History()
	if cur.Operator != "Sam Operator" || fmt.Sprint(cur.Settings) != "[dicom.allowed_aes dicom.remote_aes]" || prev.Operator != "Pat Admin" || cur.Digest == prev.Digest {
		t.Fatalf("history after the second edit = %+v / %+v", cur, prev)
	}

	// A reload (SIGHUP) keeps the overlay's settings, also when config.yaml changes them.
	changed := strings.Replace(baseConfig, "[CT1, \"MR1@192.168.1.20\"]", "[XA1]", 1)
	if err := os.WriteFile(e.cfg, []byte(changed), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := e.r.reload(); err != nil || e.state() != second {
		t.Fatalf("after a reload: %s (%v), want %s", e.state(), err, second)
	}
	// A restart: a new loader on the same files gives the same settings.
	cfg, use, err := loadConfigOverlay(e.cfg, &e.overlay, nil)
	if err != nil || !use.AllowedAEs || !use.RemoteAEs || fmt.Sprint(cfg.DICOM.AllowedAEs) != "[CT1 US1@10.0.0.0/24]" ||
		len(cfg.DICOM.RemoteAEs) != 2 || cfg.DICOM.RemoteAEs["NEWVIEW"].Host != "viewer-2.example" || cfg.DICOM.AETitle != "OVERLAYTEST" {
		t.Fatalf("load after restart = %+v %+v %v", cfg.DICOM, use, err)
	}

	// Rollback: the first version again.
	next, err = e.r.PreviewRollback()
	if err != nil || len(next.RemoteAEs) != 1 || e.state() != second {
		t.Fatalf("rollback preview = %+v %v", next, err)
	}
	if _, err := e.r.ApplyRollback(meta("Pat Admin"), nil); err != nil || e.state() != first {
		t.Fatalf("after rollback: %s (%v), want %s", e.state(), err, first)
	}
	cur, prev, _ = e.r.History()
	if cur.Kind != "rollback" || cur.Operator != "Pat Admin" || fmt.Sprint(cur.Settings) != "[dicom.allowed_aes]" || prev.Operator != "Sam Operator" {
		t.Fatalf("history after rollback = %+v / %+v", cur, prev)
	}
	// And back again.
	if _, err := e.r.ApplyRollback(meta("Pat Admin"), nil); err != nil || e.state() != second {
		t.Fatalf("after the second rollback: %s (%v), want %s", e.state(), err, second)
	}
	// Only the overlay, its previous version and config.yaml exist: no temporary file is left.
	var names []string
	_ = filepath.Walk(e.dir, func(p string, info os.FileInfo, _ error) error {
		if !info.IsDir() {
			names = append(names, filepath.Base(p))
		}
		return nil
	})
	sort.Strings(names)
	if fmt.Sprint(names) != "[config-overlay.yaml config-overlay.yaml.prev config.yaml]" {
		t.Fatalf("files = %v", names)
	}
}

// Settings are loaded in the order defaults, config.yaml, overlay,
// environment. A setting the overlay holds replaces the file's value as a
// whole; an environment variable still wins.
// Verifies: DCM-SRS-093, DCM-SRS-050
func TestOverlayLoadOrder(t *testing.T) {
	e := newOverlayEnv(t)
	if _, err := e.r.ApplyEdit(control.ConfigEdit{AllowedAEs: allowEdit("US1").AllowedAEs,
		RemoteAEs: remoteEdit(map[string]gateway.RemoteAE{"OVL": {Host: "10.0.0.50", Port: 104}}).RemoteAEs}, meta("Pat Admin"), nil); err != nil {
		t.Fatal(err)
	}
	if e.state() != "[OVL=10.0.0.50:104/] [US1]" {
		t.Fatalf("overlay over file: %s", e.state())
	}
	// The environment wins over the overlay.
	t.Setenv("LIBRE_DCM_ALLOWED_AES", "ENVCT")
	t.Setenv("LIBRE_DCM_REMOTE_AES", "ENVVIEW=10.0.0.9:104,OVL=10.0.0.99:11112")
	if err := e.r.reload(); err != nil {
		t.Fatal(err)
	}
	if e.state() != "[ENVVIEW=10.0.0.9:104/ OVL=10.0.0.99:11112/] [ENVCT]" {
		t.Fatalf("environment over overlay: %s", e.state())
	}
	if ra, al := e.r.Managed(); !ra || al {
		t.Fatalf("managed = %v %v: the allow-list comes from the environment", ra, al)
	}
	t.Setenv("LIBRE_DCM_ALLOWED_AES", "")
	t.Setenv("LIBRE_DCM_REMOTE_AES", "")

	// An empty list in the overlay is a value: it replaces the file's list.
	if _, err := e.r.ApplyEdit(allowEdit(), meta("Pat Admin"), nil); err != nil {
		t.Fatal(err)
	}
	if len(e.target.allowed) != 0 {
		t.Fatalf("allow-list = %v, want empty", e.target.allowed)
	}
	if _, al := e.r.Managed(); !al {
		t.Fatal("the empty allow-list of the overlay is not reported as managed")
	}

	// The overlay path comes from the configuration at start-up; "" turns it off.
	withPath := baseConfig + "control:\n  overlay_file: " + e.overlay + "\n"
	if err := os.WriteFile(e.cfg, []byte(withPath), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, use, err := loadConfigOverlay(e.cfg, nil, nil)
	if err != nil || !use.AllowedAEs || len(cfg.DICOM.AllowedAEs) != 0 || cfg.DICOM.RemoteAEs["OVL"].Host != "10.0.0.50" {
		t.Fatalf("start-up load = %+v %+v %v", cfg.DICOM, use, err)
	}
	if err := os.WriteFile(e.cfg, []byte(baseConfig+"control:\n  overlay_file: \"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, use, err = loadConfigOverlay(e.cfg, nil, nil)
	if err != nil || use.AllowedAEs || use.RemoteAEs || fmt.Sprint(cfg.DICOM.AllowedAEs) != "[CT1 MR1@192.168.1.20]" || cfg.Control.OverlayFile != "" {
		t.Fatalf("overlay off: %+v %+v %v", cfg.DICOM, use, err)
	}
	off := &reloader{path: e.cfg, gw: &fakeTarget{}, log: e.r.log}
	if off.Enabled() {
		t.Fatal("a reloader without an overlay path is enabled")
	}
	if _, err := off.ApplyEdit(allowEdit("CT1"), meta("Pat Admin"), nil); !errors.Is(err, errOverlayOff) {
		t.Fatalf("edit without an overlay file: %v", err)
	}
	if _, err := off.PreviewEdit(allowEdit("CT1")); !errors.Is(err, errOverlayOff) {
		t.Fatalf("preview without an overlay file: %v", err)
	}
	if _, err := off.ApplyRollback(meta("Pat Admin"), nil); !errors.Is(err, errOverlayOff) {
		t.Fatalf("rollback without an overlay file: %v", err)
	}
	if def := defaults(); def.Control.OverlayFile != "./data/config-overlay.yaml" {
		t.Fatalf("default overlay file = %q", def.Control.OverlayFile)
	}
}

// The overlay file can hold the two live settings and nothing else. A file
// with any other key, an unknown version, an invalid entry or broken YAML
// is refused as a whole: the node does not start with it, and a reload
// keeps the running settings. It never falls back to config.yaml alone.
// Verifies: DCM-SRS-093
func TestOverlayFileHoldsOnlyLiveSettings(t *testing.T) {
	for name, content := range map[string]string{
		"AE title":           "version: 1\ndicom:\n  ae_title: EVIL\n",
		"listen address":     "version: 1\ndicom:\n  listen: \":104\"\n",
		"TLS off":            "version: 1\ndicom:\n  tls_cert_file: \"\"\n  tls_key_file: \"\"\n",
		"audit off":          "version: 1\ndicom:\n  audit_log_off: true\n",
		"conflict policy":    "version: 1\ndicom:\n  patient_conflict_policy: accept_separate\n",
		"admin token":        "version: 1\nadmin:\n  token: x\n",
		"storage path":       "version: 1\nstorage:\n  path: /tmp/elsewhere\n",
		"control section":    "version: 1\ncontrol:\n  backup_dir: /tmp\n",
		"unknown key":        "version: 1\nsurprise: true\n",
		"no version":         "dicom:\n  allowed_aes: [CT1]\n",
		"future version":     "version: 2\ndicom:\n  allowed_aes: [CT1]\n",
		"bad allow rule":     "version: 1\ndicom:\n  allowed_aes: [\"CT1@10.0.0.0/99\"]\n",
		"bad destination":    "version: 1\ndicom:\n  remote_aes:\n    V: {host: \"10.0.0.1/x\", port: 104}\n",
		"destination port":   "version: 1\ndicom:\n  remote_aes:\n    V: {host: 10.0.0.1, port: 0}\n",
		"destination extras": "version: 1\ndicom:\n  remote_aes:\n    V: {host: 10.0.0.1, port: 104, tls: false}\n",
		"not YAML":           "version: 1\ndicom: [this is not valid\n",
		"a list":             "- 1\n- 2\n",
		"too large":          "version: 1\n# " + strings.Repeat("x", overlayMaxBytes) + "\n",
	} {
		t.Run(name, func(t *testing.T) {
			e := newOverlayEnv(t)
			start := e.state()
			if err := os.MkdirAll(filepath.Dir(e.overlay), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(e.overlay, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, _, err := loadConfigOverlay(e.cfg, &e.overlay, nil); err == nil || !strings.Contains(err.Error(), "config overlay") {
				t.Fatalf("the loader accepted the overlay (err = %v)", err)
			}
			if err := e.r.reload(); err == nil || e.state() != start {
				t.Fatalf("reload with this overlay: err %v, running %s", err, e.state())
			}
			// Start-up: build() refuses to start.
			withPath := baseConfig + "control:\n  overlay_file: " + e.overlay + "\nstorage:\n  path: " + filepath.Join(e.dir, "blobs") +
				"\nindex:\n  dsn: " + filepath.Join(e.dir, "index.db") + "\n"
			if err := os.WriteFile(e.cfg, []byte(withPath), 0o600); err != nil {
				t.Fatal(err)
			}
			a, err := build(e.cfg)
			if a != nil {
				a.close()
			}
			if err == nil {
				t.Fatal("the node starts with this overlay")
			}
			// An edit through the API cannot be made on top of it either.
			if _, err := e.r.ApplyEdit(allowEdit("CT1"), meta("Pat Admin"), nil); err == nil {
				t.Fatal("an edit was applied on top of a refused overlay")
			}
			if b, _ := os.ReadFile(e.overlay); string(b) != content {
				t.Fatal("the refused overlay was rewritten")
			}
		})
	}
	// What the reloader writes itself passes the same strict reader.
	e := newOverlayEnv(t)
	if _, err := e.r.ApplyEdit(control.ConfigEdit{AllowedAEs: allowEdit("CT1", "US1@10.0.0.0/24").AllowedAEs,
		RemoteAEs: remoteEdit(map[string]gateway.RemoteAE{"V": {Host: "fd00::20", Port: 104, AETitle: "VAE"}}).RemoteAEs}, meta("Pat: \"Admin\"\n# x"), nil); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(e.overlay)
	o, err := parseOverlay(raw)
	if err != nil || o.Meta.Operator != "Pat: \"Admin\"\n# x" || (*o.DICOM.RemoteAEs)["V"].Host != "fd00::20" || !strings.HasPrefix(string(raw), "# Written by LibreDCM") {
		t.Fatalf("written overlay does not read back: %v\n%s", err, raw)
	}
}

// A change that is refused or fails leaves config.yaml, the overlay, the
// kept previous version and the running settings exactly as they were.
// Verifies: DCM-SRS-093, DCM-SRS-094, DCM-SRS-060
func TestOverlayFailedChangeLeavesEverythingAsItWas(t *testing.T) {
	longRule := "ABCDEFGHIJKLMNOP@2001:db8:1111:2222:3333:4444:5555:6666" // valid, and refused by the fake target (over 40 characters)
	errRefused := errors.New("refused by the check")
	for _, withOverlay := range []bool{false, true} {
		for name, c := range map[string]struct {
			arrange func(e *overlayEnv)
			run     func(e *overlayEnv) error
			want    error // nil: any error
		}{
			"check refuses": {nil, func(e *overlayEnv) error {
				_, err := e.r.ApplyEdit(allowEdit("XA1"), meta("Pat Admin"), func(control.LiveSettings) error { return errRefused })
				return err
			}, errRefused},
			"rollback check refuses": {nil, func(e *overlayEnv) error {
				_, err := e.r.ApplyRollback(meta("Pat Admin"), func(control.LiveSettings) error { return errRefused })
				if !withOverlayHasPrev(e) {
					return errRefused // nothing to roll back to: covered by its own case
				}
				return err
			}, errRefused},
			"invalid allow rule": {nil, func(e *overlayEnv) error {
				_, err := e.r.ApplyEdit(allowEdit("CT1@10.0.0.0/99"), meta("Pat Admin"), nil)
				return err
			}, nil},
			"invalid destination": {nil, func(e *overlayEnv) error {
				_, err := e.r.ApplyEdit(remoteEdit(map[string]gateway.RemoteAE{"V": {Host: "10.0.0.1 ; rm", Port: 104}}), meta("Pat Admin"), nil)
				return err
			}, nil},
			"too many destinations": {nil, func(e *overlayEnv) error {
				m := map[string]gateway.RemoteAE{}
				for i := 0; i <= maxRemoteAEs; i++ {
					m[fmt.Sprintf("V%d", i)] = gateway.RemoteAE{Host: "10.0.0.1", Port: 104}
				}
				_, err := e.r.ApplyEdit(remoteEdit(m), meta("Pat Admin"), nil)
				return err
			}, nil},
			"licence block": {func(e *overlayEnv) {
				e.r.gate = func() (bool, string) { return false, "unlicensed and not acknowledged" }
			},
				func(e *overlayEnv) error {
					_, err := e.r.ApplyEdit(allowEdit("XA1"), meta("Pat Admin"), nil)
					return err
				}, control.ErrReloadBlocked},
			"licence block on rollback": {func(e *overlayEnv) {
				e.r.gate = func() (bool, string) { return false, "unlicensed and not acknowledged" }
			},
				func(e *overlayEnv) error {
					_, err := e.r.ApplyRollback(meta("Pat Admin"), nil)
					if !withOverlayHasPrev(e) {
						return control.ErrReloadBlocked
					}
					return err
				}, control.ErrReloadBlocked},
			"config.yaml broken": {func(e *overlayEnv) { _ = os.WriteFile(e.cfg, []byte("dicom: [broken"), 0o600) },
				func(e *overlayEnv) error {
					_, err := e.r.ApplyEdit(allowEdit("XA1"), meta("Pat Admin"), nil)
					return err
				}, nil},
			"overlay cannot be written": {func(e *overlayEnv) {
				e.r.writeFn = func(string, []byte) error { return errors.New("disk full") }
			}, func(e *overlayEnv) error {
				_, err := e.r.ApplyEdit(allowEdit("XA1"), meta("Pat Admin"), nil)
				return err
			}, control.ErrConfigWrite},
			"overlay written in part": {func(e *overlayEnv) {
				e.r.writeFn = func(p string, b []byte) error { return os.WriteFile(p, b[:len(b)/2], 0o600) }
			}, func(e *overlayEnv) error {
				_, err := e.r.ApplyEdit(allowEdit("XA1"), meta("Pat Admin"), nil)
				return err
			}, control.ErrConfigWrite},
			"other content written": {func(e *overlayEnv) {
				e.r.writeFn = func(p string, _ []byte) error {
					return os.WriteFile(p, []byte("version: 1\ndicom:\n  allowed_aes: [SOMEONE]\n"), 0o600)
				}
			}, func(e *overlayEnv) error {
				_, err := e.r.ApplyEdit(allowEdit("XA1"), meta("Pat Admin"), nil)
				return err
			}, control.ErrConfigWrite},
			"gateway refuses the settings": {nil, func(e *overlayEnv) error {
				_, err := e.r.ApplyEdit(allowEdit(longRule), meta("Pat Admin"), nil)
				return err
			}, control.ErrConfigWrite},
		} {
			t.Run(fmt.Sprintf("%s/overlay=%v", name, withOverlay), func(t *testing.T) {
				e := newOverlayEnv(t)
				if withOverlay {
					for _, rules := range [][]string{{"CT1", "US1"}, {"CT1", "US1", "NM1"}} {
						if _, err := e.r.ApplyEdit(allowEdit(rules...), meta("Pat Admin"), nil); err != nil {
							t.Fatal(err)
						}
					}
				}
				if c.arrange != nil {
					c.arrange(e)
				}
				files, state := e.files(t), e.state()
				err := c.run(e)
				if err == nil || (c.want != nil && !errors.Is(err, c.want)) {
					t.Fatalf("err = %v, want %v", err, c.want)
				}
				if got := e.files(t); got != files {
					t.Errorf("files changed:\n%s\n=== want ===\n%s", got, files)
				}
				if e.state() != state {
					t.Errorf("running settings changed: %s, want %s", e.state(), state)
				}
			})
		}
	}
	// No previous version: a rollback is refused.
	e := newOverlayEnv(t)
	if _, err := e.r.PreviewRollback(); !errors.Is(err, control.ErrNoPreviousVersion) {
		t.Fatalf("rollback preview without a previous version: %v", err)
	}
	if _, err := e.r.ApplyRollback(meta("Pat Admin"), nil); !errors.Is(err, control.ErrNoPreviousVersion) {
		t.Fatalf("rollback without a previous version: %v", err)
	}
}

func withOverlayHasPrev(e *overlayEnv) bool {
	_, err := os.Stat(e.overlay + ".prev")
	return err == nil
}

// writeFileAtomic replaces a file as a whole and leaves no temporary file.
// Verifies: DCM-SRS-093
func TestWriteFileAtomic(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "new", "dir")
	p := filepath.Join(dir, "f.yaml")
	for _, content := range []string{"first\n", "second, longer content\n", "3\n"} {
		if err := writeFileAtomic(p, []byte(content)); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(p)
		st, _ := os.Stat(p)
		if err != nil || string(b) != content || st.Mode().Perm() != 0o600 {
			t.Fatalf("read back %q (%v), mode %v", b, err, st.Mode().Perm())
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("%d files in the directory, want 1", len(entries))
	}
	// A target that cannot be replaced (it is a directory) fails and leaves no temporary file.
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(filepath.Join(dir, "sub"), []byte("x")); err == nil {
		t.Fatal("writing over a directory succeeded")
	}
	if entries, _ = os.ReadDir(dir); len(entries) != 2 {
		t.Fatalf("a temporary file was left: %v", entries)
	}
}
