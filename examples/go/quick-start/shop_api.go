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
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strings"
	"sync"
	"time"
)

// ShopAPI is a mock payment and shipping API that drops a repeated idempotency key.
// The zero value keeps keys in memory; NewFileShopAPI keeps them in a file.
type ShopAPI struct {
	mu           sync.Mutex
	keysPath     string
	chargeDelay  time.Duration
	usedKeys     map[string]bool
	charged      []string
	shipped      []string
	repeatedKeys []string
}

// NewFileShopAPI returns a ShopAPI that remembers keys in keysPath across processes.
// It waits chargeDelay after each new charge.
func NewFileShopAPI(keysPath string, chargeDelay time.Duration) *ShopAPI {
	return &ShopAPI{keysPath: keysPath, chargeDelay: chargeDelay}
}

// Charge charges an order once per idempotencyKey.
func (shop *ShopAPI) Charge(orderID string, idempotencyKey string) error {
	shop.mu.Lock()
	isRepeat, err := shop.markKey(idempotencyKey)
	if err == nil && !isRepeat {
		shop.charged = append(shop.charged, orderID)
	}
	shop.mu.Unlock()
	if err != nil {
		return err
	}
	if isRepeat {
		fmt.Printf("Charge for %s already done (key %s); not charged again\n", orderID, idempotencyKey)
		return nil
	}
	fmt.Println("Charged", orderID, "with key", idempotencyKey)
	// Leaves time to stop the Worker after the payment, before Dex records the Step.
	time.Sleep(shop.chargeDelay)
	return nil
}

// Ship ships an order once per idempotencyKey.
func (shop *ShopAPI) Ship(orderID string, idempotencyKey string) error {
	shop.mu.Lock()
	defer shop.mu.Unlock()
	isRepeat, err := shop.markKey(idempotencyKey)
	if err != nil {
		return err
	}
	if isRepeat {
		fmt.Printf("Ship for %s already done (key %s); not shipped again\n", orderID, idempotencyKey)
		return nil
	}
	shop.shipped = append(shop.shipped, orderID)
	fmt.Println("Shipped", orderID, "with key", idempotencyKey)
	return nil
}

// Charged returns the order IDs charged so far.
func (shop *ShopAPI) Charged() []string {
	shop.mu.Lock()
	defer shop.mu.Unlock()
	return slices.Clone(shop.charged)
}

// Shipped returns the order IDs shipped so far.
func (shop *ShopAPI) Shipped() []string {
	shop.mu.Lock()
	defer shop.mu.Unlock()
	return slices.Clone(shop.shipped)
}

// RepeatedKeys returns the idempotency keys this ShopAPI dropped as repeats.
func (shop *ShopAPI) RepeatedKeys() []string {
	shop.mu.Lock()
	defer shop.mu.Unlock()
	return slices.Clone(shop.repeatedKeys)
}

// markKey reports whether key was used before, and records it if not.
func (shop *ShopAPI) markKey(key string) (bool, error) {
	if err := shop.loadKeys(); err != nil {
		return false, err
	}
	if shop.usedKeys[key] {
		shop.repeatedKeys = append(shop.repeatedKeys, key)
		return true, nil
	}
	shop.usedKeys[key] = true
	return false, shop.saveKeys()
}

func (shop *ShopAPI) loadKeys() error {
	if shop.usedKeys == nil {
		shop.usedKeys = map[string]bool{}
	}
	if shop.keysPath == "" {
		return nil
	}
	data, err := os.ReadFile(shop.keysPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read used keys: %w", err)
	}
	for _, key := range strings.Fields(string(data)) {
		shop.usedKeys[key] = true
	}
	return nil
}

func (shop *ShopAPI) saveKeys() error {
	if shop.keysPath == "" {
		return nil
	}
	keys := make([]string, 0, len(shop.usedKeys))
	for key := range shop.usedKeys {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	data := strings.Join(keys, "\n") + "\n"
	if err := os.WriteFile(shop.keysPath, []byte(data), 0o600); err != nil {
		return fmt.Errorf("write used keys: %w", err)
	}
	return nil
}
