package main

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/mulcahyio/libre-rws/modules/dicom/internal/gateway"
)

type fakeTarget struct {
	remotes map[string]gateway.RemoteAE
	allowed []string
}

func (f *fakeTarget) SetRemoteAEs(m map[string]gateway.RemoteAE) { f.remotes = m }
func (f *fakeTarget) SetAllowedAEs(a []string) error {
	for _, e := range a {
		if e == "" || len(e) > 40 {
			return os.ErrInvalid
		}
	}
	f.allowed = a
	return nil
}

func TestReloadAppliesLiveSettings(t *testing.T) {
	t.Setenv("LIBRE_DCM_REMOTE_AES", "ENVVIEW=10.0.0.9:104")
	t.Setenv("LIBRE_DCM_ALLOWED_AES", "")
	p := filepath.Join(t.TempDir(), "config.yaml")
	write := func(s string) {
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(`dicom:
  allowed_aes: [CT1, "MR1@192.168.1.20"]
  remote_aes:
    VIEWER: {host: 127.0.0.1, port: 11113}
`)
	ft := &fakeTarget{}
	r := &reloader{path: p, gw: ft, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := r.reload(); err != nil {
		t.Fatal(err)
	}
	if len(ft.allowed) != 2 || ft.allowed[1] != "MR1@192.168.1.20" {
		t.Fatalf("allowed = %v", ft.allowed)
	}
	if ft.remotes["VIEWER"].Port != 11113 || ft.remotes["ENVVIEW"].Host != "10.0.0.9" {
		t.Fatalf("remotes = %v (file and environment should both apply)", ft.remotes)
	}

	// A broken file keeps the current settings.
	write("dicom: [this is not valid")
	if err := r.reload(); err == nil {
		t.Fatal("broken config accepted")
	}
	if len(ft.allowed) != 2 || len(ft.remotes) != 2 {
		t.Fatal("settings changed after a failed reload")
	}
}
