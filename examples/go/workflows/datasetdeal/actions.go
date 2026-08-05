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

package datasetdeal

import (
	"fmt"
	"sort"

	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/sdk-go/logging"
)

const (
	TransferMoneyFromBuyerToSeller = "transferMoneyFromBuyerToSeller"
	TransferMoneyFromSellerToBuyer = "transferMoneyFromSellerToBuyer"
	TransportFullDatasetToBuyer    = "transportFullDatasetToBuyer"
	TransportSampleDatasetToBuyer  = "transportSampleDatasetToBuyer"
)

type ActionInput struct {
	FlowID      string
	ProcessID   string
	BuyerID     string
	TargetState string
	StateData   map[string]string
}

type actionHandler func(dex.Logger, ActionInput) (map[string]string, error)

type actionRegistry struct {
	logger   dex.Logger
	handlers map[string]actionHandler
}

func newActionRegistry(logger dex.Logger) *actionRegistry {
	return &actionRegistry{
		logger: logging.OrDefault(logger),
		handlers: map[string]actionHandler{
			TransferMoneyFromBuyerToSeller: transferMoneyFromBuyerToSeller,
			TransferMoneyFromSellerToBuyer: transferMoneyFromSellerToBuyer,
			TransportFullDatasetToBuyer:    transportFullDatasetToBuyer,
			TransportSampleDatasetToBuyer:  transportSampleDatasetToBuyer,
		},
	}
}

func (registry *actionRegistry) execute(
	name string,
	input ActionInput,
) (map[string]string, error) {
	handler, found := registry.handlers[name]
	if !found {
		return nil, fmt.Errorf("dataset deal action %q is not registered", name)
	}
	updates, err := handler(registry.logger, input)
	if err != nil {
		return nil, err
	}
	if updates == nil {
		updates = make(map[string]string)
	}
	updates["lastAction"] = name
	updates["lastActionStatus"] = "completed"
	return updates, nil
}

func AvailableActionNames() []string {
	names := []string{
		TransferMoneyFromBuyerToSeller,
		TransferMoneyFromSellerToBuyer,
		TransportFullDatasetToBuyer,
		TransportSampleDatasetToBuyer,
	}
	sort.Strings(names)
	return names
}

func transferMoneyFromBuyerToSeller(
	logger dex.Logger,
	input ActionInput,
) (map[string]string, error) {
	logger.Info(
		"dataset deal transferred money from buyer to seller",
		"flow_id", input.FlowID,
		"buyer_id", input.BuyerID,
		"target_state", input.TargetState,
		"sample_price", input.StateData["proposedSamplePrice"],
		"full_price", input.StateData["proposedFullPrice"],
	)
	return nil, nil
}

func transferMoneyFromSellerToBuyer(
	logger dex.Logger,
	input ActionInput,
) (map[string]string, error) {
	logger.Info(
		"dataset deal transferred refund from seller to buyer",
		"flow_id", input.FlowID,
		"buyer_id", input.BuyerID,
		"refund_price", input.StateData["proposedSampleRefundPrice"],
	)
	return nil, nil
}

func transportFullDatasetToBuyer(
	logger dex.Logger,
	input ActionInput,
) (map[string]string, error) {
	logger.Info(
		"dataset deal transported full dataset to buyer",
		"flow_id", input.FlowID,
		"buyer_id", input.BuyerID,
	)
	return map[string]string{"deliveredDataset": "full"}, nil
}

func transportSampleDatasetToBuyer(
	logger dex.Logger,
	input ActionInput,
) (map[string]string, error) {
	logger.Info(
		"dataset deal transported sample dataset to buyer",
		"flow_id", input.FlowID,
		"buyer_id", input.BuyerID,
	)
	return map[string]string{"deliveredDataset": "sample"}, nil
}
