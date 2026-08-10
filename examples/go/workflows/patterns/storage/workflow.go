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

package storage

import (
	"errors"
	"fmt"

	"github.com/superdurable/dex/sdk-go/dex"
)

const (
	DAStore       = "Store"
	StorageFlowID = "sample-storage-test"
)

var Store = dex.DefineAttributeMap[string](DAStore)

type AddStorageItemRequest struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type StorageFlow struct {
	dex.FlowDefaults
}

func NewStorageFlow() *StorageFlow {
	return &StorageFlow{}
}

func (*StorageFlow) GetFlowType() string {
	return "StorageFlow"
}

func (*StorageFlow) GetSteps() []dex.StepDef {
	return nil
}

func (*StorageFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{Store},
	}
}

func (*StorageFlow) AddItem(
	ctx dex.Context,
	request AddStorageItemRequest,
) (*dex.RPCResult[dex.None], error) {
	if request.Key == "" {
		return nil, fmt.Errorf("key is null")
	}
	if request.Value == "" {
		return nil, fmt.Errorf("value is null")
	}
	if err := Store.Set(ctx, request.Key, request.Value); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

func (*StorageFlow) GetItem(
	ctx dex.Context,
	itemKey string,
) (*dex.RPCResult[string], error) {
	value, err := Store.Get(ctx, itemKey)
	if err != nil {
		var notFound *dex.AttributeNotFoundError
		if errors.As(err, &notFound) {
			return &dex.RPCResult[string]{}, nil
		}
		return nil, err
	}
	return &dex.RPCResult[string]{Output: value}, nil
}

func (*StorageFlow) RemoveItem(
	ctx dex.Context,
	itemKey string,
) (*dex.RPCResult[dex.None], error) {
	if err := Store.Delete(ctx, itemKey); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

var (
	_ dex.Flow                                 = (*StorageFlow)(nil)
	_ dex.RPC[AddStorageItemRequest, dex.None] = (*StorageFlow)(nil).AddItem
	_ dex.RPC[string, string]                  = (*StorageFlow)(nil).GetItem
	_ dex.RPC[string, dex.None]                = (*StorageFlow)(nil).RemoveItem
)
