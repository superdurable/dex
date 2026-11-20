// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//go:build integration

package dev

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/superdurable/dex/blob-cache-go/blobcache"
	"github.com/superdurable/dex/cli/internal/command"
	"github.com/superdurable/dex/cli/internal/payloadcodec"
	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const encryptedOrderTaskQueue = "dexcli-codec"

var (
	encryptedOrderSecret  = dex.DefineAttribute[string]("order-secret")
	encryptedOrderRelease = dex.DefineChannel[string]("order-release")
)

type EncryptedOrderFlow struct {
	dex.FlowDefaults
}

func (EncryptedOrderFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(encryptedOrderStartStep{})}
}

func (EncryptedOrderFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{encryptedOrderSecret},
		Channels:   []dex.ChannelDef{encryptedOrderRelease},
	}
}

type encryptedOrderStartStep struct {
	dex.StepDefaults
}

func (encryptedOrderStartStep) WaitFor(ctx dex.Context, input string) (*dex.Wait, error) {
	if err := encryptedOrderSecret.Set(ctx, "secret:"+input); err != nil {
		return nil, err
	}
	return dex.AllOf(encryptedOrderRelease.ForOne()), nil
}

func (encryptedOrderStartStep) Execute(_ dex.Context, input string) (*dex.StepDecision, error) {
	return dex.GracefulComplete(input), nil
}

// TestDevCodecKeyFileDecryptsForWebAndFlowCommands checks that Dex Web and flow commands see the dev codec server.
func TestDevCodecKeyFileDecryptsForWebAndFlowCommands(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	keyFilePath := filepath.Join(t.TempDir(), "key.json")
	if err := payloadcodec.GenerateKeyFile(keyFilePath, "dev-k1", false); err != nil {
		t.Fatal(err)
	}
	ports := freePorts(t, 2)
	cfg := testConfig(t)
	cfg.StateDirectory = filepath.Join(home, ".dex")
	cfg.DexPort, cfg.WebPort = ports[0], ports[1]
	cfg.explicitLocalFlags["dex-port"] = true
	cfg.explicitLocalFlags["web-port"] = true
	cfg.CodecKeyFile = keyFilePath
	output := &synchronizedBuffer{}
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runFinished := make(chan error, 1)
	go func() { runFinished <- newSupervisor(cfg, output, output).Run(runCtx) }()
	webURL := "http://127.0.0.1:" + strconv.Itoa(cfg.WebPort)
	waitForHealthyWeb(t, webURL+"/healthz", runFinished)
	dexAddress := net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.DexPort))

	webCodecServer := readWebCodecServer(t, webURL)
	if !webCodecServer.Configured || webCodecServer.Token == "" {
		t.Fatalf("Dex Web codec server config = %+v", webCodecServer)
	}
	remote, err := payloadcodec.NewRemoteCodec(webCodecServer.Endpoint, webCodecServer.Token)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := remote.EncodeValues(context.Background(), dex.PayloadContext{FlowID: "probe"}, []*dexpb.Value{{Kind: &dexpb.Value_StringValue{StringValue: "x"}}}); err != nil {
		t.Fatalf("the endpoint Dex Web received does not encode: %v", err)
	}

	client := startEncryptedOrderApplication(t, dexAddress, keyFilePath)
	if _, err := client.StartFlow(context.Background(), EncryptedOrderFlow{}, "encrypted-order-1", "A-1", dex.StartFlowOptions{TaskQueue: encryptedOrderTaskQueue}); err != nil {
		t.Fatal(err)
	}
	waitForStoredEnvelope(t, dexAddress, "encrypted-order-1", "order-secret")

	cliOutput := &bytes.Buffer{}
	cliErrors := &bytes.Buffer{}
	if err := command.NewApp(bytes.NewReader(nil), cliOutput, cliErrors).Execute(context.Background(), []string{
		"flow", "state", "encrypted-order-1", "--server", dexAddress,
	}); err != nil {
		t.Fatalf("flow state: %v stderr=%s", err, cliErrors.String())
	}
	if !bytes.Contains(cliOutput.Bytes(), []byte(`"secret:A-1"`)) || bytes.Contains(cliOutput.Bytes(), []byte("__dexEncryptedValue")) {
		t.Fatalf("flow state did not decrypt through the dev codec server: %s", cliOutput.String())
	}

	discoveryPath := payloadcodec.DevCodecServerDiscoveryPath(cfg.StateDirectory, cfg.DexPort)
	info, err := os.Stat(discoveryPath)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("discovery file %s: %v %v", discoveryPath, info, err)
	}
	cancel()
	if err := <-runFinished; err != nil {
		t.Fatalf("dev stopped with %v; output:\n%s", err, output.String())
	}
	if _, err := os.Stat(discoveryPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("discovery file still exists after dev stopped: %v", err)
	}
}

