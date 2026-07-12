package metrics

import (
	"context"
	"time"

	"github.com/superdurable/dex/server/config"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
)

// filteringExporter wraps an exporter and skips export if there's no data
type filteringExporter struct {
	exporter metric.Exporter
}

func (f *filteringExporter) Export(ctx context.Context, rm *metricdata.ResourceMetrics) error {
	// Check if there's any actual data to export
	if rm == nil || len(rm.ScopeMetrics) == 0 {
		return nil // Skip export for empty payload
	}

	hasData := false
	for _, sm := range rm.ScopeMetrics {
		if len(sm.Metrics) > 0 {
			hasData = true
			break
		}
	}

	if !hasData {
		return nil // Skip export for empty payload
	}

	return f.exporter.Export(ctx, rm)
}

func (f *filteringExporter) ForceFlush(ctx context.Context) error {
	return f.exporter.ForceFlush(ctx)
}

func (f *filteringExporter) Shutdown(ctx context.Context) error {
	return f.exporter.Shutdown(ctx)
}

func (f *filteringExporter) Temporality(k metric.InstrumentKind) metricdata.Temporality {
	return f.exporter.Temporality(k)
}

func (f *filteringExporter) Aggregation(k metric.InstrumentKind) metric.Aggregation {
	return f.exporter.Aggregation(k)
}

func newDatadogProvider(ctx context.Context, datadogCfg *config.DatadogConfig, res *resource.Resource) (*metric.MeterProvider, error) {
	baseExp, err := otlpmetrichttp.New(ctx,
		otlpmetrichttp.WithEndpoint(datadogCfg.Endpoint),
		otlpmetrichttp.WithURLPath("/v1/metrics"),
		otlpmetrichttp.WithHeaders(map[string]string{
			"dd-api-key":            datadogCfg.APIKey,
			"dd-otel-metric-config": `{"resource_attributes_as_tags":true,"histograms":{"mode":"distributions"}}`,
		}),
		// Use delta temporality for Datadog compatibility
		otlpmetrichttp.WithTemporalitySelector(func(ik metric.InstrumentKind) metricdata.Temporality {
			return metricdata.DeltaTemporality
		}),
	)
	if err != nil {
		return nil, err
	}

	// Wrap with filtering exporter to skip empty payloads
	exp := &filteringExporter{exporter: baseExp}

	mp := metric.NewMeterProvider(
		metric.WithReader(metric.NewPeriodicReader(exp, metric.WithInterval(10*time.Second))),
		metric.WithResource(res),
	)
	return mp, nil
}

func newPrometheusProvider(res *resource.Resource) (*metric.MeterProvider, *prometheus.Registry, error) {
	reg := prometheus.NewRegistry()
	promExp, err := otelprom.New(
		otelprom.WithRegisterer(reg),
	)
	if err != nil {
		return nil, nil, err
	}

	mp := metric.NewMeterProvider(
		metric.WithReader(promExp),
		metric.WithResource(res),
	)

	return mp, reg, nil
}
