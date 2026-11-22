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
	"fmt"
	"strings"
)

// MockMessageModel answers like a model would, without an API key or a network call.
type MockMessageModel struct{}

// WriteMessage drafts the customer message for a refunded or denied case.
func (*MockMessageModel) WriteMessage(customer string, outcome string, amountUSD float64, note string) string {
	if outcome == outcomeRefunded {
		return strings.TrimSpace(fmt.Sprintf(
			"Hi %s, we are sorry for the service interruption. We refunded %s USD to your card. %s",
			customer, formatUSD(amountUSD), note,
		))
	}
	return strings.TrimSpace(fmt.Sprintf(
		"Hi %s, thank you for reaching out. We cannot refund this charge. %s", customer, note,
	))
}

// formatUSD writes an amount with thousands separators and cents, such as 1,249.00.
func formatUSD(amount float64) string {
	plain := fmt.Sprintf("%.2f", amount)
	whole, cents, _ := strings.Cut(plain, ".")
	sign := ""
	if strings.HasPrefix(whole, "-") {
		sign, whole = "-", whole[1:]
	}
	var grouped strings.Builder
	for index, digit := range whole {
		if index > 0 && (len(whole)-index)%3 == 0 {
			grouped.WriteByte(',')
		}
		grouped.WriteRune(digit)
	}
	return sign + grouped.String() + "." + cents
}
