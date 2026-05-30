// Package config loads service configuration from environment variables.
// Each service defines its own config struct; this package provides the loader.
//
// Usage:
//
//	type Config struct {
//	    ListenAddr string `env:"LISTEN_ADDR" default:":8080"`
//	    DatabaseURL string `env:"DATABASE_URL,required"`
//	    SomeEmbed  pkg.SomeConfig  // embedded structs are walked recursively
//	}
//	cfg, err := config.Load[Config]()
package config

import (
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// Load reads environment variables into a struct of type T.
// Field tags:
//   - `env:"VAR_NAME"` — env var name (required)
//   - `env:"VAR_NAME,required"` — error if missing/empty
//   - `default:"value"` — used when env var is unset
//
// Embedded (anonymous) structs are walked recursively.
// Supported field types: string, int*, bool.
func Load[T any]() (T, error) {
	var cfg T
	v := reflect.ValueOf(&cfg).Elem()
	if err := loadStruct(v); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func loadStruct(v reflect.Value) error {
	t := v.Type()
	for i := range t.NumField() {
		field := t.Field(i)
		fv := v.Field(i)

		// Recurse into anonymous (embedded) struct fields.
		if field.Anonymous && fv.Kind() == reflect.Struct {
			if err := loadStruct(fv); err != nil {
				return err
			}
			continue
		}

		tag := field.Tag.Get("env")
		if tag == "" {
			continue
		}

		parts := strings.SplitN(tag, ",", 2)
		envKey := parts[0]
		required := len(parts) == 2 && parts[1] == "required"

		raw, ok := os.LookupEnv(envKey)
		if !ok || raw == "" {
			def := field.Tag.Get("default")
			if def != "" {
				raw = def
				ok = true
			}
		}

		if (!ok || raw == "") && required {
			return fmt.Errorf("required env var %s is not set", envKey)
		}
		if !ok || raw == "" {
			continue
		}

		if err := setField(fv, raw); err != nil {
			return fmt.Errorf("env var %s: %w", envKey, err)
		}
	}
	return nil
}

func setField(fv reflect.Value, raw string) error {
	if fv.Type() == reflect.TypeOf(time.Duration(0)) {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return fmt.Errorf("expected duration (e.g. 10m, 1h30s), got %q", raw)
		}
		fv.SetInt(int64(d))
		return nil
	}
	switch fv.Kind() {
	case reflect.String:
		fv.SetString(raw)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return fmt.Errorf("expected integer, got %q", raw)
		}
		fv.SetInt(n)
	case reflect.Bool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return fmt.Errorf("expected bool, got %q", raw)
		}
		fv.SetBool(b)
	default:
		return fmt.Errorf("unsupported field type %s", fv.Kind())
	}
	return nil
}
