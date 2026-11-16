package config

import (
	"fmt"
	"os"
	"reflect"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Where a setting's value came from.
const (
	SourceDefault = "default"
	SourceFile    = "file"
	SourceEnv     = "env"
	// SourceOverlay: a file the module itself writes (LibreDCM's
	// configuration overlay). Describe never returns it; the module sets it.
	SourceOverlay = "overlay"
)

// Setting is one leaf of a configuration struct, as it is running.
type Setting struct {
	Key    string // dotted YAML path, e.g. "dicom.ae_title"
	Value  any    // the value in force (durations as text)
	Source string // SourceDefault, SourceFile or SourceEnv
	Env    string // the environment variable that set it, when Source is SourceEnv
}

// FileKeys returns the dotted paths of every key set in the YAML file at
// path, including the paths of their parents. A missing file gives an empty
// set, as in Load.
func FileKeys(path string) (map[string]bool, error) {
	keys := map[string]bool{}
	if path == "" {
		return keys, nil
	}
	b, err := os.ReadFile(path) // #nosec G304 -- path is the administrator's -config flag
	if os.IsNotExist(err) {
		return keys, nil
	}
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	var walk func(prefix string, m map[string]any)
	walk = func(prefix string, m map[string]any) {
		for k, v := range m {
			key := prefix + k
			keys[key] = true
			if sub, ok := v.(map[string]any); ok {
				walk(key+".", sub)
			}
		}
	}
	walk("", doc)
	return keys, nil
}

// Describe lists every leaf setting of cfg (a struct or a pointer to one)
// with its value and source. cfg must be the struct Load filled; fileKeys is
// the result of FileKeys for the same file. An environment variable wins over
// the file, as in Load. Values are returned as they are, secrets included:
// the caller decides what may be shown.
func Describe(cfg any, fileKeys map[string]bool) []Setting {
	var out []Setting
	describe(reflect.ValueOf(cfg), "", fileKeys, &out)
	return out
}

func describe(v reflect.Value, prefix string, fileKeys map[string]bool, out *[]Setting) {
	if v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return
	}
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		if name == "-" {
			continue
		}
		if name == "" {
			name = strings.ToLower(f.Name)
		}
		key := prefix + name
		fv := v.Field(i)
		if fv.Kind() == reflect.Struct && fv.Type() != durationType {
			describe(fv, key+".", fileKeys, out)
			continue
		}
		s := Setting{Key: key, Value: fv.Interface(), Source: SourceDefault}
		if fv.Type() == durationType {
			s.Value = fv.Interface().(fmt.Stringer).String()
		}
		if fileKeys[key] {
			s.Source = SourceFile
		}
		if tag := f.Tag.Get("env"); tag != "" {
			if envName, _, found := lookupEnv(tag); found {
				s.Source, s.Env = SourceEnv, envName
			}
		}
		*out = append(*out, s)
	}
}
