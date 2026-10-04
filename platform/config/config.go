// Package config loads module configuration from a YAML file and then applies
// environment-variable overrides. Every RWS module uses the same mechanism, so
// any setting can be supplied by file (bare-metal installs) or by environment
// (containers, Kubernetes) without code changes.
//
// Environment overrides are declared with an `env` struct tag on leaf fields.
// Nested structs are walked automatically. Supported leaf kinds: string, bool,
// ints, uints, floats, time.Duration and []string (comma separated).
package config

import (
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Load reads path (if non-empty and present) into dst, then applies env
// overrides. dst must be a pointer to a struct already populated with defaults.
func Load(path string, dst any) error {
	if path != "" {
		b, err := os.ReadFile(path)
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
		key := f.Tag.Get("env")
		if key == "" {
			continue
		}
		raw, ok := os.LookupEnv(key)
		if !ok {
			continue
		}
		if err := setValue(fv, raw); err != nil {
			return fmt.Errorf("config: env %s: %w", key, err)
		}
	}
	return nil
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
