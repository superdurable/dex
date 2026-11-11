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
	"fmt"
	"sync"
)

// ShopAPI is a mock payment and shipping API that records each call.
type ShopAPI struct {
	mu      sync.Mutex
	charged []string
	shipped []string
}

// Charge records and prints a charge. A real payment API uses idempotencyKey to charge once.
func (shop *ShopAPI) Charge(orderID string, idempotencyKey string) {
	shop.mu.Lock()
	defer shop.mu.Unlock()
	shop.charged = append(shop.charged, orderID)
	fmt.Println("Charged", orderID, "with key", idempotencyKey)
}

// Ship records and prints a shipment.
func (shop *ShopAPI) Ship(orderID string) {
	shop.mu.Lock()
	defer shop.mu.Unlock()
	shop.shipped = append(shop.shipped, orderID)
	fmt.Println("Shipped", orderID)
}

// Charged returns the order IDs charged so far.
func (shop *ShopAPI) Charged() []string {
	shop.mu.Lock()
	defer shop.mu.Unlock()
	return append([]string(nil), shop.charged...)
}

// Shipped returns the order IDs shipped so far.
func (shop *ShopAPI) Shipped() []string {
	shop.mu.Lock()
	defer shop.mu.Unlock()
	return append([]string(nil), shop.shipped...)
}
