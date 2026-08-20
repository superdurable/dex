// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package bootstrap

import (
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/service/common/log/loggerimpl"
	"github.com/uber-go/tally/v4/prometheus"
)

func TestPrometheusScopeDoesNotPrefixSDKMetrics(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	address := listener.Addr().String()
	require.NoError(t, listener.Close())

	logger, err := loggerimpl.NewDevelopment()
	require.NoError(t, err)
	handler, closer, err := newPrometheusScope(&prometheus.Configuration{
		ListenAddress: address,
		TimerType:     "histogram",
	}, logger)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, closer.Close()) })

	handler.Counter("dex_metric_probe").Inc(1)
	require.Eventually(t, func() bool {
		response, requestErr := http.Get("http://" + address + "/metrics")
		if requestErr != nil {
			return false
		}
		defer response.Body.Close()
		body, readErr := io.ReadAll(response.Body)
		return readErr == nil &&
			strings.Contains(string(body), "dex_metric_probe") &&
			!strings.Contains(string(body), "temporal_samples_dex_metric_probe")
	}, 5*time.Second, 50*time.Millisecond)
}
