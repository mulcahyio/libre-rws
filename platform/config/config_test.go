package config

import (
	"os"
	"path/filepath"
	"testing"
)

type testCfg struct {
	Name  string   `yaml:"name" env:"T_NAME"`
	List  []string `yaml:"list" env:"T_LIST"`
	Count int      `yaml:"count" env:"T_COUNT"`
}

func TestEnvOverridesAndEmptyIsUnset(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	if err := os.WriteFile(p, []byte("name: file\nlist: [a, b]\ncount: 3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("T_NAME", "env")
	t.Setenv("T_LIST", "")  // empty: keep the file's list
	t.Setenv("T_COUNT", " ") // blank: keep the file's value (and no parse error)
	var c testCfg
	if err := Load(p, &c); err != nil {
		t.Fatal(err)
	}
	if c.Name != "env" || len(c.List) != 2 || c.Count != 3 {
		t.Fatalf("got %+v", c)
	}
	t.Setenv("T_LIST", "x, y ,z")
	if err := Load(p, &c); err != nil {
		t.Fatal(err)
	}
	if len(c.List) != 3 || c.List[1] != "y" {
		t.Fatalf("list = %v", c.List)
	}
}
