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

// Package payloadencryptioninterop defines the Flow that the cross-SDK payload
// encryption interoperability test runs in Go, Python, and TypeScript.
//
// Every SDK defines the same Flow shape under its own Flow type. Each SDK's
// Worker serves only its own Flow type, and every SDK's Client starts and reads
// all three. Keep this file in sync with
// examples/python/tests/interop/payload_encryption_interop_flow.py and
// examples/typescript/test/interop/payload-encryption-interop-flow.ts.
package payloadencryptioninterop

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/superdurable/dex/blob-cache-go/blobcache"
	"github.com/superdurable/dex/sdk-go/dex"
)

// Owner names one SDK in the interop test.
type Owner string

const (
	OwnerGo         Owner = "go"
	OwnerPython     Owner = "python"
	OwnerTypeScript Owner = "typescript"
)

// Owners lists every SDK whose Worker takes part in the interop test.
var Owners = []Owner{OwnerGo, OwnerPython, OwnerTypeScript}

// FailInputPrefix makes the Step fail with an error that names the rest of the input.
const FailInputPrefix = "fail:"

// SecretText holds Worker-written text, so payload encryption stores it as ciphertext.
var SecretText = dex.DefineAttribute[string]("payload-interop-secret")

// StatusText stays plaintext, so a Client without a codec can read it.
var StatusText = dex.DefineAttribute[string]("payload-interop-status", dex.Plaintext())

// FlowTypeOf returns the Flow type that the Owner's Worker serves.
func FlowTypeOf(owner Owner) string {
	switch owner {
	case OwnerGo:
		return "GoPayloadInteropFlow"
	case OwnerPython:
		return "PythonPayloadInteropFlow"
	case OwnerTypeScript:
		return "TypeScriptPayloadInteropFlow"
	}
	panic(fmt.Sprintf("unknown payload interop owner %q", owner))
}

// ExpectedSecretText returns the encrypted Attribute value the Owner's Worker writes for input.
func ExpectedSecretText(owner Owner, input string) string {
	return input + " | stored by " + string(owner)
}

// ExpectedStatusText returns the plaintext Attribute value the Owner's Worker writes.
func ExpectedStatusText(owner Owner) string {
	return "stored by " + string(owner)
}

// ExpectedOutput returns the Flow output the Owner's Worker completes with for input.
func ExpectedOutput(owner Owner, input string) string {
	return "completed by " + string(owner) + ": " + input
}

// ExpectedFailureText returns the Step error text the Owner's Worker fails with for input.
// The input is the text after FailInputPrefix.
func ExpectedFailureText(owner Owner, input string) string {
	return "payload interop failure in " + string(owner) + ": " + input
}

// PayloadInteropFlow is the interop Flow under the Flow type of one Owner.
type PayloadInteropFlow struct {
	dex.FlowDefaults
	owner Owner
}

// NewPayloadInteropFlow returns the interop Flow that the Owner's Worker serves.
func NewPayloadInteropFlow(owner Owner) *PayloadInteropFlow {
	return &PayloadInteropFlow{owner: owner}
}

func (flow *PayloadInteropFlow) GetFlowType() string {
	return FlowTypeOf(flow.owner)
}

func (flow *PayloadInteropFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(storePayloadInteropSecretStep{owner: flow.owner})}
}

func (*PayloadInteropFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Attributes: []dex.AttributeDef{SecretText, StatusText}}
}

// NewClientRegistry returns a Registry with every Owner's Flow, so one Client can start them all.
func NewClientRegistry() (*dex.Registry, error) {
	flows := make([]dex.Flow, 0, len(Owners))
	for _, owner := range Owners {
		flows = append(flows, NewPayloadInteropFlow(owner))
	}
	return dex.NewRegistry(flows)
}

type storePayloadInteropSecretStep struct {
	dex.StepDefaultsNoWaitFor[string]
	owner Owner
}

func (storePayloadInteropSecretStep) GetStepType() string {
	return "StorePayloadInteropSecretStep"
}

// GetStepOptions disables retries, so a failing Step fails the Flow at once.
func (storePayloadInteropSecretStep) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{ExecuteRetry: &dex.RetryPolicy{MaximumAttempts: 1}}
}

func (step storePayloadInteropSecretStep) Execute(ctx dex.Context, input string) (*dex.StepDecision, error) {
	if failureInput, isFailure := strings.CutPrefix(input, FailInputPrefix); isFailure {
		return nil, errors.New(ExpectedFailureText(step.owner, failureInput))
	}
	if err := SecretText.Set(ctx, ExpectedSecretText(step.owner, input)); err != nil {
		return nil, err
	}
	if err := StatusText.Set(ctx, ExpectedStatusText(step.owner)); err != nil {
		return nil, err
	}
	return dex.GracefulComplete(ExpectedOutput(step.owner, input)), nil
}

// NewFilePayloadEncryption reads a key file, such as one from dexcli codec-server keygen.
func NewFilePayloadEncryption(keyFilePath string) (dex.PayloadEncryptionOptions, error) {
	keys, err := dex.NewFilePayloadEncryptionKeyProvider(keyFilePath)
	if err != nil {
		return dex.PayloadEncryptionOptions{}, err
	}
	codec, err := dex.NewAESGCMPayloadCodec(keys, dex.AESGCMPayloadCodecOptions{})
	if err != nil {
		return dex.PayloadEncryptionOptions{}, err
	}
	return dex.PayloadEncryptionOptions{Codec: codec}, nil
}

// WorkerConfig configures RunGoPayloadInteropWorker.
type WorkerConfig struct {
	ServerAddress string
	KeyFilePath   string
	CacheDir      string
}

// RunGoPayloadInteropWorker serves the Go interop Flow until ctx ends.
// It calls onReady once the Worker is registered for pull tasks.
func RunGoPayloadInteropWorker(ctx context.Context, config WorkerConfig, onReady func()) error {
	encryption, err := NewFilePayloadEncryption(config.KeyFilePath)
	if err != nil {
		return err
	}
	registry, err := dex.NewRegistry([]dex.Flow{NewPayloadInteropFlow(OwnerGo)})
	if err != nil {
		return err
	}
	cache, err := blobcache.New(&blobcache.Config{Dir: config.CacheDir, MaxBytes: 64 << 20})
	if err != nil {
		return err
	}
	worker, err := dex.NewWorker(registry, cache, dex.WorkerOptions{
		FlowServiceAddress: config.ServerAddress,
		Pull:               &dex.PullWorkerOptions{MaxConcurrentTasks: 16},
		PayloadEncryption:  encryption,
	})
	if err != nil {
		return errors.Join(err, cache.Close())
	}
	workerResult := make(chan error, 1)
	go func() { workerResult <- worker.Start() }()
	startupContext, cancelStartup := context.WithTimeout(ctx, time.Minute)
	defer cancelStartup()
	if err := worker.WaitPullRegistered(startupContext); err != nil {
		return errors.Join(err, stopPayloadInteropWorker(worker, workerResult), cache.Close())
	}
	onReady()
	select {
	case <-ctx.Done():
		return errors.Join(stopPayloadInteropWorker(worker, workerResult), cache.Close())
	case err := <-workerResult:
		return errors.Join(err, cache.Close())
	}
}

func stopPayloadInteropWorker(worker *dex.Worker, workerResult <-chan error) error {
	stopContext, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	stopErr := worker.Stop(stopContext)
	select {
	case runErr := <-workerResult:
		return errors.Join(stopErr, runErr)
	case <-stopContext.Done():
		return errors.Join(stopErr, stopContext.Err())
	}
}

var _ dex.Flow = (*PayloadInteropFlow)(nil)
