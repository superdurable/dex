// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

// Tests run against the Dex server at DEX_FLOW_SERVICE_ADDRESS.
// Without one they fail, unless DEX_EXAMPLES_ALLOW_SKIP=1.
package quickstart_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/superdurable/dex/blob-cache-go/blobcache"
	quickstart "github.com/superdurable/dex/examples/go/quick-start"
	"github.com/superdurable/dex/sdk-go/dex"
)

const waitTimeout = 60 * time.Second

func TestRestartedWorkerShipsWithoutChargingAgain(t *testing.T) {
	orderID := "order-" + randomHex(t)
	firstShop := &quickstart.ShopAPI{}
	secondShop := &quickstart.ShopAPI{}
	clientFlow := quickstart.NewOrderFlow(&quickstart.ShopAPI{})
	client := connectClient(t, clientFlow)
	ctx := context.Background()

	stopFirstWorker := startWorker(t, quickstart.NewOrderFlow(firstShop))
	if _, err := client.StartFlow(ctx, clientFlow, orderID, orderID, dex.StartFlowOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := client.WaitForStepCompletion(ctx, orderID, dex.StepExecutionID{StepType: "Charge"},
		dex.WaitForStepCompletionOptions{RequestTimeout: waitTimeout}); err != nil {
		t.Fatal(err)
	}
	stopFirstWorker()
	assertOrders(t, "first Worker charged", firstShop.Charged(), orderID)
	assertOrders(t, "first Worker shipped", firstShop.Shipped())

	stopSecondWorker := startWorker(t, quickstart.NewOrderFlow(secondShop))
	defer stopSecondWorker()
	skipShipDelay(t, client, orderID)
	waitCtx, cancel := context.WithTimeout(ctx, waitTimeout)
	defer cancel()
	result, err := client.WaitForFlow(waitCtx, orderID, dex.WaitForFlowOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != dex.FlowCompleted {
		t.Fatalf("Flow status = %v, want completed", result.Status)
	}
	assertOrders(t, "second Worker charged", secondShop.Charged())
	assertOrders(t, "second Worker shipped", secondShop.Shipped(), orderID)
}

func connectClient(t *testing.T, flow *quickstart.OrderFlow) *dex.Client {
	t.Helper()
	registry, err := dex.NewRegistry([]dex.Flow{flow})
	if err != nil {
		t.Fatal(err)
	}
	cache := openCache(t, "client")
	client, err := dex.NewClient(registry, cache, dex.ClientOptions{FlowServiceAddress: quickstart.ServerAddress()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Error(err)
		}
	})
	if !isServerReady(client) {
		message := "no Dex server at " + quickstart.ServerAddress() + "; set DEX_FLOW_SERVICE_ADDRESS"
		if os.Getenv("DEX_EXAMPLES_ALLOW_SKIP") == "1" {
			t.Skip(message)
		}
		t.Fatal(message)
	}
	return client
}

// startWorker returns once the Worker can take tasks; call the result to stop it.
func startWorker(t *testing.T, flow *quickstart.OrderFlow) func() {
	t.Helper()
	registry, err := dex.NewRegistry([]dex.Flow{flow})
	if err != nil {
		t.Fatal(err)
	}
	worker, err := dex.NewWorker(registry, openCache(t, "worker"), dex.WorkerOptions{
		FlowServiceAddress: quickstart.ServerAddress(),
	})
	if err != nil {
		t.Fatal(err)
	}
	workerResult := make(chan error, 1)
	go func() { workerResult <- worker.Start() }()
	isStopped := false
	stop := func() {
		if isStopped {
			return
		}
		isStopped = true
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := errors.Join(worker.Stop(ctx), <-workerResult); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(stop)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := worker.WaitPullRegistered(ctx); err != nil {
		t.Fatal(err)
	}
	return stop
}

func openCache(t *testing.T, name string) *blobcache.Cache {
	t.Helper()
	cache, err := quickstart.OpenCache(filepath.Join(t.TempDir(), name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := cache.Close(); err != nil {
			t.Error(err)
		}
	})
	return cache
}

func isServerReady(client *dex.Client) bool {
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, err := client.HealthCheck(ctx)
		cancel()
		if err == nil {
			return true
		}
		time.Sleep(250 * time.Millisecond)
	}
	return false
}

// skipShipDelay skips the 30-second Timer once Ship waits on it.
func skipShipDelay(t *testing.T, client *dex.Client, orderID string) {
	t.Helper()
	deadline := time.Now().Add(waitTimeout)
	for {
		err := client.SkipTimer(context.Background(), orderID, dex.StepExecutionID{StepType: "Ship"},
			dex.TimerID{ConditionID: quickstart.ShipDelayTimer})
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func assertOrders(t *testing.T, what string, got []string, want ...string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("%s %v, want %v", what, got, want)
	}
}

func randomHex(t *testing.T) string {
	t.Helper()
	randomBytes := make([]byte, 16)
	if _, err := rand.Read(randomBytes); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(randomBytes)
}
