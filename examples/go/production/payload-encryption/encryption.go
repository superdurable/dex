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

package payloadencryption

import (
	"context"
	"errors"
	"time"

	"github.com/superdurable/dex/blob-cache-go/blobcache"
	"github.com/superdurable/dex/sdk-go/dex"
)

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

// StartEncryptedSupportTicket starts a Worker and a Client that share one key file.
func StartEncryptedSupportTicket(
	cache *blobcache.Cache,
	serverAddress string,
	keyFilePath string,
) (*SupportTicketFlow, *dex.Worker, *dex.Client, <-chan error, error) {
	encryption, err := NewFilePayloadEncryption(keyFilePath)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	flow := &SupportTicketFlow{}
	registry, err := dex.NewRegistry([]dex.Flow{flow})
	if err != nil {
		return nil, nil, nil, nil, err
	}
	worker, err := dex.NewWorker(registry, cache, dex.WorkerOptions{
		FlowServiceAddress: serverAddress,
		Pull:               &dex.PullWorkerOptions{MaxConcurrentTasks: 8},
		PayloadEncryption:  encryption,
	})
	if err != nil {
		return nil, nil, nil, nil, err
	}
	client, err := dex.NewClient(registry, cache, dex.ClientOptions{
		FlowServiceAddress: serverAddress,
		PayloadEncryption:  encryption,
	})
	if err != nil {
		return nil, nil, nil, nil, errors.Join(err, worker.Stop(context.Background()))
	}
	workerResult := make(chan error, 1)
	go func() { workerResult <- worker.Start() }()
	startupContext, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := worker.WaitPullRegistered(startupContext); err != nil {
		return nil, nil, nil, nil, errors.Join(err, StopEncryptedSupportTicket(context.Background(), worker, client, workerResult))
	}
	return flow, worker, client, workerResult, nil
}

// StopEncryptedSupportTicket closes the Client and stops the Worker.
func StopEncryptedSupportTicket(ctx context.Context, worker *dex.Worker, client *dex.Client, workerResult <-chan error) error {
	clientCloseErr := client.Close()
	workerStopErr := worker.Stop(ctx)
	var workerRunErr error
	select {
	case workerRunErr = <-workerResult:
	case <-ctx.Done():
		workerRunErr = ctx.Err()
	}
	return errors.Join(clientCloseErr, workerStopErr, workerRunErr)
}
