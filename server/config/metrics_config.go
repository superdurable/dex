// Copyright (c) 2023-2026 Super Durable, Inc.
//
// This file is part of Dex
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program. If not, see <https://www.gnu.org/licenses/>.

package config

type MetricsProvider string

const (
	// MetricsProviderNone disables metric emission. Default.
	MetricsProviderNone MetricsProvider = "none"
	// MetricsProviderM3 emits metrics to M3 via the tally UDP reporter.
	MetricsProviderM3 MetricsProvider = "m3"
)

type MetricsConfig struct {
	// MetricPrefix is prepended to every metric name. Default: "dex_".
	MetricPrefix string `yaml:"metricPrefix" env:"METRIC_PREFIX"`
	// MaxEmittingTier is the maximum metric tier to emit (1–4).
	// Higher tiers are registered as no-ops. Default: 2 (Info).
	MaxEmittingTier int `yaml:"maxEmittingTier" env:"METRIC_MAX_EMITTING_TIER"`
	// Provider selects the metrics backend. Default: "none".
	Provider MetricsProvider `yaml:"provider" env:"METRIC_PROVIDER"`

	// HostPorts are aggregator addresses. When set, overrides M3.HostPort.
	HostPorts []string `yaml:"hostPorts"`
	// Service is the required service common tag. Default: "dex".
	Service string `yaml:"service" env:"METRIC_SERVICE"`
	// Env is the required env common tag. Default: "development".
	Env string `yaml:"env" env:"METRIC_ENV"`
	// CommonTags are added to every metric this process emits.
	CommonTags map[string]string `yaml:"tags"`
	// IncludeHost adds the local hostname as a common tag. Default: false.
	IncludeHost bool `yaml:"includeHost" env:"METRIC_INCLUDE_HOST"`

	// M3 configures the M3 reporter when Provider is "m3".
	// Default: nil (only required when Provider is "m3").
	M3 *M3Config `yaml:"m3"`
}

// M3Config configures the tally M3 UDP reporter.
type M3Config struct {
	// HostPort is the M3 aggregator address (host:port).
	// Used when MetricsConfig.HostPorts is empty. No default; required for m3.
	HostPort string `yaml:"hostPort" env:"METRIC_M3_HOST_PORT"`
	// Queue is the max metric queue size. Default: 0 (tally default).
	Queue int `yaml:"queue" env:"METRIC_M3_QUEUE"`
	// PacketSize is the max UDP packet size in bytes. Default: 0 (tally default).
	PacketSize int32 `yaml:"packetSize" env:"METRIC_M3_PACKET_SIZE"`
}

func DefaultMetricsConfig() MetricsConfig {
	return MetricsConfig{
		Provider:        MetricsProviderNone,
	}
}
