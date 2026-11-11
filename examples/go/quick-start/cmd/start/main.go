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

// Command start starts one OrderFlow. Pass an order ID, or let the command make one.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"

	quickstart "github.com/superdurable/dex/examples/go/quick-start"
	"github.com/superdurable/dex/sdk-go/dex"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() (err error) {
	orderID, err := orderIDFromArgs()
	if err != nil {
		return err
	}
	orderFlow := quickstart.NewOrderFlow(&quickstart.ShopAPI{})
	registry, err := dex.NewRegistry([]dex.Flow{orderFlow})
	if err != nil {
		return err
	}
	cache, err := quickstart.OpenCache(".dex-blob-cache/client")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, cache.Close()) }()
	client, err := dex.NewClient(registry, cache, dex.ClientOptions{
		FlowServiceAddress: quickstart.ServerAddress(),
	})
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, client.Close()) }()
	if _, err := client.StartFlow(context.Background(), orderFlow, orderID, orderID, dex.StartFlowOptions{}); err != nil {
		return err
	}
	fmt.Printf("Started Flow %s. Ship runs in 30 seconds.\n", orderID)
	return nil
}

func orderIDFromArgs() (string, error) {
	if len(os.Args) > 1 {
		return os.Args[1], nil
	}
	randomBytes := make([]byte, 4)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", err
	}
	return "order-" + hex.EncodeToString(randomBytes), nil
}
