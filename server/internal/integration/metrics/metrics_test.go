package metrics

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	"github.com/superdurable/dex/server/config"
	"github.com/superdurable/dex/server/internal/integration/testhelpers"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

const dbPrefix = "dex_test_integration_metrics"

func TestE2E_MetricsEndpoint_ExposesRunMetrics(t *testing.T) {
	app, runClient, _ := testhelpers.StartE2EServerWithConfig(t, dbPrefix, func(cfg *config.Config) {
		cfg.Metrics = config.DefaultMetricsConfig()
		cfg.Metrics.Provider = config.MetricsProviderPrometheus
		cfg.Metrics.Prometheus.ListenAddress = "127.0.0.1:0"
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err := runClient.StartRun(ctx, &pb.StartRunRequest{
		Namespace:    "metrics-ns",
		RunId:        uuid.NewString(),
		FlowType:     "metrics-flow",
		TaskListName: "metrics-tasklist",
	})
	require.NoError(t, err)

	resp, err := http.Get("http://" + app.MetricsAddress() + "/metrics")
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, string(body), "dex_run_attempt_started_counter")
}
