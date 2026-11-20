// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Command encrypted-payloads starts two runs whose values the Go SDK encrypts with a key file:
// one waits for an approval Channel message, the other fails with an encrypted reason. Dex Web
// decrypts them through dexcli dev -codec-key-file. It keeps serving as a Worker until stopped.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/superdurable/dex/blob-cache-go/blobcache"
	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/web/scripts/local-demo/demoworker"
)

var (
	customerNote = dex.DefineAttribute[string]("customer-note")
	orderStatus  = dex.DefineAttribute[string]("order-status", dex.Plaintext())
	approval     = dex.DefineChannel[string]("approval")
)

// OrderInput starts an order run.
type OrderInput struct {
	Customer string `json:"customer"`
	Items    int    `json:"items"`
}

// PackingSlip is the encrypted Step output that PrepareOrder passes to AwaitApproval.
type PackingSlip struct {
	Customer string `json:"customer"`
	Items    int    `json:"items"`
	Address  string `json:"address"`
}

// EncryptedOrderFlow prepares an order and waits for an approval message.
type EncryptedOrderFlow struct {
	dex.FlowDefaults
}

// GetSteps returns PrepareOrder and AwaitApproval.
func (*EncryptedOrderFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(PrepareOrder{}), dex.DefineStep(AwaitApproval{})}
}

// GetPersistenceSchema declares the encrypted note, the plaintext status, and the approval Channel.
func (*EncryptedOrderFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{customerNote, orderStatus},
		Channels:   []dex.ChannelDef{approval},
	}
}

// PrepareOrder writes the Attributes and passes a packing slip on.
type PrepareOrder struct {
	dex.StepDefaultsNoWaitFor[OrderInput]
}

// Execute writes an encrypted note and a plaintext status.
func (PrepareOrder) Execute(ctx dex.Context, input OrderInput) (*dex.StepDecision, error) {
	if err := errors.Join(
		customerNote.Set(ctx, "VIP customer: deliver before noon"),
		orderStatus.Set(ctx, "waiting"),
	); err != nil {
		return nil, err
	}
	slip := PackingSlip{Customer: input.Customer, Items: input.Items, Address: "221B Baker Street"}
	return dex.GoTo(AwaitApproval{}, slip), nil
}

// AwaitApproval waits for one approval message and completes with it.
type AwaitApproval struct {
	dex.StepDefaults
}

// WaitFor waits for one approval message.
func (AwaitApproval) WaitFor(_ dex.Context, _ PackingSlip) (*dex.Wait, error) {
	return dex.AllOf(approval.ForOne()), nil
}

// Execute completes with the approval message.
func (AwaitApproval) Execute(ctx dex.Context, _ PackingSlip) (*dex.StepDecision, error) {
	messages, err := approval.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	return dex.GracefulComplete(messages[0]), nil
}

// DeclinedPaymentFlow fails at once with an encrypted reason.
type DeclinedPaymentFlow struct {
	dex.FlowDefaults
}

// GetSteps returns ChargeCard.
func (*DeclinedPaymentFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(ChargeCard{})}
}

// GetPersistenceSchema returns an empty schema.
func (*DeclinedPaymentFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{}
}

// ChargeCard fails the run.
type ChargeCard struct {
	dex.StepDefaultsNoWaitFor[string]
}

// Execute fails the run with a reason that names the customer.
func (ChargeCard) Execute(_ dex.Context, customer string) (*dex.StepDecision, error) {
	return dex.ForceFail("card declined for " + customer), nil
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	suffix := time.Now().Unix()
	orderID := flag.String("order-flow-id", fmt.Sprintf("encrypted-order-%d", suffix), "Flow ID of the waiting order run")
	declinedID := flag.String("declined-flow-id", fmt.Sprintf("declined-payment-%d", suffix), "Flow ID of the failed run")
	keyFile := flag.String("key-file", "", "payload key file, as dexcli codec-server keygen writes it")
	flag.Parse()
	if *keyFile == "" {
		return errors.New("-key-file is required")
	}
	keys, err := dex.NewFilePayloadEncryptionKeyProvider(*keyFile)
	if err != nil {
		return err
	}
	codec, err := dex.NewAESGCMPayloadCodec(keys, dex.AESGCMPayloadCodecOptions{})
	if err != nil {
		return err
	}
	encryption := dex.PayloadEncryptionOptions{Codec: codec}
	order, declined := &EncryptedOrderFlow{}, &DeclinedPaymentFlow{}
	registry, err := dex.NewRegistry([]dex.Flow{order, declined})
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return serveEncrypted(ctx, registry, encryption, func(ctx context.Context, client *dex.Client) error {
		if _, err := client.StartFlow(ctx, order, *orderID, OrderInput{Customer: "Ada Lovelace", Items: 3}, dex.StartFlowOptions{}); err != nil {
			return fmt.Errorf("start flow %s: %w", *orderID, err)
		}
		if _, err := client.StartFlow(ctx, declined, *declinedID, "Ada Lovelace", dex.StartFlowOptions{}); err != nil {
			return fmt.Errorf("start flow %s: %w", *declinedID, err)
		}
		fmt.Println("ORDER", *orderID, "DECLINED", *declinedID)
		return nil
	})
}

// serveEncrypted runs an encrypting Worker and Client until ctx ends, calling started once after registration.
func serveEncrypted(
	ctx context.Context,
	registry *dex.Registry,
	encryption dex.PayloadEncryptionOptions,
	started func(context.Context, *dex.Client) error,
) (err error) {
	cacheDir, err := os.MkdirTemp("", "dex-encrypted-payloads-")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(cacheDir)) }()
	workerCache, err := blobcache.New(&blobcache.Config{Dir: filepath.Join(cacheDir, "worker"), MaxBytes: 1 << 26})
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, workerCache.Close()) }()
	clientCache, err := blobcache.New(&blobcache.Config{Dir: filepath.Join(cacheDir, "client"), MaxBytes: 1 << 26})
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, clientCache.Close()) }()
	address := demoworker.FlowServiceAddress()
	worker, err := dex.NewWorker(registry, workerCache, dex.WorkerOptions{FlowServiceAddress: address, PayloadEncryption: encryption})
	if err != nil {
		return err
	}
	client, err := dex.NewClient(registry, clientCache, dex.ClientOptions{FlowServiceAddress: address, PayloadEncryption: encryption})
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, client.Close()) }()
	workerResult := make(chan error, 1)
	go func() { workerResult <- worker.Start() }()
	startupCtx, cancelStartup := context.WithTimeout(ctx, 30*time.Second)
	registrationErr := worker.WaitPullRegistered(startupCtx)
	cancelStartup()
	if registrationErr == nil {
		registrationErr = started(ctx, client)
	}
	if registrationErr != nil {
		return errors.Join(registrationErr, worker.Stop(context.Background()), <-workerResult)
	}
	select {
	case err := <-workerResult:
		return errors.Join(err, worker.Stop(context.Background()))
	case <-ctx.Done():
		return errors.Join(worker.Stop(context.Background()), <-workerResult)
	}
}
