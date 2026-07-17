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

package metrics

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/superdurable/dex/server/config"
	"github.com/superdurable/dex/server/internal/log"
	"github.com/superdurable/dex/server/internal/log/tag"
	tally "github.com/uber-go/tally/v4"
	"github.com/uber-go/tally/v4/m3"
)

var registeredMetricNames = map[string]bool{}
var registeredCounter = map[string]*counterImpl{}
var registeredGauge = map[string]*gaugeImpl{}
var registeredLatency = map[string]*latencyImpl{}
var registeredHistogram = map[string]*histogramImpl{}

var metricReporter *metricsReporterImpl

type metricsReporterImpl struct {
	scope           tally.Scope
	closer          io.Closer
	maxEmittingTier int
	logger          log.Logger
}

func Initialize(
	ctx context.Context, cfg *config.MetricsConfig, logger log.Logger,
) (shutdownFn func(context.Context) error, err error) {
	if cfg == nil {
		panic("metrics config is nil")
	}
	if logger == nil {
		panic("metrics logger is nil")
	}

	if cfg.Provider == config.MetricsProviderNone {
		return func(context.Context) error { return nil }, nil
	}

	metricReporter, err = newMetricsReporter(ctx, cfg, logger)
	if err != nil {
		return nil, err
	}

	if err := finalizeRegisteredMetrics(metricReporter); err != nil {
		// Prefer the finalize error; Close is best-effort cleanup.
		_ = metricReporter.Close(ctx)
		metricReporter = nil
		return nil, err
	}

	return metricReporter.Close, nil
}

func newMetricsReporter(
	_ context.Context, cfg *config.MetricsConfig, logger log.Logger,
) (*metricsReporterImpl, error) {
	switch cfg.Provider {
	case config.MetricsProviderM3:
		return newM3Reporter(cfg, logger)
	default:
		return nil, fmt.Errorf("unsupported metrics provider: %s", cfg.Provider)
	}
}

func newM3Reporter(cfg *config.MetricsConfig, logger log.Logger) (*metricsReporterImpl, error) {
	if cfg.M3 == nil {
		cfg.M3 = &config.M3Config{}
	}
	hostPorts := cfg.HostPorts
	if len(hostPorts) == 0 && cfg.M3.HostPort != "" {
		hostPorts = []string{cfg.M3.HostPort}
	}
	if len(hostPorts) == 0 {
		return nil, fmt.Errorf("m3 metrics require hostPorts or m3.hostPort")
	}

	reporter, err := m3.NewReporter(m3.Options{
		HostPorts:          hostPorts,
		Service:            cfg.Service,
		Env:                cfg.Env,
		CommonTags:         cfg.CommonTags,
		MaxQueueSize:       cfg.M3.Queue,
		MaxPacketSizeBytes: cfg.M3.PacketSize,
		IncludeHost:        cfg.IncludeHost,
	})
	if err != nil {
		return nil, fmt.Errorf("create m3 reporter: %w", err)
	}

	scope, closer := tally.NewRootScope(tally.ScopeOptions{
		Prefix:          cfg.MetricPrefix,
		CachedReporter:  reporter,
		Separator:       tally.DefaultSeparator,
		SanitizeOptions: &m3.DefaultSanitizerOpts,
	}, time.Second)

	logger.Info("initialized m3 metrics reporter",
		tag.Address(hostPorts[0]))

	return &metricsReporterImpl{
		scope:           scope,
		closer:          closer,
		maxEmittingTier: cfg.MaxEmittingTier,
		logger:          logger,
	}, nil
}

func finalizeRegisteredMetrics(reporter *metricsReporterImpl) error {
	for name, counter := range registeredCounter {
		reporter.finalizeCounter(counter, name)
	}
	for name, histogram := range registeredHistogram {
		reporter.finalizeHistogram(histogram, name)
	}
	for name, latency := range registeredLatency {
		reporter.finalizeLatency(latency, name)
	}
	for name, gauge := range registeredGauge {
		reporter.finalizeGauge(gauge, name)
	}
	return nil
}

func (m *metricsReporterImpl) finalizeCounter(counter *counterImpl, name string) {
	if counter.tier > m.maxEmittingTier {
		return
	}
	counter.name = name
	counter.scope = m.scope
}

func (m *metricsReporterImpl) finalizeHistogram(histogram *histogramImpl, name string) {
	if histogram.tier > m.maxEmittingTier {
		return
	}
	histogram.name = name
	histogram.scope = m.scope
}

func (m *metricsReporterImpl) finalizeLatency(latency *latencyImpl, name string) {
	if latency.tier > m.maxEmittingTier {
		return
	}
	latency.name = name
	latency.scope = m.scope
}

func (m *metricsReporterImpl) finalizeGauge(gauge *gaugeImpl, name string) {
	if gauge.tier > m.maxEmittingTier {
		return
	}
	gauge.name = name
	gauge.scope = m.scope
}

func (m *metricsReporterImpl) Close(_ context.Context) error {
	if m.closer == nil {
		return nil
	}
	return m.closer.Close()
}

type counterImpl struct {
	tier  int
	name  string
	scope tally.Scope
}

var _ Counter = (*counterImpl)(nil)

func (c *counterImpl) Inc(tags ...Tag) {
	c.IncBy(1, tags...)
}

func (c *counterImpl) IncBy(delta int, tags ...Tag) {
	if c.scope == nil {
		return
	}
	c.scope.Tagged(tagsToMap(tags)).Counter(c.name).Inc(int64(delta))
}

type gaugeImpl struct {
	tier  int
	name  string
	scope tally.Scope
}

var _ Gauge = (*gaugeImpl)(nil)

func (g *gaugeImpl) Record(val int64, tags ...Tag) {
	if g.scope == nil {
		return
	}
	g.scope.Tagged(tagsToMap(tags)).Gauge(g.name).Update(float64(val))
}

type histogramImpl struct {
	tier  int
	name  string
	scope tally.Scope
}

var _ Histogram = (*histogramImpl)(nil)

func (h *histogramImpl) Record(val float64, tags ...Tag) {
	if h.scope == nil {
		return
	}
	h.scope.Tagged(tagsToMap(tags)).Histogram(h.name, tally.DefaultBuckets).RecordValue(val)
}

type latencyImpl struct {
	tier  int
	name  string
	scope tally.Scope
}

var _ Latency = (*latencyImpl)(nil)

func (l *latencyImpl) Record(val time.Duration, tags ...Tag) {
	if l.scope == nil {
		return
	}
	l.scope.Tagged(tagsToMap(tags)).Timer(l.name).Record(val)
}

func tagsToMap(tags []Tag) map[string]string {
	if len(tags) == 0 {
		return nil
	}
	out := make(map[string]string, len(tags))
	for _, metricTag := range tags {
		out[string(metricTag.Key)] = string(metricTag.Value)
	}
	return out
}
