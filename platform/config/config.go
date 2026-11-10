// Package config loads module configuration from a YAML file and then applies
// environment-variable overrides. Every RWS module uses the same mechanism, so
// any setting can be supplied by file (bare-metal installs) or by environment
// (containers, Kubernetes) without code changes.
//
// Environment overrides are declared with an `env` struct tag on leaf fields.
// Nested structs are walked automatically. Supported leaf kinds: string, bool,
// ints, uints, floats, time.Duration and []string (comma separated).
// A variable that is set but empty is treated as unset. A tag may list
// several names ("NEW,OLD"): the first one set wins, so renamed settings keep
// working under their old names.
package config

import (
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.yaml.in/yaml/v3"
)

// Load reads path (if non-empty and present) into dst, then applies env
// overrides. dst must be a pointer to a struct already populated with defaults.
func Load(path string, dst any) error {
	if path != "" {
		b, err := os.ReadFile(path) // #nosec G304 -- path is the administrator's -config flag
		switch {
		case err == nil:
			if err := yaml.Unmarshal(b, dst); err != nil {
				return fmt.Errorf("config: parse %s: %w", path, err)
			}
		case os.IsNotExist(err):
			// A missing file is allowed: defaults + env is a valid setup.
		default:
			return fmt.Errorf("config: read %s: %w", path, err)
		}
	}
	return applyEnv(reflect.ValueOf(dst))
}

var durationType = reflect.TypeOf(time.Duration(0))

func applyEnv(v reflect.Value) error {
	if v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return nil
	}
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		fv := v.Field(i)
		if !f.IsExported() {
			continue
		}
		if fv.Kind() == reflect.Struct && fv.Type() != durationType {
			if err := applyEnv(fv.Addr()); err != nil {
				return err
			}
			continue
		}
		tag := f.Tag.Get("env")
		if tag == "" {
			continue
		}
		key, raw, found := lookupEnv(tag)
		if !found {
			continue
		}
		if err := setValue(fv, raw); err != nil {
			return fmt.Errorf("config: env %s: %w", key, err)
		}
	}
	return nil
}

// Prefix replaces "{P}" in env tags, so shared chassis settings take the
// running module's prefix (LibreDCM sets "LIBRE_DCM_": {P}ADMIN_TOKEN reads
// LIBRE_DCM_ADMIN_TOKEN). Set it before Load.
var Prefix = "RWS_"

var (
	deprecatedMu   sync.Mutex
	deprecatedUsed = map[string]string{}
)

// lookupEnv tries the comma-separated names in tag in order: the current
// name first, then older names kept for backward compatibility. A variable
// that is set but empty counts as unset: compose files pass every variable
// through (VAR: ${VAR:-}), and an empty one must not wipe a config-file value.
func lookupEnv(tag string) (key, raw string, found bool) {
	names := strings.Split(tag, ",")
	for i, n := range names {
		n = strings.ReplaceAll(strings.TrimSpace(n), "{P}", Prefix)
		if v, ok := os.LookupEnv(n); ok && strings.TrimSpace(v) != "" {
			if i > 0 {
				deprecatedMu.Lock()
				deprecatedUsed[n] = strings.ReplaceAll(strings.TrimSpace(names[0]), "{P}", Prefix)
				deprecatedMu.Unlock()
			}
			return n, v, true
		}
	}
	return "", "", false
}

// Getenv reads one setting the same way (current name first, then legacy
// names), for values not held in a config struct.
func Getenv(names ...string) string {
	_, v, _ := lookupEnv(strings.Join(names, ","))
	return v
}

// DeprecatedInUse returns legacy variable names that were used, mapped to
// their replacements, so the module can log a warning.
func DeprecatedInUse() map[string]string {
	deprecatedMu.Lock()
	defer deprecatedMu.Unlock()
	out := make(map[string]string, len(deprecatedUsed))
	for k, v := range deprecatedUsed {
		out[k] = v
	}
	return out
}

func setValue(fv reflect.Value, raw string) error {
	if fv.Type() == durationType {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return err
		}
		fv.SetInt(int64(d))
		return nil
	}
	switch fv.Kind() {
	case reflect.String:
		fv.SetString(raw)
	case reflect.Bool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return err
		}
		fv.SetBool(b)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return err
		}
		fv.SetInt(n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			return err
		}
		fv.SetUint(n)
	case reflect.Float32, reflect.Float64:
		n, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return err
		}
		fv.SetFloat(n)
	case reflect.Slice:
		if fv.Type().Elem().Kind() != reflect.String {
			return fmt.Errorf("unsupported slice type %s", fv.Type())
		}
		var parts []string
		for _, p := range strings.Split(raw, ",") {
			if p = strings.TrimSpace(p); p != "" {
				parts = append(parts, p)
			}
		}
		fv.Set(reflect.ValueOf(parts))
	default:
		return fmt.Errorf("unsupported kind %s", fv.Kind())
	}
	return nil
}
