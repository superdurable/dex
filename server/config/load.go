package config

import (
	"bytes"
	"fmt"
	"os"
	"reflect"

	"gopkg.in/yaml.v3"
)

// EnvConfigPath is the environment variable used to locate a YAML config file.
const EnvConfigPath = "DEX_CONFIG_PATH"

// Load reads the default config, optionally overlays a YAML config file, then
// applies environment variable overrides.
func Load(configPath string) (Config, error) {
	cfg := DefaultConfig()
	if configPath != "" {
		if err := LoadFile(configPath, &cfg); err != nil {
			return Config{}, err
		}
	}
	if err := ApplyEnvOverrides(&cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// LoadFile overlays a YAML file onto cfg.
func LoadFile(path string, cfg *Config) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config file %q: %w", path, err)
	}

	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(cfg); err != nil {
		return fmt.Errorf("decode config file %q: %w", path, err)
	}
	expandEnvTemplates(reflect.ValueOf(cfg))
	return nil
}

func expandEnvTemplates(v reflect.Value) {
	if !v.IsValid() {
		return
	}

	switch v.Kind() {
	case reflect.Pointer:
		if !v.IsNil() {
			expandEnvTemplates(v.Elem())
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Field(i).CanSet() || v.Field(i).Kind() == reflect.Pointer || v.Field(i).Kind() == reflect.Struct || v.Field(i).Kind() == reflect.Slice {
				expandEnvTemplates(v.Field(i))
			}
		}
	case reflect.String:
		if v.CanSet() {
			v.SetString(os.ExpandEnv(v.String()))
		}
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			expandEnvTemplates(v.Index(i))
		}
	}
}
