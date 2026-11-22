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

package managerapproval

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/superdurable/dex/blob-cache-go/blobcache"
	"github.com/superdurable/dex/sdk-go/dex"
)

const (
	// BillingModeNormal calls billing; BillingModeCallMissing leaves the call out.
	BillingModeNormal      = "normal"
	BillingModeCallMissing = "call-missing"
	// FlowDefinitionsDirectory holds the design that dexcli visualize writes.
	FlowDefinitionsDirectory = "build/customer-refund"
)

// NorthstarRenewal is the sample request: a 1,249.00 USD renewal after a 31-hour interruption.
var NorthstarRenewal = RefundRequest{
	Customer:      "Northstar Studio",
	CustomerEmail: "billing@northstar.example",
	OrderID:       "INV-1249",
	Reason:        "Our workspace was down for 31 hours after the renewal.",
}

// ScenarioNames lists the scenarios in the order the README describes them.
var ScenarioNames = []string{"normal", "fail-then-succeed", "deny", "small"}

// ScenarioRequest returns the run input of a scenario, or false for an unknown name.
func ScenarioRequest(scenario string) (RefundRequest, bool) {
	request := NorthstarRenewal
	switch scenario {
	case "normal":
	case "fail-then-succeed":
		request.PaymentTimeoutsToSimulate = 2
	case "deny":
		request.OrderID = "INV-0388"
	case "small":
		request.OrderID = "INV-0049"
	default:
		return RefundRequest{}, false
	}
	return request, true
}

// RefundServices are the fakes that one ledger directory backs.
type RefundServices struct {
	Billing *FakeBillingService
	Email   *FakeEmailService
}

// NewRefundServices returns billing and email over the ledger in directory.
func NewRefundServices(ledgerDirectory string) RefundServices {
	ledger := NewRefundLedger(ledgerDirectory)
	return RefundServices{Billing: NewFakeBillingService(ledger), Email: NewFakeEmailService(ledger)}
}

// NewFlowForBillingMode returns the Flow; billing mode "call-missing" leaves out the billing call.
func NewFlowForBillingMode(billingMode string, ledgerDirectory string) (*ManagerApprovalRefundFlow, error) {
	services := NewRefundServices(ledgerDirectory)
	var billing BillingService
	switch billingMode {
	case BillingModeNormal:
		billing = services.Billing
	case BillingModeCallMissing:
		billing = CallMissingBillingService{FakeBillingService: services.Billing}
	default:
		return nil, fmt.Errorf("REFUND_BILLING_MODE must be normal or call-missing, not %q", billingMode)
	}
	return NewManagerApprovalRefundFlow(billing, services.Email, &MockMessageModel{}), nil
}

// NewFilePayloadEncryption turns a key file into payload encryption; an empty path turns it off.
func NewFilePayloadEncryption(keyFilePath string) (dex.PayloadEncryptionOptions, error) {
	if keyFilePath == "" {
		return dex.PayloadEncryptionOptions{}, nil
	}
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

// NewWorkerID returns a Worker ID without the host name; it changes on every start.
func NewWorkerID() string {
	suffix := make([]byte, 3)
	_, _ = rand.Read(suffix)
	return "refund-worker-" + hex.EncodeToString(suffix)
}

// OpenBlobCache opens a local cache for large Flow values, up to 1 GiB.
func OpenBlobCache(directory string) (*blobcache.Cache, error) {
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return nil, err
	}
	return blobcache.New(&blobcache.Config{Dir: directory, MaxBytes: 1 << 30})
}

// FlowDefinitionPaths returns the design folder when it exists, so the Worker uploads it.
func FlowDefinitionPaths(directory string) []string {
	info, err := os.Stat(directory)
	if err != nil || !info.IsDir() {
		return nil
	}
	return []string{filepath.Clean(directory)}
}

// OpenCommandClient opens the Client of the start and decide commands, with the environment's settings.
// Close the Client, then the cache.
func OpenCommandClient(flow *ManagerApprovalRefundFlow) (*dex.Client, *blobcache.Cache, error) {
	encryption, err := NewFilePayloadEncryption(os.Getenv("REFUND_PAYLOAD_KEY_FILE"))
	if err != nil {
		return nil, nil, err
	}
	registry, err := dex.NewRegistry([]dex.Flow{flow})
	if err != nil {
		return nil, nil, err
	}
	cache, err := OpenBlobCache(".dex-blob-cache/customer-refund-client")
	if err != nil {
		return nil, nil, err
	}
	client, err := dex.NewClient(registry, cache, dex.ClientOptions{
		FlowServiceAddress: EnvironmentOr("DEX_FLOW_SERVICE_ADDRESS", "127.0.0.1:8801"),
		PayloadEncryption:  encryption,
	})
	if err != nil {
		return nil, nil, errors.Join(err, cache.Close())
	}
	return client, cache, nil
}

// EnvironmentOr returns the environment variable, or fallback when it is unset or empty.
func EnvironmentOr(name string, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
