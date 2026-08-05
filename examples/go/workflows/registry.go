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

package workflows

import (
	"github.com/superdurable/dex/examples/go/workflows/engagement"
	"github.com/superdurable/dex/examples/go/workflows/microservices"
	"github.com/superdurable/dex/examples/go/workflows/moneytransfer"
	"github.com/superdurable/dex/examples/go/workflows/polling"
	"github.com/superdurable/dex/examples/go/workflows/service"
	"github.com/superdurable/dex/examples/go/workflows/subscription"
	"github.com/superdurable/dex/sdk-go/dex"
)

var (
	applicationService = service.NewMyService()

	Engagement    = engagement.NewEngagementFlow(applicationService)
	Microservices = microservices.NewOrchestrationFlow(applicationService)
	MoneyTransfer = moneytransfer.NewMoneyTransferFlow(applicationService)
	Polling       = polling.NewPollingFlow(applicationService)
	Subscription  = subscription.NewSubscriptionFlow(applicationService)
)

func Flows(additional ...dex.Flow) []dex.Flow {
	flows := []dex.Flow{
		Engagement,
		Microservices,
		MoneyTransfer,
		Polling,
		Subscription,
	}
	return append(flows, additional...)
}
