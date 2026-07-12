package main

import (
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"
)

type benchmarkConfig struct {
	HTTPListenAddress      string `env:"BENCHMARK_HTTP_LISTEN_ADDRESS"`
	RunServiceAddress      string `env:"BENCHMARK_RUN_SERVICE_ADDRESS"`
	MatchingServiceAddress string `env:"BENCHMARK_MATCHING_SERVICE_ADDRESS"`
	Namespace              string `env:"BENCHMARK_NAMESPACE"`
	TaskListName           string `env:"BENCHMARK_TASK_LIST_NAME"`
	// WorkerRunConcurrency caps the number of in-flight runs this worker
	// process executes concurrently (forwarded to dex.WorkerOptions.
	// RunConcurrency). Throughput is scaled out by adding more replicas
	// of this benchmark process and/or by tuning the server-side
	// `tasklist.numWritePartitions` / `numReadPartitions` so the tasklist
	// fan-in is not the bottleneck.
	WorkerRunConcurrency int    `env:"BENCHMARK_WORKER_RUN_CONCURRENCY"`
	TriggerToken         string `env:"BENCHMARK_TRIGGER_TOKEN"`
}

func defaultBenchmarkConfig() benchmarkConfig {
	return benchmarkConfig{
		HTTPListenAddress:    ":9123",
		Namespace:            "default",
		TaskListName:         "benchmark-workers",
		WorkerRunConcurrency: 100,
	}
}

func loadBenchmarkConfig() (benchmarkConfig, error) {
	cfg := defaultBenchmarkConfig()
	if err := applyEnvOverrides(&cfg); err != nil {
		return benchmarkConfig{}, err
	}
	return cfg, nil
}

// ============ env config loader (copied from server/config/env.go) ============

var durationType = reflect.TypeOf(time.Duration(0))

// applyEnvOverrides walks a struct and sets fields from environment variables
// based on the `env` struct tag.
func applyEnvOverrides(cfg interface{}) error {
	return applyEnvOverridesFromTags(reflect.ValueOf(cfg))
}

func applyEnvOverridesFromTags(v reflect.Value) error {
	if !v.IsValid() {
		return nil
	}
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil
		}
		return applyEnvOverridesFromTags(v.Elem())
	}
	if v.Kind() != reflect.Struct {
		return nil
	}

	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		fieldVal := v.Field(i)
		fieldType := t.Field(i)

		if envTag := fieldType.Tag.Get("env"); envTag != "" {
			if err := applyEnvValue(fieldVal, fieldType.Name, envTag); err != nil {
				return err
			}
		}

		switch fieldVal.Kind() {
		case reflect.Pointer:
			if fieldVal.IsNil() {
				continue
			}
			if fieldVal.Type().Elem().Kind() == reflect.Struct {
				if err := applyEnvOverridesFromTags(fieldVal); err != nil {
					return err
				}
			}
		case reflect.Struct:
			if err := applyEnvOverridesFromTags(fieldVal); err != nil {
				return err
			}
		}
	}
	return nil
}

func applyEnvValue(fieldVal reflect.Value, fieldName string, envTag string) error {
	keys := strings.Split(envTag, ",")
	for _, key := range keys {
		trimmed := strings.TrimSpace(key)
		if trimmed == "" {
			continue
		}
		if envVal, ok := os.LookupEnv(trimmed); ok {
			if err := setFieldValue(fieldVal, envVal); err != nil {
				return fmt.Errorf("failed to apply env %q to field %s: %w", trimmed, fieldName, err)
			}
			return nil
		}
	}
	return nil
}

func setFieldValue(fieldVal reflect.Value, raw string) error {
	if !fieldVal.CanSet() {
		return fmt.Errorf("field is not settable")
	}

	switch fieldVal.Kind() {
	case reflect.String:
		fieldVal.SetString(raw)
		return nil
	case reflect.Bool:
		v, err := strconv.ParseBool(raw)
		if err != nil {
			return fmt.Errorf("invalid bool value %q: %w", raw, err)
		}
		fieldVal.SetBool(v)
		return nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if fieldVal.Type() == durationType {
			if v, err := time.ParseDuration(raw); err == nil {
				fieldVal.SetInt(int64(v))
				return nil
			}
			if v, err := strconv.ParseFloat(raw, 64); err == nil {
				fieldVal.SetInt(int64(v * float64(time.Second)))
				return nil
			}
			return fmt.Errorf("invalid duration value %q", raw)
		}
		v, err := strconv.ParseInt(raw, 10, fieldVal.Type().Bits())
		if err != nil {
			return fmt.Errorf("invalid int value %q: %w", raw, err)
		}
		fieldVal.SetInt(v)
		return nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v, err := strconv.ParseUint(raw, 10, fieldVal.Type().Bits())
		if err != nil {
			return fmt.Errorf("invalid uint value %q: %w", raw, err)
		}
		fieldVal.SetUint(v)
		return nil
	case reflect.Float32, reflect.Float64:
		v, err := strconv.ParseFloat(raw, fieldVal.Type().Bits())
		if err != nil {
			return fmt.Errorf("invalid float value %q: %w", raw, err)
		}
		fieldVal.SetFloat(v)
		return nil
	default:
		return fmt.Errorf("unsupported field type: %s", fieldVal.Kind())
	}
}