type webCodecServerResponse struct {
	Configured bool   `json:"configured"`
	Endpoint   string `json:"endpoint"`
	Token      string `json:"token"`
}

func readWebCodecServer(t *testing.T, webURL string) webCodecServerResponse {
	t.Helper()
	response, err := http.Get(webURL + "/api/codec-server")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var body webCodecServerResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body
}

func startEncryptedOrderApplication(t *testing.T, dexAddress string, keyFilePath string) *dex.Client {
	t.Helper()
	keys, err := dex.NewFilePayloadEncryptionKeyProvider(keyFilePath)
	if err != nil {
		t.Fatal(err)
	}
	codec, err := dex.NewAESGCMPayloadCodec(keys, dex.AESGCMPayloadCodecOptions{})
	if err != nil {
		t.Fatal(err)
	}
	encryption := dex.PayloadEncryptionOptions{Codec: codec}
	registry, err := dex.NewRegistry([]dex.Flow{EncryptedOrderFlow{}})
	if err != nil {
		t.Fatal(err)
	}
	cache, err := blobcache.New(&blobcache.Config{Dir: t.TempDir(), MaxBytes: 64 << 20})
	if err != nil {
		t.Fatal(err)
	}
	worker, err := dex.NewWorker(registry, cache, dex.WorkerOptions{
		FlowServiceAddress: dexAddress,
		Pull:               &dex.PullWorkerOptions{TaskQueue: encryptedOrderTaskQueue, MaxConcurrentTasks: 4},
		PayloadEncryption:  encryption,
	})
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = worker.Start() }()
	registerCtx, cancelRegister := context.WithTimeout(context.Background(), time.Minute)
	defer cancelRegister()
	if err := worker.WaitPullRegistered(registerCtx); err != nil {
		t.Fatal(err)
	}
	client, err := dex.NewClient(registry, cache, dex.ClientOptions{FlowServiceAddress: dexAddress, PayloadEncryption: encryption})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stopCtx, cancelStop := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancelStop()
		_ = worker.Stop(stopCtx)
		_ = client.Close()
		_ = cache.Close()
	})
	return client
}

// waitForStoredEnvelope waits until the server stores the Attribute, and checks that it holds only ciphertext.
func waitForStoredEnvelope(t *testing.T, dexAddress string, flowID string, key string) {
	t.Helper()
	connection, err := grpc.NewClient(dexAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	service := dexpb.NewFlowServiceClient(connection)
	deadline := time.Now().Add(time.Minute)
	for time.Now().Before(deadline) {
		response, err := service.GetAttributes(context.Background(), &dexpb.GetAttributesRequest{FlowId: flowID, Keys: []string{key}})
		if err == nil && len(response.GetAttributes()) == 1 {
			if encoding := response.GetAttributes()[0].GetValue().GetObjValue().GetEncoding(); encoding != payloadcodec.EnvelopeMarker {
				t.Fatalf("server stores Attribute %s as %v, not an envelope", key, response.GetAttributes()[0].GetValue())
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("Attribute %s of %s was never written", key, flowID)
}
