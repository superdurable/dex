package metrics

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
)

type Counter interface {
	Inc(tags ...Tag)
	IncBy(delta int, tags ...Tag)
}

type Gauge interface {
	Record(val int64, tags ...Tag)
}

type Histogram interface {
	Record(val float64, tags ...Tag)
}

type Latency interface {
	Record(val time.Duration, tags ...Tag)
}

type Tag struct {
	Key   tagKey
	Value tagValue
}

// tagKey is private because it's required to use predefined tagKeys to ensure consistency
// to avoid typos
type tagKey string

// tagValue is private because it's preferred to use predefined tagKeys
// if values cannot be predefined, you can use anyTagValue to define some dynamic tag values
// see example TagResponseCodeFromProtoError
type tagValue string

// anyTagValue lets you set arbitrary value.
// This is useful when it's hard to pre-define all the tag values
// see example TagResponseCodeFromProtoError
// NOTE: always use "_" and lower case for naming -- Prometheus doesn't allow "-" or ".".
// All the names will be lower-cased and sanitized in metrics provider.
// !!! extremely cautious when using this, it could blow up cardinality, cost, and sanity.
func anyTagValue(v string) tagValue {
	return tagValue(sanitize(v))
}

// Convert to lowercase and replace all non-alphanumeric characters with underscore in one pass
func sanitize(v string) string {
	var builder strings.Builder
	builder.Grow(len(v))
	for _, r := range v {
		if unicode.IsLetter(r) {
			builder.WriteRune(unicode.ToLower(r))
		} else if unicode.IsDigit(r) {
			builder.WriteRune(r)
		} else {
			builder.WriteRune('_')
		}
	}
	return builder.String()
}

func internalCounter(tier int, name string) (counter *counterImpl) {
	return makeCounter(tier, name, false)
}

func publicCounter(tier int, name string) (counter *counterImpl) {
	return makeCounter(tier, name, true)
}

func makeCounter(tier int, name string, isPublic bool) (counter *counterImpl) {
	if tier < 1 {
		panic(fmt.Sprintf("metric tier must be >= 1, got %d for %s", tier, name))
	}
	validateMetricName(name)
	if _, ok := registryNames[name]; ok {
		panic(fmt.Sprintf("metric name already registered: %s", name))
	}
	registryNames[name] = true

	defer func() {
		registryCounter[name] = counter
	}()
	counter = &counterImpl{
		tier:     tier,
		isPublic: isPublic,
	}
	return counter
}

func internalLatency(tier int, name string) (latency *latencyImpl) {
	return makeLatency(tier, name, false)
}

func publicLatency(tier int, name string) (latency *latencyImpl) {
	return makeLatency(tier, name, true)
}

func makeLatency(tier int, name string, isPublic bool) (latency *latencyImpl) {
	if tier < 1 {
		panic(fmt.Sprintf("metric tier must be >= 1, got %d for %s", tier, name))
	}
	validateMetricName(name)
	if _, ok := registryNames[name]; ok {
		panic(fmt.Sprintf("metric name already registered: %s", name))
	}
	registryNames[name] = true

	defer func() {
		registryLatency[name] = latency
	}()
	latency = &latencyImpl{
		tier:     tier,
		isPublic: isPublic,
	}
	return latency
}

func internalGauge(tier int, name string) (gauge *gaugeImpl) {
	return makeGauge(tier, name, false)
}

func publicGauge(tier int, name string) (gauge *gaugeImpl) {
	return makeGauge(tier, name, true)
}

func makeGauge(tier int, name string, isPublic bool) (gauge *gaugeImpl) {
	if tier < 1 {
		panic(fmt.Sprintf("metric tier must be >= 1, got %d for %s", tier, name))
	}
	validateMetricName(name)
	if _, ok := registryNames[name]; ok {
		panic(fmt.Sprintf("metric name already registered: %s", name))
	}
	registryNames[name] = true

	defer func() {
		registryGauge[name] = gauge
	}()
	gauge = &gaugeImpl{
		tier:     tier,
		isPublic: isPublic,
	}
	return gauge
}

func internalHistogram(tier int, name string) (histogram *histogramImpl) {
	return makeHistogram(tier, name, false)
}

func publicHistogram(tier int, name string) (histogram *histogramImpl) {
	return makeHistogram(tier, name, true)
}

func makeHistogram(tier int, name string, isPublic bool) (histogram *histogramImpl) {
	if tier < 1 {
		panic(fmt.Sprintf("metric tier must be >= 1, got %d for %s", tier, name))
	}
	validateMetricName(name)
	if _, ok := registryNames[name]; ok {
		panic(fmt.Sprintf("metric name already registered: %s", name))
	}
	registryNames[name] = true

	defer func() {
		registryHistogram[name] = histogram
	}()
	histogram = &histogramImpl{
		tier:     tier,
		isPublic: isPublic,
	}
	return histogram
}

var metricNameRegex = regexp.MustCompile(`^[a-z0-9_]+$`)

// should only contain lowercase letters, numbers, and underscores
func validateMetricName(name string) {
	if !metricNameRegex.MatchString(name) {
		panic(fmt.Sprintf("metric name can only contain lowercase letters, numbers, and underscores: %s", name))
	}
}
