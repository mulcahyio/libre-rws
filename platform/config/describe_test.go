package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

type describeCfg struct {
	Admin struct {
		Addr    string        `yaml:"addr" env:"TD_ADDR"`
		Token   string        `yaml:"token" env:"TD_TOKEN"`
		Timeout time.Duration `yaml:"timeout" env:"TD_TIMEOUT"`
	} `yaml:"admin"`
	List    []string          `yaml:"list" env:"TD_LIST"`
	Peers   map[string]string `yaml:"peers"`
	NoTag   int
	Skipped string `yaml:"-"`
	hidden  string
}

// Describe lists every leaf with the value in force and where it came from:
// the environment wins over the file, the file over the default, and an
// empty environment variable does not count.
// Verifies: DCM-SRS-082
func TestDescribeSources(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	if err := os.WriteFile(p, []byte("admin:\n  addr: \":9000\"\n  token: filetoken\npeers:\n  A: x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TD_TOKEN", "envtoken")
	t.Setenv("TD_ADDR", "") // empty: not an override
	t.Setenv("TD_TIMEOUT", "")
	t.Setenv("TD_LIST", "")
	var c describeCfg
	c.Admin.Addr, c.Admin.Timeout, c.hidden = ":8081", 30*time.Second, "x"
	if err := Load(p, &c); err != nil {
		t.Fatal(err)
	}
	keys, err := FileKeys(p)
	if err != nil {
		t.Fatal(err)
	}
	if !keys["admin"] || !keys["admin.addr"] || !keys["peers.A"] || keys["list"] {
		t.Fatalf("file keys = %v", keys)
	}
	got := map[string]Setting{}
	for _, s := range Describe(&c, keys) {
		got[s.Key] = s
	}
	want := map[string]Setting{
		"admin.addr":    {Value: ":9000", Source: SourceFile},
		"admin.token":   {Value: "envtoken", Source: SourceEnv, Env: "TD_TOKEN"},
		"admin.timeout": {Value: "30s", Source: SourceDefault},
		"list":          {Source: SourceDefault},
		"peers":         {Source: SourceFile},
		"notag":         {Value: 0, Source: SourceDefault},
	}
	if len(got) != len(want) {
		t.Fatalf("settings = %v, want %d keys", got, len(want))
	}
	for k, w := range want {
		g, ok := got[k]
		if !ok || g.Source != w.Source || g.Env != w.Env {
			t.Errorf("%s = %+v, want source %s env %q", k, g, w.Source, w.Env)
		}
		if w.Value != nil && g.Value != w.Value {
			t.Errorf("%s value = %v, want %v", k, g.Value, w.Value)
		}
	}
	if m, _ := got["peers"].Value.(map[string]string); m["A"] != "x" {
		t.Errorf("peers = %v", got["peers"].Value)
	}

	// A missing file is an empty set; a broken file is an error.
	if k, err := FileKeys(filepath.Join(t.TempDir(), "none.yaml")); err != nil || len(k) != 0 {
		t.Fatalf("missing file: %v %v", k, err)
	}
	if err := os.WriteFile(p, []byte("admin: [broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := FileKeys(p); err == nil {
		t.Fatal("broken file accepted")
	}
}
