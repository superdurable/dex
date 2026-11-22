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

import "log"

// FakeEmailService records only each email's key in the shared ledger; the first record is the send.
type FakeEmailService struct {
	ledger *RefundLedger
}

// NewFakeEmailService returns email over ledger.
func NewFakeEmailService(ledger *RefundLedger) *FakeEmailService {
	return &FakeEmailService{ledger: ledger}
}

// SendEmail prints the message the first time it sees idempotencyKey.
func (email *FakeEmailService) SendEmail(to string, body string, idempotencyKey string) error {
	inserted, err := email.ledger.insertEmailKeyOnce(idempotencyKey)
	if err != nil {
		return err
	}
	if inserted {
		log.Printf("Emailed %s: %s", to, body)
	} else {
		log.Printf("Email to %s already sent (key %s)", to, idempotencyKey)
	}
	return nil
}

// ListSentKeys returns the key of every email sent, oldest first.
func (email *FakeEmailService) ListSentKeys() ([]string, error) {
	rows, err := email.ledger.readRows()
	if err != nil {
		return nil, err
	}
	return rows.SentEmailKeys, nil
}
