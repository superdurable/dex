package metrics

import (
	"context"
	"errors"
	"maps"
	"time"

	"github.com/superdurable/dex/server/common/log"
	"github.com/superdurable/dex/server/common/log/tag"
	"github.com/superdurable/dex/server/config"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
)

// it will be flipped to false if Init(...) is successful to Initialize all the registry
var noopOnly bool = true

var registryNames = map[string]bool{}               // to check if the metric name is already registered
var registryCounter = map[string]*counterImpl{}     // to store the counterImpl instance
var registryGauge = map[string]*gaugeImpl{}         // to store the gaugeImpl instance
var registryLatency = map[string]*latencyImpl{}     // to store the latencyImpl instance
var registryHistogram = map[string]*histogramImpl{} // to store the histogramImpl instance

var metricReporter *metricsReporterImpl

const serviceName = "dex-server"
const metricEmissionTimeout = 1 * time.Second

type metricsReporterImpl struct {
	defaultProvider *sdkmetric.MeterProvider
	publicProvider  *sdkmetric.MeterProvider

	defaultMeter metric.Meter
	publicMeter  metric.Meter

	promRegistry *prometheus.Registry

	metricPrefix    string
	metricBaseTags  map[string]string
	maxEmittingTier int

	logger log.Logger
}

func Initialize(ctx context.Context, cfg *config.MetricsConfig, logger log.Logger) error {
	if cfg.Provider == config.MetricsProviderNone {
		return nil
	}
	if metricReporter != nil {
		if err := Close(ctx); err != nil {
			return err
		}
	}

	var err error
	metricReporter, err = newMetricsReporter(ctx, cfg, logger)
	if err != nil {
		return err
	}

	for name, counter := range registryCounter {
		err := metricReporter.initCounter(counter, name)
		if err != nil {
			return err
		}
	}
	for name, gauge := range registryGauge {
		err := metricReporter.initGauge(gauge, name)
		if err != nil {
			return err
		}
	}
	for name, histogram := range registryHistogram {
		err := metricReporter.initHistogram(histogram, name)
		if err != nil {
			return err
		}
	}
	for name, latency := range registryLatency {
		err := metricReporter.initLatency(latency, name)
		if err != nil {
			return err
		}
	}

	noopOnly = false
	return nil
}

func newMetricsReporter(ctx context.Context, cfg *config.MetricsConfig, logger log.Logger) (*metricsReporterImpl, error) {
	// Use NewSchemaless to avoid schema URL conflicts with resource.Default()
	customRes := resource.NewSchemaless()
	res, err := resource.Merge(
		resource.Default(),
		customRes,
	)
	if err != nil {
		return nil, err
	}

	var defaultProvider *sdkmetric.MeterProvider
	var promRegistry *prometheus.Registry
	if cfg.Provider == config.MetricsProviderPrometheus {
		defaultProvider, promRegistry, err = newPrometheusProvider(res)
		if err != nil {
			return nil, err
		}
	}
	if cfg.Provider == config.MetricsProviderDatadog {
		defaultProvider, err = newDatadogProvider(ctx, cfg.Datadog, res)
		if err != nil {
			return nil, err
		}
	}

	logger.Info("Metrics reporter initialized", tag.Provider(string(cfg.Provider)))

	var publicProvider *sdkmetric.MeterProvider
	if cfg.Provider == config.MetricsProviderPrometheus {
		publicProvider = defaultProvider
	} else {
		publicProvider, promRegistry, err = newPrometheusProvider(res)
		if err != nil {
			return nil, err
		}
	}

	// these tags must be added here and not in the resource above, otherwise prometheus will not register as attributes
	metricBaseTags := make(map[string]string, 2)
	if cfg.Env != "" {
		metricBaseTags["env"] = cfg.Env
	}

	var defaultMeter metric.Meter
	if defaultProvider != nil {
		defaultMeter = defaultProvider.Meter(serviceName)
	}
	var publicMeter metric.Meter
	if publicProvider != nil {
		publicMeter = publicProvider.Meter(serviceName)
	}

	return &metricsReporterImpl{
		defaultProvider: defaultProvider,
		publicProvider:  publicProvider,

		defaultMeter: defaultMeter,
		publicMeter:  publicMeter,

		promRegistry: promRegistry,

		metricPrefix:    cfg.MetricPrefix,
		metricBaseTags:  metricBaseTags,
		maxEmittingTier: cfg.MaxEmittingTier,
		logger:          logger,
	}, nil
}

func (m *metricsReporterImpl) initCounter(counter *counterImpl, name string) error {
	if counter.tier > m.maxEmittingTier {
		return nil
	}
	meter := m.defaultMeter
	if counter.isPublic {
		meter = m.publicMeter
	}

	metricCounter, err := meter.Int64Counter(m.metricPrefix + name)
	if err != nil {
		return err
	}

	counter.metricCounter = metricCounter

	if len(m.metricBaseTags) > 0 {
		counter.baseTags = m.metricBaseTags
	}

	return nil
}

