// Copyright (c) 2025 superdurable
// SPDX-License-Identifier: MIT

package config

type MetricsProvider string

const (
	MetricsProviderNone       MetricsProvider = "none"
	MetricsProviderPrometheus MetricsProvider = "prometheus"
	MetricsProviderDatadog    MetricsProvider = "datadog"
)

type MetricsConfig struct {
	// MetricPrefix is prefix of the metrics
	MetricPrefix string `yaml:"metricPrefix" env:"METRIC_PREFIX"`
	// MaxEmittingTier is the maximum metric tier to emit (1~4).
	// 1: Required for monitoring and understand the health of the system
	// 2: Required for minimum production grade operation
	// 3: Needed for basic debug & troubleshooting
	// 4: Needed for very deep debug & troubleshooting
	MaxEmittingTier int             `yaml:"maxEmittingTier" env:"METRIC_MAX_EMITTING_TIER"`
	Provider        MetricsProvider `yaml:"provider"`

	Prometheus *PrometheusConfig `yaml:"prometheus"`
	Datadog    *DatadogConfig    `yaml:"datadog"`

	HostId string `yaml:"hostId" env:"HOSTNAME"`
	Env    string `yaml:"env" env:"ENV"`
}

type DatadogConfig struct {
	APIKey   string `yaml:"apiKey" env:"DD_API_KEY" sensitive:"true"`
	Endpoint string `yaml:"endpoint" env:"DD_ENDPOINT"`
}

type PrometheusConfig struct {
	// ListenAddress is the bind address for the HTTP endpoint serving /metrics.
	// Default: ":9090"
	ListenAddress string `yaml:"listenAddress" env:"DEX_METRICS_LISTEN_ADDRESS"`
}

func DefaultMetricsConfig() MetricsConfig {
	return MetricsConfig{
		MetricPrefix:    "dex_",
		MaxEmittingTier: 2,
		Provider:        MetricsProviderNone,
		Prometheus: &PrometheusConfig{
			ListenAddress: ":9090",
		},
	}
}
