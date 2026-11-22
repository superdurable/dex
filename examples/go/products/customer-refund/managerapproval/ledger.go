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
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
)

// DefaultLedgerDirectory holds the ledger that the fake billing and email services share.
const DefaultLedgerDirectory = ".customer-refund"

const (
	ledgerFileName     = "ledger.json"
	ledgerLockFileName = "ledger.lock"
)

type ledgerRefundRow struct {
	RowID          int     `json:"rowId"`
	IdempotencyKey string  `json:"idempotencyKey"`
	OrderID        string  `json:"orderId"`
	AmountUSD      float64 `json:"amountUsd"`
}

type ledgerRows struct {
	Refunds       []ledgerRefundRow `json:"refunds"`
	SentEmailKeys []string          `json:"sentEmailKeys"`
}

// RefundLedger is a file-locked JSON ledger, shared by every process using its directory.
type RefundLedger struct {
	directory string
}

// NewRefundLedger returns the ledger in directory; it creates the files on first use.
func NewRefundLedger(directory string) *RefundLedger {
	return &RefundLedger{directory: directory}
}

// Directory returns the folder that holds the ledger files.
func (ledger *RefundLedger) Directory() string {
	return ledger.directory
}

// insertRefundOnce returns the row for key, adding it first when the key is new.
func (ledger *RefundLedger) insertRefundOnce(key string, orderID string, amountUSD float64) (ledgerRefundRow, bool, error) {
	var row ledgerRefundRow
	inserted := false
	err := ledger.updateRows(func(rows *ledgerRows) bool {
		for _, existing := range rows.Refunds {
			if existing.IdempotencyKey == key {
				row = existing
				return false
			}
		}
		row = ledgerRefundRow{RowID: len(rows.Refunds) + 1, IdempotencyKey: key, OrderID: orderID, AmountUSD: amountUSD}
		rows.Refunds = append(rows.Refunds, row)
		inserted = true
		return true
	})
	return row, inserted, err
}

// insertEmailKeyOnce reports whether key was new; only the first caller sends.
func (ledger *RefundLedger) insertEmailKeyOnce(key string) (bool, error) {
	inserted := false
	err := ledger.updateRows(func(rows *ledgerRows) bool {
		for _, existing := range rows.SentEmailKeys {
			if existing == key {
				return false
			}
		}
		rows.SentEmailKeys = append(rows.SentEmailKeys, key)
		inserted = true
		return true
	})
	return inserted, err
}

func (ledger *RefundLedger) readRows() (ledgerRows, error) {
	var rows ledgerRows
	err := ledger.updateRows(func(current *ledgerRows) bool {
		rows = *current
		return false
	})
	return rows, err
}

// updateRows runs change under the file lock and saves the rows when change returns true.
func (ledger *RefundLedger) updateRows(change func(rows *ledgerRows) bool) error {
	if err := os.MkdirAll(ledger.directory, 0o755); err != nil {
		return fmt.Errorf("create ledger directory: %w", err)
	}
	unlock, err := lockLedgerFile(filepath.Join(ledger.directory, ledgerLockFileName))
	if err != nil {
		return err
	}
	defer func() {
		if err := unlock(); err != nil {
			slog.Error("release refund ledger lock", "error", err)
		}
	}()
	path := filepath.Join(ledger.directory, ledgerFileName)
	var rows ledgerRows
	contents, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return fmt.Errorf("read ledger: %w", err)
	default:
		if err := json.Unmarshal(contents, &rows); err != nil {
			return fmt.Errorf("decode ledger %s: %w", path, err)
		}
	}
	if change(&rows) {
		if err := writeLedgerAtomically(path, rows); err != nil {
			return err
		}
	}
	// A retry may find a renamed ledger whose directory sync failed.
	directory, err := os.Open(ledger.directory)
	if err != nil {
		return fmt.Errorf("open ledger directory: %w", err)
	}
	if err := errors.Join(directory.Sync(), directory.Close()); err != nil {
		return fmt.Errorf("sync ledger directory: %w", err)
	}
	return nil
}

// writeLedgerAtomically renames a complete temporary file, so readers never see half a ledger.
func writeLedgerAtomically(path string, rows ledgerRows) error {
	encoded, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ledgerFileName+".*.tmp")
	if err != nil {
		return fmt.Errorf("write ledger: %w", err)
	}
	_, writeErr := temporary.Write(encoded)
	syncErr := temporary.Sync()
	closeErr := temporary.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return errors.Join(fmt.Errorf("write ledger: %w", err), os.Remove(temporary.Name()))
	}
	if err := os.Rename(temporary.Name(), path); err != nil {
		return errors.Join(fmt.Errorf("replace ledger: %w", err), os.Remove(temporary.Name()))
	}
	return nil
}