func (m *metricsReporterImpl) initGauge(gauge *gaugeImpl, name string) error {
	if gauge.tier > m.maxEmittingTier {
		return nil
	}
	meter := m.defaultMeter
	if gauge.isPublic {
		meter = m.publicMeter
	}

	metricGauge, err := meter.Int64Gauge(m.metricPrefix + name)
	if err != nil {
		return err
	}

	gauge.metricGauge = metricGauge

	if len(m.metricBaseTags) > 0 {
		gauge.baseTags = m.metricBaseTags
	}

	return nil
}

func (m *metricsReporterImpl) initHistogram(histogram *histogramImpl, name string) error {
	if histogram.tier > m.maxEmittingTier {
		return nil
	}
	meter := m.defaultMeter
	if histogram.isPublic {
		meter = m.publicMeter
	}

	metricHistogram, err := meter.Float64Histogram(m.metricPrefix + name)
	if err != nil {
		return err
	}

	histogram.metricHistogram = metricHistogram

	if len(m.metricBaseTags) > 0 {
		histogram.baseTags = m.metricBaseTags
	}

	return nil
}

func (m *metricsReporterImpl) initLatency(latency *latencyImpl, name string) error {
	if latency.tier > m.maxEmittingTier {
		return nil
	}
	meter := m.defaultMeter
	if latency.isPublic {
		meter = m.publicMeter
	}

	metricHistogram, err := meter.Float64Histogram(m.metricPrefix + name)
	if err != nil {
		return err
	}

	latency.metricHistogram = metricHistogram

	latency.baseTags = make(map[string]string)
	maps.Copy(latency.baseTags, m.metricBaseTags)
	return nil
}

// counterImpl implements Counter interface
type counterImpl struct {
	tier          int
	isPublic      bool
	metricCounter metric.Int64Counter
	baseTags      map[string]string
}

var _ Counter = (*counterImpl)(nil)

func (c *counterImpl) Inc(tags ...Tag) {
	if noopOnly {
		return
	}
	c.IncBy(1, tags...)
}

func (c *counterImpl) IncBy(delta int, tags ...Tag) {
	if noopOnly || c.metricCounter == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), metricEmissionTimeout)
	defer cancel()

	attrs := buildAttributes(c.baseTags, tags)
	c.metricCounter.Add(ctx, int64(delta), metric.WithAttributes(attrs...))
}

// gaugeImpl implements Gauge interface
type gaugeImpl struct {
	tier        int
	isPublic    bool
	metricGauge metric.Int64Gauge
	baseTags    map[string]string
}

var _ Gauge = (*gaugeImpl)(nil)

func (g *gaugeImpl) Record(val int64, tags ...Tag) {
	if noopOnly || g.metricGauge == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), metricEmissionTimeout)
	defer cancel()

	attrs := buildAttributes(g.baseTags, tags)
	g.metricGauge.Record(ctx, val, metric.WithAttributes(attrs...))
}

// histogramImpl implements Histogram interface
type histogramImpl struct {
	tier            int
	isPublic        bool
	metricHistogram metric.Float64Histogram
	baseTags        map[string]string
}

var _ Histogram = (*histogramImpl)(nil)

func (h *histogramImpl) Record(val float64, tags ...Tag) {
	if noopOnly || h.metricHistogram == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), metricEmissionTimeout)
	defer cancel()

	attrs := buildAttributes(h.baseTags, tags)
	h.metricHistogram.Record(ctx, val, metric.WithAttributes(attrs...))
}

// latencyImpl implements Latency interface
type latencyImpl struct {
	tier            int
	isPublic        bool
	metricHistogram metric.Float64Histogram
	baseTags        map[string]string
}

var _ Latency = (*latencyImpl)(nil)

func (l *latencyImpl) Record(val time.Duration, tags ...Tag) {
	if noopOnly || l.metricHistogram == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), metricEmissionTimeout)
	defer cancel()

	attrs := buildAttributes(l.baseTags, tags)
	// Convert duration to milliseconds
	ms := float64(val) / float64(time.Millisecond)
	l.metricHistogram.Record(ctx, ms, metric.WithAttributes(attrs...))
}

func buildAttributes(baseTags map[string]string, tags []Tag) []attribute.KeyValue {
	if len(baseTags) == 0 && len(tags) == 0 {
		return nil
	}

	attrs := make([]attribute.KeyValue, 0, len(baseTags)+len(tags))

	for k, v := range baseTags {
		attrs = append(attrs, attribute.String(k, v))
	}

	for _, tag := range tags {
		attrs = append(attrs, attribute.String(string(tag.Key), string(tag.Value)))
	}

	return attrs
}

func Close(ctx context.Context) error {
	if metricReporter == nil {
		noopOnly = true
		return nil
	}

	var errs []error
	if metricReporter.publicProvider != nil {
		err := metricReporter.publicProvider.Shutdown(ctx)
		errs = append(errs, err)
	}
	if metricReporter.defaultProvider != nil {
		err := metricReporter.defaultProvider.Shutdown(ctx)
		errs = append(errs, err)
	}
	metricReporter = nil
	noopOnly = true
	return errors.Join(errs...)
}

func PrometheusRegistry() *prometheus.Registry {
	if metricReporter == nil {
		return nil
	}
	return metricReporter.promRegistry
}
