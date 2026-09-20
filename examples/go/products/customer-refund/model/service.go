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

package model

import (
	"fmt"
	"sync"
)

const (
	BillingConfirmed = "confirmed"
	BillingDeclined  = "declined"
	BillingUnknown   = "unknown"
)

type RefundCase struct {
	CaseID        string `json:"caseId"`
	Customer      string `json:"customer"`
	CustomerEmail string `json:"customerEmail"`
	CustomerNote  string `json:"customerNote"`
	AmountCents   int64  `json:"amountCents"`
	OrderAgeDays  int64  `json:"orderAgeDays"`
}

type Evidence struct {
	IdentityStatus      string
	SubscriptionStatus  string
	PaymentStatus       string
	UsageStatus         string
	HistoryStatus       string
	IncidentStatus      string
	UsagePercent        int64
	TenureYears         int64
	PriorRefunds        int64
	CancellationPending bool
	IncidentDays        int64
}

type Service interface {
	LookupEvidence(refundCase RefundCase) Evidence
	IssueRefund(idempotencyKey string, refundCase RefundCase) string
	IssueCredit(idempotencyKey string, refundCase RefundCase) string
	LookupBillingOutcome(idempotencyKey string) string
	ApplySubscriptionState(refundCase RefundCase) error
	SendCustomerMessage(refundCase RefundCase, message string) error
}

type FakeService struct {
	mutex         sync.Mutex
	settled       map[string]string
	messages      map[string][]string
	refundCalls   map[string]int
	creditCalls   map[string]int
	subscriptions map[string]int
}

func NewFakeService() *FakeService {
	return &FakeService{
		settled:       make(map[string]string),
		messages:      make(map[string][]string),
		refundCalls:   make(map[string]int),
		creditCalls:   make(map[string]int),
		subscriptions: make(map[string]int),
	}
}

func (*FakeService) LookupEvidence(refundCase RefundCase) Evidence {
	evidence := Evidence{
		IdentityStatus: "present", SubscriptionStatus: "present", PaymentStatus: "present",
		UsageStatus: "confirmed-empty", HistoryStatus: "present", IncidentStatus: "confirmed-empty",
		TenureYears: 2,
	}
	switch refundCase.CaseID {
	case "identity-unavailable", "agent-uncertain":
		evidence.IdentityStatus = "unavailable"
	case "heavy-repeat":
		evidence.UsageStatus = "present"
		evidence.UsagePercent = 92
		evidence.PriorRefunds = 1
	case "missed-cancellation":
		evidence.CancellationPending = true
	case "incident":
		evidence.IncidentStatus = "present"
		evidence.IncidentDays = 6
	case "evidence-unavailable":
		evidence.UsageStatus = "unavailable"
	}
	if refundCase.OrderAgeDays > 14 && evidence.UsageStatus == "confirmed-empty" {
		evidence.UsageStatus = "present"
		evidence.UsagePercent = 71
	}
	if refundCase.Customer == "long-tenure" {
		evidence.TenureYears = 5
	}
	return evidence
}

func (service *FakeService) IssueRefund(idempotencyKey string, refundCase RefundCase) string {
	service.mutex.Lock()
	defer service.mutex.Unlock()
	service.refundCalls[idempotencyKey]++
	if settled, found := service.settled[idempotencyKey]; found {
		return settled
	}
	switch refundCase.CaseID {
	case "declined":
		service.settled[idempotencyKey] = BillingDeclined
		return BillingDeclined
	case "unproven":
		return BillingUnknown
	default:
		service.settled[idempotencyKey] = BillingConfirmed
		return BillingConfirmed
	}
}

func (service *FakeService) IssueCredit(idempotencyKey string, refundCase RefundCase) string {
	service.mutex.Lock()
	defer service.mutex.Unlock()
	service.creditCalls[idempotencyKey]++
	if settled, found := service.settled[idempotencyKey]; found {
		return settled
	}
	if refundCase.CaseID == "declined" {
		service.settled[idempotencyKey] = BillingDeclined
		return BillingDeclined
	}
	service.settled[idempotencyKey] = BillingConfirmed
	return BillingConfirmed
}

func (service *FakeService) LookupBillingOutcome(idempotencyKey string) string {
	service.mutex.Lock()
	defer service.mutex.Unlock()
	if settled, found := service.settled[idempotencyKey]; found {
		return settled
	}
	return BillingUnknown
}

func (service *FakeService) ApplySubscriptionState(refundCase RefundCase) error {
	service.mutex.Lock()
	defer service.mutex.Unlock()
	service.subscriptions[refundCase.CaseID]++
	if refundCase.CaseID == "subscription-failure" {
		return fmt.Errorf("subscription provider rejected update")
	}
	return nil
}

func (service *FakeService) SendCustomerMessage(refundCase RefundCase, message string) error {
	service.mutex.Lock()
	defer service.mutex.Unlock()
	if refundCase.CaseID == "email-failure" {
		return fmt.Errorf("email provider rejected message")
	}
	service.messages[refundCase.CaseID] = append(service.messages[refundCase.CaseID], message)
	return nil
}

func (service *FakeService) RefundCalls(idempotencyKey string) int {
	service.mutex.Lock()
	defer service.mutex.Unlock()
	return service.refundCalls[idempotencyKey]
}

func (service *FakeService) Messages(caseID string) []string {
	service.mutex.Lock()
	defer service.mutex.Unlock()
	return append([]string(nil), service.messages[caseID]...)
}
