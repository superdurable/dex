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
	"fmt"
	"time"
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

type tagKey string
type tagValue string

func registerCounter(tier int, name string) *counterImpl {
	validateRegister(tier, name)
	counter := &counterImpl{tier: tier}
	registeredCounter[name] = counter
	return counter
}

func registerLatency(tier int, name string) *latencyImpl {
	validateRegister(tier, name)
	latency := &latencyImpl{tier: tier}
	registeredLatency[name] = latency
	return latency
}

func registerHistogram(tier int, name string) *histogramImpl {
	validateRegister(tier, name)
	histogram := &histogramImpl{tier: tier}
	registeredHistogram[name] = histogram
	return histogram
}

func registerGauge(tier int, name string) *gaugeImpl {
	validateRegister(tier, name)
	gauge := &gaugeImpl{tier: tier}
	registeredGauge[name] = gauge
	return gauge
}

func validateRegister(tier int, name string) {
	if tier < MetricTierCritical || tier > MetricTierDeepDebug {
		panic(fmt.Sprintf("metric tier must be in [%d, %d], got %d for %s",
			MetricTierCritical, MetricTierDeepDebug, tier, name))
	}
	if _, ok := registeredMetricNames[name]; ok {
		panic(fmt.Sprintf("metric name already registered: %s", name))
	}
	registeredMetricNames[name] = true
}
