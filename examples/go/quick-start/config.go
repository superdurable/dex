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

package quickstart

import (
	"log"
	"os"
	"strconv"
	"time"

	"github.com/superdurable/dex/blob-cache-go/blobcache"
)

// ServerAddress is the Dex server address, from DEX_FLOW_SERVICE_ADDRESS.
func ServerAddress() string {
	if address := os.Getenv("DEX_FLOW_SERVICE_ADDRESS"); address != "" {
		return address
	}
	return "127.0.0.1:8801"
}

// OpenCache opens a local cache for large Flow values, up to 1 GiB.
func OpenCache(directory string) (*blobcache.Cache, error) {
	return blobcache.New(&blobcache.Config{Dir: directory, MaxBytes: 1 << 30})
}

// HTTPPort is the port of the service, from QUICK_START_HTTP_PORT.
func HTTPPort() string {
	if port := os.Getenv("QUICK_START_HTTP_PORT"); port != "" {
		return port
	}
	return "8080"
}

// ShipDelay returns how long Ship waits, from QUICK_START_SHIP_DELAY_SECONDS; 60 seconds by default.
func ShipDelay() time.Duration {
	value := os.Getenv("QUICK_START_SHIP_DELAY_SECONDS")
	if value == "" {
		return 60 * time.Second
	}
	seconds, err := strconv.Atoi(value)
	if err != nil {
		log.Printf("QUICK_START_SHIP_DELAY_SECONDS=%q is not a whole number; using 60: %v", value, err)
		return 60 * time.Second
	}
	return time.Duration(seconds) * time.Second
}

// NewShopAPIFromEnvironment returns the mock API that the Worker calls, with its keys
// in .shop-api-keys and the charge delay from QUICK_START_CHARGE_DELAY_SECONDS.
func NewShopAPIFromEnvironment() (*ShopAPI, error) {
	delaySeconds := 0.0
	if value := os.Getenv("QUICK_START_CHARGE_DELAY_SECONDS"); value != "" {
		parsed, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return nil, err
		}
		delaySeconds = parsed
	}
	return NewFileShopAPI(".shop-api-keys", time.Duration(delaySeconds*float64(time.Second))), nil
}
