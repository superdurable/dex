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

// Command worker runs the Worker for ManagerApprovalRefundFlow. Stop it with Ctrl+C.
package main

import (
	"context"
	"errors"
	"log"
	"os/signal"
	"syscall"
	"time"

	"github.com/superdurable/dex/examples/go/products/customer-refund/managerapproval"
	"github.com/superdurable/dex/sdk-go/dex"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := runRefundWorker(ctx); err != nil {
		log.Fatal(err)
	}
}

func runRefundWorker(ctx context.Context) error {
	billingMode := managerapproval.EnvironmentOr("REFUND_BILLING_MODE", managerapproval.BillingModeNormal)
	flow, err := managerapproval.NewFlowForBillingMode(billingMode, managerapproval.DefaultLedgerDirectory)
	if err != nil {
		return err
	}
	encryption, err := managerapproval.NewFilePayloadEncryption(managerapproval.EnvironmentOr("REFUND_PAYLOAD_KEY_FILE", ""))
	if err != nil {
		return err
	}
	registry, err := dex.NewRegistry([]dex.Flow{flow})
	if err != nil {
		return err
	}
	cache, err := managerapproval.OpenBlobCache(".dex-blob-cache/customer-refund-worker")
	if err != nil {
		return err
	}
	designPaths := managerapproval.FlowDefinitionPaths(managerapproval.FlowDefinitionsDirectory)
	if len(designPaths) == 0 {
		log.Printf("No design in %s; see the README to generate it.", managerapproval.FlowDefinitionsDirectory)
	}
	workerID := managerapproval.NewWorkerID()
	worker, err := dex.NewWorker(registry, cache, dex.WorkerOptions{
		FlowServiceAddress:  managerapproval.EnvironmentOr("DEX_FLOW_SERVICE_ADDRESS", "127.0.0.1:8801"),
		Pull:                &dex.PullWorkerOptions{AppWorkerID: workerID},
		FlowDefinitionPaths: designPaths,
		PayloadEncryption:   encryption,
	})
	if err != nil {
		return errors.Join(err, cache.Close())
	}
	workerResult := make(chan error, 1)
	go func() { workerResult <- worker.Start() }()

	startupContext, cancelStartup := context.WithTimeout(ctx, 30*time.Second)
	registrationErr := worker.WaitPullRegistered(startupContext)
	cancelStartup()
	if registrationErr == nil {
		encrypted := "off"
		if encryption.Codec != nil {
			encrypted = "on"
		}
		log.Printf("Worker %s is ready (billing mode %s, encryption %s). Press Ctrl+C to stop it.",
			workerID, billingMode, encrypted)
		select {
		case <-ctx.Done():
			log.Print("Stopping the Worker.")
		case err := <-workerResult:
			return errors.Join(err, cache.Close())
		}
	}
	stopContext, cancelStop := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelStop()
	stopErr := worker.Stop(stopContext)
	return errors.Join(registrationErr, stopErr, <-workerResult, cache.Close())
}
