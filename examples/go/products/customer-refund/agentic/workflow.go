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

package agentic

import (
	"errors"
	"fmt"
	"time"

	refundmodel "github.com/superdurable/dex/examples/go/products/customer-refund/model"
	"github.com/superdurable/dex/sdk-go/dex"
)

const (
	statusOpen                 = "open"
	statusGathering            = "gathering"
	statusExecuting            = "executing"
	statusAwaitingManagerRule  = "awaiting-manager-rule"
	statusAwaitingManagerAgent = "awaiting-manager-agent"
	statusAwaitingMessageOK    = "awaiting-message-approval"
	statusResolved             = "resolved"
	statusDenied               = "denied"
	statusRefunded             = "refunded"
	statusCredited             = "credited"
	statusBusinessFailure      = "business-failure"
	statusOutcomeUnknown       = "outcome-unknown"
	statusCustomerUninformed   = "customer-uninformed"
	statusFollowUpSubscription = "follow-up-subscription"
	statusNonConvergence       = "non-convergence"
	statusNotARefund           = "not-a-refund"
	actionIssueRefund          = "IssueRefund"
	actionOfferAccountCredit   = "OfferAccountCredit"
	actionRequestHumanApproval = "RequestHumanApproval"
	managerThresholdCents      = int64(1_000_000)
	decisionRoundsBudget       = int64(12)
)

var agenticInputEmail = dex.DefineAttribute[string]("in-email")

// dex:indexed-attribute attribute-key:customer-email index-key:CustomKeyword index-type:keyword value-type:string description:"Customer email address"
var agenticCustomerEmail = dex.DefineAttribute[string](
	"customer-email",
	dex.Indexed(dex.AttributeIndex{Type: dex.IndexKeyword, IndexKey: "CustomKeyword"}),
)

var agenticChargeReference = dex.DefineAttribute[string]("in-charge-ref")

var agenticPaymentAmount = dex.DefineAttribute[string]("ev-payment-amount")

var agenticIdentityStatus = dex.DefineAttribute[string]("ev-identity-status")

var agenticSubscriptionStatus = dex.DefineAttribute[string]("ev-subscription-status")

var agenticPaymentStatus = dex.DefineAttribute[string]("ev-payment-status")

var agenticPaymentAgeDays = dex.DefineAttribute[int64]("ev-payment-age-days")

var agenticUsageStatus = dex.DefineAttribute[string]("ev-usage-status")

var agenticUsagePercent = dex.DefineAttribute[int64]("ev-usage-pct-of-allowance")

var agenticHistoryStatus = dex.DefineAttribute[string]("ev-history-status")

var agenticTenureYears = dex.DefineAttribute[int64]("ev-history-tenure-years")

var agenticPriorRefunds = dex.DefineAttribute[int64]("ev-history-prior-refunds")

var agenticCancellationPending = dex.DefineAttribute[bool]("ev-history-cancel-requested")

var agenticIncidentStatus = dex.DefineAttribute[string]("ev-incident-status")

var agenticIncidentDays = dex.DefineAttribute[int64]("ev-incident-days")

var agenticEvidenceState = dex.DefineAttribute[string]("evidence-state")

var agenticRecommendedAction = dex.DefineAttribute[string]("recommended-action")

var agenticRationale = dex.DefineAttribute[string]("recommendation-rationale")

var agenticDecisionRounds = dex.DefineAttribute[int64]("decision-rounds")

var agenticGuardrailVerdict = dex.DefineAttribute[string]("guardrail-verdict")

var agenticGuardrailRule = dex.DefineAttribute[string]("guardrail-rule")

var agenticBoundAction = dex.DefineAttribute[string]("bound-action")

var agenticManagerVerdict = dex.DefineAttribute[string]("manager-verdict")

var agenticManagerRejectionReason = dex.DefineAttribute[string]("manager-rejection-reason")

var agenticGateRequestKey = dex.DefineAttribute[string]("gate-request-key")

var agenticGateEntries = dex.DefineAttribute[int64]("gate-entries")

var agenticBillingKey = dex.DefineAttribute[string]("billing-key")

var agenticBillingOutcome = dex.DefineAttribute[string]("billing-outcome")

var agenticSubscriptionApplied = dex.DefineAttribute[string]("subscription-applied")

var agenticEmailSent = dex.DefineAttribute[string]("email-sent")

var agenticOperatorNote = dex.DefineAttribute[string]("operator-note")

// dex:indexed-attribute attribute-key:refund-amount index-key:CustomDouble index-type:double value-type:double description:"Refund amount in dollars"
var agenticRefundAmount = dex.DefineAttribute[float64](
	"refund-amount",
	dex.Indexed(dex.AttributeIndex{Type: dex.IndexDouble, IndexKey: "CustomDouble"}),
)

// The message a person confirms or rewrites before it reaches the customer.
var agenticCustomerMessageDraft = dex.DefineAttribute[string]("customer-message-draft")

// dex:indexed-attribute value-type:string attribute-key:case-status description:"Current case status" index-type:keyword index-key:CustomKeyword2
var agenticCaseStatus = dex.DefineAttribute[string](
	"case-status",
	dex.Indexed(dex.AttributeIndex{Type: dex.IndexKeyword, IndexKey: "CustomKeyword2"}),
)

var agenticManagerApproval = dex.DefineChannelMap[string]("manager-approval")

var agenticMessageApproval = dex.DefineChannelMap[string]("message-approval")

type RejectRefundInput struct {
	Reason         string `json:"reason"`
	GateRequestKey string `json:"gateRequestKey"`
}

type ConfirmCustomerMessageInput struct {
	GateRequestKey string `json:"gateRequestKey"`
}

type EditCustomerMessageInput struct {
	Message        string `json:"message"`
	GateRequestKey string `json:"gateRequestKey"`
}

type AgenticCustomerRefundFlow struct {
	dex.FlowDefaults
	service refundmodel.Service
}

func NewAgenticCustomerRefundFlow(service refundmodel.Service) *AgenticCustomerRefundFlow {
	if service == nil {
		panic("customer refund service is required")
	}
	return &AgenticCustomerRefundFlow{service: service}
}

func (*AgenticCustomerRefundFlow) GetFlowType() string {
	return "AgenticCustomerRefundFlow"
}

func (flow *AgenticCustomerRefundFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(agenticReceiveRequestStep{}),
		dex.DefineStep(agenticDecisionStep{}),
		dex.DefineStep(agenticCheckIdentityStep{service: flow.service}),
		dex.DefineStep(agenticGetSubscriptionStep{service: flow.service}),
		dex.DefineStep(agenticGetPaymentStep{service: flow.service}),
		dex.DefineStep(agenticGetUsageStep{service: flow.service}),
		dex.DefineStep(agenticGetSupportHistoryStep{service: flow.service}),
		dex.DefineStep(agenticCheckIncidentsStep{service: flow.service}),
		dex.DefineStep(agenticGuardrailStep{}),
		dex.DefineStep(agenticReCheckStep{}),
		dex.DefineStep(agenticRequestHumanApprovalStep{}),
		dex.DefineStep(agenticAuditIntentStep{}),
		dex.DefineStep(agenticIssueRefundStep{service: flow.service}),
		dex.DefineStep(agenticOfferAccountCreditStep{service: flow.service}),
		dex.DefineStep(agenticVerifyBillingStep{service: flow.service}),
		dex.DefineStep(agenticApplySubscriptionStep{service: flow.service}),
		dex.DefineStep(agenticDraftCustomerMessageStep{}),
		dex.DefineStep(agenticConfirmCustomerMessageStep{}),
		dex.DefineStep(agenticSendCustomerMessageStep{service: flow.service}),
		dex.DefineStep(agenticNonConvergenceStep{}),
		dex.DefineStep(agenticNotARefundStep{}),
		dex.DefineStep(agenticBillingFailedStep{}),
		dex.DefineStep(agenticSubscriptionFailedStep{}),
		dex.DefineStep(agenticEmailFailedStep{}),
		dex.DefineStep(agenticCloseCaseStep{}),
	}
}

func (flow *AgenticCustomerRefundFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.GetDexSummary, nil),
		dex.DefineRPC(flow.GetDexDisplay, nil),
		dex.DefineRPC(flow.ApproveRefund, &dex.RPCOptions{
			Action: dex.DefineAction(
				"Approve",
				dex.WhenAttributeMatches(
					agenticCaseStatus,
					dex.AttributeMatchEqual(statusAwaitingManagerRule),
					dex.AttributeMatchEqual(statusAwaitingManagerAgent),
				),
				dex.ActionRequiresPermission("refund.manage"),
			),
			LockAttributes: []dex.AttributeLock{
				dex.LockAttribute(agenticCaseStatus),
				dex.LockAttribute(agenticGateRequestKey),
			},
		}),
		dex.DefineRPC(flow.RejectRefund, &dex.RPCOptions{
			Action: dex.DefineAction(
				"Reject",
				dex.WhenAttributeMatches(
					agenticCaseStatus,
					dex.AttributeMatchEqual(statusAwaitingManagerRule),
					dex.AttributeMatchEqual(statusAwaitingManagerAgent),
				),
				dex.ActionRequiresPermission("refund.manage"),
			),
			LockAttributes: []dex.AttributeLock{
				dex.LockAttribute(agenticCaseStatus),
				dex.LockAttribute(agenticGateRequestKey),
			},
		}),
		dex.DefineRPC(flow.ConfirmCustomerMessage, &dex.RPCOptions{
			Action: dex.DefineAction(
				"Send as written",
				dex.WhenAttributeMatches(
					agenticCaseStatus,
					dex.AttributeMatchEqual(statusAwaitingMessageOK),
				),
				dex.ActionRequiresPermission("refund.message"),
			),
			LockAttributes: []dex.AttributeLock{
				dex.LockAttribute(agenticCaseStatus),
				dex.LockAttribute(agenticGateRequestKey),
				dex.LockAttribute(agenticCustomerMessageDraft),
			},
		}),
		dex.DefineRPC(flow.EditCustomerMessage, &dex.RPCOptions{
			Action: dex.DefineAction(
				"Rewrite and send",
				dex.WhenAttributeMatches(
					agenticCaseStatus,
					dex.AttributeMatchEqual(statusAwaitingMessageOK),
				),
				dex.ActionRequiresPermission("refund.message"),
			),
			LockAttributes: []dex.AttributeLock{
				dex.LockAttribute(agenticCaseStatus),
				dex.LockAttribute(agenticGateRequestKey),
				dex.LockAttribute(agenticCustomerMessageDraft),
			},
		}),
	}
}

func (*AgenticCustomerRefundFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{
			agenticInputEmail,
			agenticChargeReference,
			agenticPaymentAmount,
			agenticIdentityStatus,
			agenticSubscriptionStatus,
			agenticPaymentStatus,
			agenticPaymentAgeDays,
			agenticUsageStatus,
			agenticUsagePercent,
			agenticHistoryStatus,
			agenticTenureYears,
			agenticPriorRefunds,
			agenticCancellationPending,
			agenticIncidentStatus,
			agenticIncidentDays,
			agenticEvidenceState,
			agenticRecommendedAction,
			agenticRationale,
			agenticDecisionRounds,
			agenticGuardrailVerdict,
			agenticGuardrailRule,
			agenticBoundAction,
			agenticManagerVerdict,
			agenticManagerRejectionReason,
			agenticGateRequestKey,
			agenticGateEntries,
			agenticBillingKey,
			agenticBillingOutcome,
			agenticSubscriptionApplied,
			agenticEmailSent,
			agenticOperatorNote,
			agenticCaseStatus,
			agenticRefundAmount,
			agenticCustomerEmail,
			agenticCustomerMessageDraft,
		},
		Channels: []dex.ChannelDef{agenticManagerApproval, agenticMessageApproval},
	}
}

// dex:field editable:false description:"Charge reference" value-type:string attribute-key:in-charge-ref
// dex:field attribute-key:ev-payment-amount value-type:string editable:false description:"Payment amount"
// dex:field attribute-key:recommended-action value-type:string editable:false description:"Recommended action"
// dex:field attribute-key:guardrail-rule value-type:string editable:false description:"Guardrail rule"
func (*AgenticCustomerRefundFlow) GetDexSummary(
	ctx dex.Context,
	_ dex.None,
) (*dex.RPCResult[map[string]any], error) {
	chargeReference, err := agenticOptionalDisplayAttribute(ctx, agenticChargeReference)
	if err != nil {
		return nil, err
	}
	paymentAmount, err := agenticOptionalDisplayAttribute(ctx, agenticPaymentAmount)
	if err != nil {
		return nil, err
	}
	recommendedAction, err := agenticOptionalDisplayAttribute(ctx, agenticRecommendedAction)
	if err != nil {
		return nil, err
	}
	guardrailRule, err := agenticOptionalDisplayAttribute(ctx, agenticGuardrailRule)
	if err != nil {
		return nil, err
	}
	return &dex.RPCResult[map[string]any]{Output: map[string]any{
		"in-charge-ref":      chargeReference,
		"ev-payment-amount":  paymentAmount,
		"recommended-action": recommendedAction,
		"guardrail-rule":     guardrailRule,
	}}, nil
}

// dex:field attribute-key:customer-email value-type:string editable:false description:"Customer email" ui-slot:title
// dex:field attribute-key:case-status value-type:string editable:false description:"Case status" ui-slot:status
// dex:field attribute-key:in-email value-type:string editable:false description:"Customer request" ui-slot:subtitle
// dex:field attribute-key:in-charge-ref value-type:string editable:false description:"Charge reference"
// dex:field attribute-key:evidence-state value-type:string editable:false description:"Evidence state"
// dex:field attribute-key:recommended-action value-type:string editable:false description:"Recommendation" ui-slot:recommendation
// dex:field attribute-key:recommendation-rationale value-type:string editable:false description:"Recommendation rationale" ui-slot:reason
// dex:field attribute-key:guardrail-verdict value-type:string editable:false description:"Guardrail verdict"
// dex:field attribute-key:guardrail-rule value-type:string editable:false description:"Guardrail rule" ui-slot:reason
// dex:field attribute-key:manager-verdict value-type:string editable:false description:"Manager verdict"
// dex:field attribute-key:gate-request-key value-type:string editable:false description:"Approval gate"
// dex:field attribute-key:billing-outcome value-type:string editable:false description:"Billing effect"
// dex:field attribute-key:subscription-applied value-type:string editable:false description:"Subscription effect"
// dex:field attribute-key:email-sent value-type:string editable:false description:"Customer message effect"
// dex:field attribute-key:customer-message-draft value-type:string editable:true description:"Customer message"
// dex:field attribute-key:operator-note value-type:string editable:true description:"Operator note"
func (*AgenticCustomerRefundFlow) GetDexDisplay(
	ctx dex.Context,
	_ dex.None,
) (*dex.RPCResult[map[string]any], error) {
	fields := []struct {
		key       string
		attribute dex.Attribute[string]
	}{
		{"customer-email", agenticCustomerEmail},
		{"case-status", agenticCaseStatus},
		{"in-email", agenticInputEmail},
		{"in-charge-ref", agenticChargeReference},
		{"evidence-state", agenticEvidenceState},
		{"recommended-action", agenticRecommendedAction},
		{"recommendation-rationale", agenticRationale},
		{"guardrail-verdict", agenticGuardrailVerdict},
		{"guardrail-rule", agenticGuardrailRule},
		{"manager-verdict", agenticManagerVerdict},
		{"gate-request-key", agenticGateRequestKey},
		{"billing-outcome", agenticBillingOutcome},
		{"subscription-applied", agenticSubscriptionApplied},
		{"email-sent", agenticEmailSent},
		{"customer-message-draft", agenticCustomerMessageDraft},
		{"operator-note", agenticOperatorNote},
	}
	output := make(map[string]any, len(fields))
	for _, field := range fields {
		value, err := agenticOptionalDisplayAttribute(ctx, field.attribute)
		if err != nil {
			return nil, err
		}
		output[field.key] = value
	}
	return &dex.RPCResult[map[string]any]{Output: map[string]any{
		"customer-email":           output["customer-email"],
		"case-status":              output["case-status"],
		"in-email":                 output["in-email"],
		"in-charge-ref":            output["in-charge-ref"],
		"evidence-state":           output["evidence-state"],
		"recommended-action":       output["recommended-action"],
		"recommendation-rationale": output["recommendation-rationale"],
		"guardrail-verdict":        output["guardrail-verdict"],
		"guardrail-rule":           output["guardrail-rule"],
		"manager-verdict":          output["manager-verdict"],
		"gate-request-key":         output["gate-request-key"],
		"billing-outcome":          output["billing-outcome"],
		"subscription-applied":     output["subscription-applied"],
		"email-sent":               output["email-sent"],
		"customer-message-draft":   output["customer-message-draft"],
		"operator-note":            output["operator-note"],
	}}, nil
}

func (*AgenticCustomerRefundFlow) ApproveRefund(
	ctx dex.Context,
	_ dex.None,
) (*dex.RPCResult[dex.None], error) {
	gateRequestKey, err := agenticValidateOpenGate(ctx)
	if err != nil {
		return nil, err
	}
	if err := agenticManagerApproval.Publish(ctx, gateRequestKey, "approve"); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

// dex:input field-name:reason value-type:string source:user required:true description:"Rejection reason"
// dex:input description:"Approval gate" required:true source:attribute attribute-key:gate-request-key value-type:string field-name:gateRequestKey
func (*AgenticCustomerRefundFlow) RejectRefund(
	ctx dex.Context,
	input RejectRefundInput,
) (*dex.RPCResult[dex.None], error) {
	gateRequestKey, err := agenticValidateOpenGate(ctx)
	if err != nil {
		return nil, err
	}
	if input.GateRequestKey != gateRequestKey {
		return nil, fmt.Errorf("approval gate changed; refresh the case before rejecting")
	}
	if input.Reason == "" {
		return nil, fmt.Errorf("rejection reason is required")
	}
	if err := agenticManagerRejectionReason.Set(ctx, input.Reason); err != nil {
		return nil, err
	}
	if err := agenticManagerApproval.Publish(ctx, gateRequestKey, "reject"); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

// dex:input description:"Message gate" required:true source:attribute attribute-key:gate-request-key value-type:string field-name:gateRequestKey
func (*AgenticCustomerRefundFlow) ConfirmCustomerMessage(
	ctx dex.Context,
	input ConfirmCustomerMessageInput,
) (*dex.RPCResult[dex.None], error) {
	gateRequestKey, err := agenticValidateOpenMessageGate(ctx)
	if err != nil {
		return nil, err
	}
	if input.GateRequestKey != gateRequestKey {
		return nil, fmt.Errorf("message gate changed; refresh the case before confirming")
	}
	if err := agenticMessageApproval.Publish(ctx, gateRequestKey, "confirm"); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

// dex:input field-name:message value-type:string source:user required:true description:"Message to send"
// dex:input description:"Message gate" required:true source:attribute attribute-key:gate-request-key value-type:string field-name:gateRequestKey
func (*AgenticCustomerRefundFlow) EditCustomerMessage(
	ctx dex.Context,
	input EditCustomerMessageInput,
) (*dex.RPCResult[dex.None], error) {
	gateRequestKey, err := agenticValidateOpenMessageGate(ctx)
	if err != nil {
		return nil, err
	}
	if input.GateRequestKey != gateRequestKey {
		return nil, fmt.Errorf("message gate changed; refresh the case before rewriting")
	}
	if input.Message == "" {
		return nil, fmt.Errorf("message is required")
	}
	if err := agenticCustomerMessageDraft.Set(ctx, input.Message); err != nil {
		return nil, err
	}
	if err := agenticMessageApproval.Publish(ctx, gateRequestKey, "edited"); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

// dex:group group-id:intake group-label:"Intake"
// dex:explanation text:"Store the inbound refund request and open or reject the case."
type agenticReceiveRequestStep struct {
	dex.StepDefaultsNoWaitFor[refundmodel.RefundCase]
}

func (agenticReceiveRequestStep) GetStepType() string {
	return "ReceiveRequestStep"
}

func (agenticReceiveRequestStep) Execute(
	ctx dex.Context,
	refundCase refundmodel.RefundCase,
) (*dex.StepDecision, error) {
	if err := agenticCustomerEmail.Set(ctx, refundCase.CustomerEmail); err != nil {
		return nil, err
	}
	if err := agenticInputEmail.Set(ctx, refundCase.CustomerNote); err != nil {
		return nil, err
	}
	if err := agenticChargeReference.Set(ctx, refundCase.CaseID); err != nil {
		return nil, err
	}
	if err := agenticOperatorNote.Set(ctx, ""); err != nil {
		return nil, err
	}
	if refundCase.CaseID == "" {
		if err := agenticCaseStatus.Set(ctx, statusNotARefund); err != nil {
			return nil, err
		}
		return dex.GoTo(agenticNotARefundStep{}, refundCase), nil
	}
	if err := agenticCaseStatus.Set(ctx, statusOpen); err != nil {
		return nil, err
	}
	return dex.GoTo(agenticDecisionStep{}, refundCase), nil
}

// dex:group group-id:reasoning group-label:"Reasoning"
// dex:explanation text:"Choose the next capability from gathered evidence and guardrails."
type agenticDecisionStep struct {
	dex.StepDefaultsNoWaitFor[refundmodel.RefundCase]
}

func (agenticDecisionStep) GetStepType() string {
	return "AgentDecisionStep"
}

func (agenticDecisionStep) Execute(
	ctx dex.Context,
	refundCase refundmodel.RefundCase,
) (*dex.StepDecision, error) {
	rounds, _, err := agenticOptionalAttribute(ctx, agenticDecisionRounds)
	if err != nil {
		return nil, err
	}
	if rounds >= decisionRoundsBudget {
		return dex.GoTo(agenticNonConvergenceStep{}, refundCase), nil
	}
	if err := agenticDecisionRounds.Set(ctx, rounds+1); err != nil {
		return nil, err
	}
	if err := agenticCaseStatus.Set(ctx, statusGathering); err != nil {
		return nil, err
	}
	_, hasIdentity, err := agenticOptionalAttribute(ctx, agenticIdentityStatus)
	if err != nil {
		return nil, err
	}
	if !hasIdentity {
		return dex.GoTo(agenticCheckIdentityStep{}, refundCase), nil
	}
	_, hasSubscription, err := agenticOptionalAttribute(ctx, agenticSubscriptionStatus)
	if err != nil {
		return nil, err
	}
	if !hasSubscription {
		return dex.GoTo(agenticGetSubscriptionStep{}, refundCase), nil
	}
	_, hasPayment, err := agenticOptionalAttribute(ctx, agenticPaymentStatus)
	if err != nil {
		return nil, err
	}
	if !hasPayment {
		return dex.GoTo(agenticGetPaymentStep{}, refundCase), nil
	}
	_, hasUsage, err := agenticOptionalAttribute(ctx, agenticUsageStatus)
	if err != nil {
		return nil, err
	}
	if !hasUsage {
		return dex.GoTo(agenticGetUsageStep{}, refundCase), nil
	}
	_, hasHistory, err := agenticOptionalAttribute(ctx, agenticHistoryStatus)
	if err != nil {
		return nil, err
	}
	if !hasHistory {
		return dex.GoTo(agenticGetSupportHistoryStep{}, refundCase), nil
	}
	_, hasIncidents, err := agenticOptionalAttribute(ctx, agenticIncidentStatus)
	if err != nil {
		return nil, err
	}
	if !hasIncidents {
		return dex.GoTo(agenticCheckIncidentsStep{}, refundCase), nil
	}
	action, rationale, err := agenticChooseAction(ctx, refundCase)
	if err != nil {
		return nil, err
	}
	if err := agenticRecommendedAction.Set(ctx, action); err != nil {
		return nil, err
	}
	if err := agenticRationale.Set(ctx, rationale); err != nil {
		return nil, err
	}
	return dex.GoTo(agenticGuardrailStep{}, refundCase), nil
}

// dex:group group-id:evidence group-label:"Evidence"
// dex:explanation text:"Verify the customer identity before collecting further evidence."
type agenticCheckIdentityStep struct {
	dex.StepDefaultsNoWaitFor[refundmodel.RefundCase]
	service refundmodel.Service
}

func (agenticCheckIdentityStep) GetStepType() string {
	return "CheckIdentityStep"
}

func (step agenticCheckIdentityStep) Execute(
	ctx dex.Context,
	refundCase refundmodel.RefundCase,
) (*dex.StepDecision, error) {
	if err := agenticIdentityStatus.Set(ctx, step.service.LookupEvidence(refundCase).IdentityStatus); err != nil {
		return nil, err
	}
	return dex.GoTo(agenticDecisionStep{}, refundCase), nil
}

// dex:group group-id:evidence group-label:"Evidence"
// dex:explanation text:"Load the customer's subscription details for the case."
type agenticGetSubscriptionStep struct {
	dex.StepDefaultsNoWaitFor[refundmodel.RefundCase]
	service refundmodel.Service
}

func (agenticGetSubscriptionStep) GetStepType() string {
	return "GetSubscriptionStep"
}

func (step agenticGetSubscriptionStep) Execute(
	ctx dex.Context,
	refundCase refundmodel.RefundCase,
) (*dex.StepDecision, error) {
	if err := agenticSubscriptionStatus.Set(ctx, step.service.LookupEvidence(refundCase).SubscriptionStatus); err != nil {
		return nil, err
	}
	return dex.GoTo(agenticDecisionStep{}, refundCase), nil
}

// dex:group group-id:evidence group-label:"Evidence"
// dex:explanation text:"Load the payment and charge evidence for the refund."
type agenticGetPaymentStep struct {
	dex.StepDefaultsNoWaitFor[refundmodel.RefundCase]
	service refundmodel.Service
}

func (agenticGetPaymentStep) GetStepType() string {
	return "GetPaymentStep"
}

func (step agenticGetPaymentStep) Execute(
	ctx dex.Context,
	refundCase refundmodel.RefundCase,
) (*dex.StepDecision, error) {
	evidence := step.service.LookupEvidence(refundCase)
	if err := agenticPaymentStatus.Set(ctx, evidence.PaymentStatus); err != nil {
		return nil, err
	}
	if err := agenticPaymentAgeDays.Set(ctx, refundCase.OrderAgeDays); err != nil {
		return nil, err
	}
	if err := agenticRefundAmount.Set(ctx, float64(refundCase.AmountCents)/100); err != nil {
		return nil, err
	}
	if err := agenticPaymentAmount.Set(ctx, fmt.Sprintf("%.2f", float64(refundCase.AmountCents)/100)); err != nil {
		return nil, err
	}
	return dex.GoTo(agenticDecisionStep{}, refundCase), nil
}

// dex:group group-id:evidence group-label:"Evidence"
// dex:explanation text:"Load usage signals that may support or deny a refund."
type agenticGetUsageStep struct {
	dex.StepDefaultsNoWaitFor[refundmodel.RefundCase]
	service refundmodel.Service
}

func (agenticGetUsageStep) GetStepType() string {
	return "GetUsageStep"
}

func (step agenticGetUsageStep) Execute(
	ctx dex.Context,
	refundCase refundmodel.RefundCase,
) (*dex.StepDecision, error) {
	evidence := step.service.LookupEvidence(refundCase)
	if err := agenticUsageStatus.Set(ctx, evidence.UsageStatus); err != nil {
		return nil, err
	}
	if err := agenticUsagePercent.Set(ctx, evidence.UsagePercent); err != nil {
		return nil, err
	}
	return dex.GoTo(agenticDecisionStep{}, refundCase), nil
}

// dex:group group-id:evidence group-label:"Evidence"
// dex:explanation text:"Load prior support history for this customer."
type agenticGetSupportHistoryStep struct {
	dex.StepDefaultsNoWaitFor[refundmodel.RefundCase]
	service refundmodel.Service
}

func (agenticGetSupportHistoryStep) GetStepType() string {
	return "GetSupportHistoryStep"
}

func (step agenticGetSupportHistoryStep) Execute(
	ctx dex.Context,
	refundCase refundmodel.RefundCase,
) (*dex.StepDecision, error) {
	evidence := step.service.LookupEvidence(refundCase)
	if err := agenticHistoryStatus.Set(ctx, evidence.HistoryStatus); err != nil {
		return nil, err
	}
	if err := agenticTenureYears.Set(ctx, evidence.TenureYears); err != nil {
		return nil, err
	}
	if err := agenticPriorRefunds.Set(ctx, evidence.PriorRefunds); err != nil {
		return nil, err
	}
	if err := agenticCancellationPending.Set(ctx, evidence.CancellationPending); err != nil {
		return nil, err
	}
	return dex.GoTo(agenticDecisionStep{}, refundCase), nil
}

// dex:group group-id:evidence group-label:"Evidence"
// dex:explanation text:"Check for active incidents that affect refund policy."
type agenticCheckIncidentsStep struct {
	dex.StepDefaultsNoWaitFor[refundmodel.RefundCase]
	service refundmodel.Service
}

func (agenticCheckIncidentsStep) GetStepType() string {
	return "CheckIncidentsStep"
}

func (step agenticCheckIncidentsStep) Execute(
	ctx dex.Context,
	refundCase refundmodel.RefundCase,
) (*dex.StepDecision, error) {
	evidence := step.service.LookupEvidence(refundCase)
	if err := agenticIncidentStatus.Set(ctx, evidence.IncidentStatus); err != nil {
		return nil, err
	}
	if err := agenticIncidentDays.Set(ctx, evidence.IncidentDays); err != nil {
		return nil, err
	}
	if err := agenticEvidenceState.Set(ctx, "complete"); err != nil {
		return nil, err
	}
	return dex.GoTo(agenticDecisionStep{}, refundCase), nil
}

// dex:group group-id:control group-label:"Control"
// dex:explanation text:"Apply refund guardrails and set the recommended action."
type agenticGuardrailStep struct {
	dex.StepDefaultsNoWaitFor[refundmodel.RefundCase]
}

func (agenticGuardrailStep) GetStepType() string {
	return "GuardrailStep"
}

func (agenticGuardrailStep) Execute(
	ctx dex.Context,
	refundCase refundmodel.RefundCase,
) (*dex.StepDecision, error) {
	action, err := agenticRecommendedAction.Get(ctx)
	if err != nil {
		return nil, err
	}
	verdict := "auto"
	rule := "standard-policy"
	status := statusExecuting
	if action == actionRequestHumanApproval {
		verdict = "escalate-agent"
		rule = "agent-uncertainty"
		status = statusAwaitingManagerAgent
	} else if refundCase.AmountCents > managerThresholdCents || refundCase.CaseID == "rule-escalation" || refundCase.CaseID == "incident" {
		verdict = "escalate-rule"
		rule = "manager-approval-required"
		status = statusAwaitingManagerRule
	}
	if err := agenticGuardrailVerdict.Set(ctx, verdict); err != nil {
		return nil, err
	}
	if err := agenticGuardrailRule.Set(ctx, rule); err != nil {
		return nil, err
	}
	if err := agenticCaseStatus.Set(ctx, status); err != nil {
		return nil, err
	}
	if verdict != "auto" {
		gateEntries, _, getErr := agenticOptionalAttribute(ctx, agenticGateEntries)
		if getErr != nil {
			return nil, getErr
		}
		gateEntries++
		if err := agenticGateEntries.Set(ctx, gateEntries); err != nil {
			return nil, err
		}
		if err := agenticGateRequestKey.Set(ctx, fmt.Sprintf("%s:gate:%d", refundCase.CaseID, gateEntries)); err != nil {
			return nil, err
		}
		return dex.GoTo(agenticRequestHumanApprovalStep{}, refundCase), nil
	}
	if err := agenticBoundAction.Set(ctx, action); err != nil {
		return nil, err
	}
	return dex.GoTo(agenticAuditIntentStep{}, refundCase), nil
}

// dex:group group-id:control group-label:"Control"
// dex:explanation text:"Re-check evidence after a capability returns to the decision loop."
type agenticReCheckStep struct {
	dex.StepDefaultsNoWaitFor[refundmodel.RefundCase]
}

func (agenticReCheckStep) GetStepType() string {
	return "ReCheckStep"
}

func (agenticReCheckStep) Execute(
	ctx dex.Context,
	refundCase refundmodel.RefundCase,
) (*dex.StepDecision, error) {
	verdict, err := agenticManagerVerdict.Get(ctx)
	if err != nil {
		return nil, err
	}
	if verdict != "approve" {
		if err := agenticCaseStatus.Set(ctx, statusDenied); err != nil {
			return nil, err
		}
		return dex.GoTo(agenticDraftCustomerMessageStep{}, refundCase), nil
	}
	if err := agenticBoundAction.Set(ctx, actionIssueRefund); err != nil {
		return nil, err
	}
	if err := agenticCaseStatus.Set(ctx, statusExecuting); err != nil {
		return nil, err
	}
	return dex.GoTo(agenticAuditIntentStep{}, refundCase), nil
}

// dex:group group-id:control group-label:"Control"
// dex:explanation text:"Ask a human to approve or reject the recommended refund action."
type agenticRequestHumanApprovalStep struct {
	dex.StepDefaults
}

func (agenticRequestHumanApprovalStep) GetStepType() string {
	return "RequestHumanApprovalStep"
}

func (agenticRequestHumanApprovalStep) WaitFor(
	ctx dex.Context,
	_ refundmodel.RefundCase,
) (*dex.Wait, error) {
	gateRequestKey, err := agenticGateRequestKey.Get(ctx)
	if err != nil {
		return nil, err
	}
	return dex.Until(agenticManagerApproval.ForOne(gateRequestKey)), nil
}

func (agenticRequestHumanApprovalStep) Execute(
	ctx dex.Context,
	refundCase refundmodel.RefundCase,
) (*dex.StepDecision, error) {
	gateRequestKey, err := agenticGateRequestKey.Get(ctx)
	if err != nil {
		return nil, err
	}
	verdicts, err := agenticManagerApproval.GetConditionResults(ctx, gateRequestKey)
	if err != nil {
		return nil, err
	}
	if len(verdicts) != 1 {
		return nil, fmt.Errorf("approval gate expected one verdict")
	}
	if err := agenticManagerVerdict.Set(ctx, verdicts[0]); err != nil {
		return nil, err
	}
	return dex.GoTo(agenticReCheckStep{}, refundCase), nil
}

// dex:group group-id:resolution group-label:"Resolution"
// dex:explanation text:"Record the approved refund intent before billing changes."
type agenticAuditIntentStep struct {
	dex.StepDefaultsNoWaitFor[refundmodel.RefundCase]
}

func (agenticAuditIntentStep) GetStepType() string {
	return "AuditIntentStep"
}

func (agenticAuditIntentStep) Execute(
	ctx dex.Context,
	refundCase refundmodel.RefundCase,
) (*dex.StepDecision, error) {
	action, err := agenticBoundAction.Get(ctx)
	if err != nil {
		return nil, err
	}
	if action == actionOfferAccountCredit {
		return dex.GoTo(agenticOfferAccountCreditStep{}, refundCase), nil
	}
	return dex.GoTo(agenticIssueRefundStep{}, refundCase), nil
}

// dex:group group-id:resolution group-label:"Resolution"
// dex:explanation text:"Issue the refund through billing."
type agenticIssueRefundStep struct {
	dex.StepDefaultsNoWaitFor[refundmodel.RefundCase]
	service refundmodel.Service
}

func (agenticIssueRefundStep) GetStepType() string {
	return "IssueRefundStep"
}

func (agenticIssueRefundStep) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		ExecuteRetry: &dex.RetryPolicy{
			InitialInterval: time.Second, BackoffCoefficient: 2, MaximumInterval: 10 * time.Second, MaximumAttempts: 4,
		},
		ExecuteFailure: dex.ProceedToOnExecuteFailure(agenticBillingFailedStep{}, nil),
	}
}

func (step agenticIssueRefundStep) Execute(
	ctx dex.Context,
	refundCase refundmodel.RefundCase,
) (*dex.StepDecision, error) {
	key := refundCase.CaseID + ":refund"
	if err := agenticBillingKey.Set(ctx, key); err != nil {
		return nil, err
	}
	outcome := step.service.IssueRefund(key, refundCase)
	if err := agenticBillingOutcome.Set(ctx, outcome); err != nil {
		return nil, err
	}
	switch outcome {
	case refundmodel.BillingConfirmed:
		if err := agenticCaseStatus.Set(ctx, statusRefunded); err != nil {
			return nil, err
		}
		return dex.GoTo(agenticApplySubscriptionStep{}, refundCase), nil
	case refundmodel.BillingUnknown:
		return dex.GoTo(agenticVerifyBillingStep{}, refundCase), nil
	default:
		return dex.GoTo(agenticBillingFailedStep{}, refundCase), nil
	}
}

// dex:group group-id:resolution group-label:"Resolution"
// dex:explanation text:"Offer account credit instead of a cash refund."
type agenticOfferAccountCreditStep struct {
	dex.StepDefaultsNoWaitFor[refundmodel.RefundCase]
	service refundmodel.Service
}

func (agenticOfferAccountCreditStep) GetStepType() string {
	return "OfferAccountCreditStep"
}

func (step agenticOfferAccountCreditStep) Execute(
	ctx dex.Context,
	refundCase refundmodel.RefundCase,
) (*dex.StepDecision, error) {
	key := refundCase.CaseID + ":credit"
	if err := agenticBillingKey.Set(ctx, key); err != nil {
		return nil, err
	}
	outcome := step.service.IssueCredit(key, refundCase)
	if err := agenticBillingOutcome.Set(ctx, outcome); err != nil {
		return nil, err
	}
	if outcome != refundmodel.BillingConfirmed {
		return dex.GoTo(agenticBillingFailedStep{}, refundCase), nil
	}
	if err := agenticCaseStatus.Set(ctx, statusCredited); err != nil {
		return nil, err
	}
	return dex.GoTo(agenticApplySubscriptionStep{}, refundCase), nil
}

// dex:group group-id:resolution group-label:"Resolution"
// dex:explanation text:"Verify the billing outcome after a refund or credit."
type agenticVerifyBillingStep struct {
	dex.StepDefaultsNoWaitFor[refundmodel.RefundCase]
	service refundmodel.Service
}

func (agenticVerifyBillingStep) GetStepType() string {
	return "VerifyBillingStep"
}

func (step agenticVerifyBillingStep) Execute(
	ctx dex.Context,
	refundCase refundmodel.RefundCase,
) (*dex.StepDecision, error) {
	key, err := agenticBillingKey.Get(ctx)
	if err != nil {
		return nil, err
	}
	outcome := step.service.LookupBillingOutcome(key)
	if err := agenticBillingOutcome.Set(ctx, outcome); err != nil {
		return nil, err
	}
	if outcome == refundmodel.BillingConfirmed {
		if err := agenticCaseStatus.Set(ctx, statusRefunded); err != nil {
			return nil, err
		}
		return dex.GoTo(agenticApplySubscriptionStep{}, refundCase), nil
	}
	if outcome == refundmodel.BillingUnknown {
		if err := agenticCaseStatus.Set(ctx, statusOutcomeUnknown); err != nil {
			return nil, err
		}
		return dex.GoTo(agenticCloseCaseStep{}, refundCase), nil
	}
	return dex.GoTo(agenticBillingFailedStep{}, refundCase), nil
}

// dex:group group-id:resolution group-label:"Resolution"
// dex:explanation text:"Apply any subscription change required by the resolution."
type agenticApplySubscriptionStep struct {
	dex.StepDefaultsNoWaitFor[refundmodel.RefundCase]
	service refundmodel.Service
}

func (agenticApplySubscriptionStep) GetStepType() string {
	return "ApplySubscriptionStep"
}

func (agenticApplySubscriptionStep) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{ExecuteFailure: dex.ProceedToOnExecuteFailure(agenticSubscriptionFailedStep{}, nil)}
}

func (step agenticApplySubscriptionStep) Execute(
	ctx dex.Context,
	refundCase refundmodel.RefundCase,
) (*dex.StepDecision, error) {
	if err := step.service.ApplySubscriptionState(refundCase); err != nil {
		return nil, err
	}
	if err := agenticSubscriptionApplied.Set(ctx, "yes"); err != nil {
		return nil, err
	}
	return dex.GoTo(agenticDraftCustomerMessageStep{}, refundCase), nil
}

// dex:group group-id:resolution group-label:"Resolution"
// dex:explanation text:"Draft the customer message and open it for a person to confirm."
type agenticDraftCustomerMessageStep struct {
	dex.StepDefaultsNoWaitFor[refundmodel.RefundCase]
}

func (agenticDraftCustomerMessageStep) GetStepType() string {
	return "DraftCustomerMessageStep"
}

func (agenticDraftCustomerMessageStep) Execute(
	ctx dex.Context,
	refundCase refundmodel.RefundCase,
) (*dex.StepDecision, error) {
	status, err := agenticCaseStatus.Get(ctx)
	if err != nil {
		return nil, err
	}
	message := "We have finished reviewing your request."
	switch status {
	case statusRefunded:
		message = "Your refund has been issued."
	case statusCredited:
		message = "We applied account credit."
	case statusDenied:
		message = "We cannot approve this refund."
	case statusBusinessFailure:
		message = "We could not process a refund on this charge."
	}
	if err := agenticCustomerMessageDraft.Set(ctx, message); err != nil {
		return nil, err
	}
	// Same counter and key Attribute as the approval gate; the channel is what differs,
	// so the two gates cannot consume each other's answer.
	gateEntries, _, getErr := agenticOptionalAttribute(ctx, agenticGateEntries)
	if getErr != nil {
		return nil, getErr
	}
	gateEntries++
	if err := agenticGateEntries.Set(ctx, gateEntries); err != nil {
		return nil, err
	}
	if err := agenticGateRequestKey.Set(ctx, fmt.Sprintf("%s:gate:%d", refundCase.CaseID, gateEntries)); err != nil {
		return nil, err
	}
	if err := agenticCaseStatus.Set(ctx, statusAwaitingMessageOK); err != nil {
		return nil, err
	}
	return dex.GoTo(agenticConfirmCustomerMessageStep{}, refundCase), nil
}

// dex:group group-id:resolution group-label:"Resolution"
// dex:explanation text:"Wait for a person to confirm or rewrite the customer message."
type agenticConfirmCustomerMessageStep struct {
	dex.StepDefaults
}

func (agenticConfirmCustomerMessageStep) GetStepType() string {
	return "ConfirmCustomerMessageStep"
}

func (agenticConfirmCustomerMessageStep) WaitFor(
	ctx dex.Context,
	_ refundmodel.RefundCase,
) (*dex.Wait, error) {
	gateRequestKey, err := agenticGateRequestKey.Get(ctx)
	if err != nil {
		return nil, err
	}
	return dex.Until(agenticMessageApproval.ForOne(gateRequestKey)), nil
}

func (agenticConfirmCustomerMessageStep) Execute(
	ctx dex.Context,
	refundCase refundmodel.RefundCase,
) (*dex.StepDecision, error) {
	gateRequestKey, err := agenticGateRequestKey.Get(ctx)
	if err != nil {
		return nil, err
	}
	verdicts, err := agenticMessageApproval.GetConditionResults(ctx, gateRequestKey)
	if err != nil {
		return nil, err
	}
	if len(verdicts) != 1 {
		return nil, fmt.Errorf("message gate expected one verdict")
	}
	return dex.GoTo(agenticSendCustomerMessageStep{}, refundCase), nil
}

// dex:group group-id:resolution group-label:"Resolution"
// dex:explanation text:"Send the customer the message a person confirmed."
type agenticSendCustomerMessageStep struct {
	dex.StepDefaultsNoWaitFor[refundmodel.RefundCase]
	service refundmodel.Service
}

func (agenticSendCustomerMessageStep) GetStepType() string {
	return "SendCustomerMessageStep"
}

func (agenticSendCustomerMessageStep) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{ExecuteFailure: dex.ProceedToOnExecuteFailure(agenticEmailFailedStep{}, nil)}
}

func (step agenticSendCustomerMessageStep) Execute(
	ctx dex.Context,
	refundCase refundmodel.RefundCase,
) (*dex.StepDecision, error) {
	status, err := agenticCaseStatus.Get(ctx)
	if err != nil {
		return nil, err
	}
	// Whatever the person confirmed, not a freshly composed message.
	message, err := agenticCustomerMessageDraft.Get(ctx)
	if err != nil {
		return nil, err
	}
	if err := step.service.SendCustomerMessage(refundCase, message); err != nil {
		return nil, err
	}
	if err := agenticEmailSent.Set(ctx, "yes"); err != nil {
		return nil, err
	}
	if status != statusDenied && status != statusBusinessFailure {
		if err := agenticCaseStatus.Set(ctx, statusResolved); err != nil {
			return nil, err
		}
	}
	return dex.GoTo(agenticCloseCaseStep{}, refundCase), nil
}

// dex:group group-id:failure group-label:"Failure"
// dex:explanation text:"Close the loop when the agent cannot converge on an action."
type agenticNonConvergenceStep struct {
	dex.StepDefaultsNoWaitFor[refundmodel.RefundCase]
}

func (agenticNonConvergenceStep) GetStepType() string {
	return "NonConvergenceStep"
}

func (agenticNonConvergenceStep) Execute(
	ctx dex.Context,
	refundCase refundmodel.RefundCase,
) (*dex.StepDecision, error) {
	if err := agenticCaseStatus.Set(ctx, statusNonConvergence); err != nil {
		return nil, err
	}
	return dex.GoTo(agenticCloseCaseStep{}, refundCase), nil
}

// dex:group group-id:failure group-label:"Failure"
// dex:explanation text:"Mark the case as not a refund request."
type agenticNotARefundStep struct {
	dex.StepDefaultsNoWaitFor[refundmodel.RefundCase]
}

func (agenticNotARefundStep) GetStepType() string {
	return "NotARefundStep"
}

func (agenticNotARefundStep) Execute(
	_ dex.Context,
	refundCase refundmodel.RefundCase,
) (*dex.StepDecision, error) {
	return dex.GoTo(agenticCloseCaseStep{}, refundCase), nil
}

// dex:group group-id:failure group-label:"Failure"
// dex:explanation text:"Handle a billing failure during refund or credit."
type agenticBillingFailedStep struct {
	dex.StepDefaultsNoWaitFor[refundmodel.RefundCase]
}

func (agenticBillingFailedStep) GetStepType() string {
	return "BillingFailedStep"
}

func (agenticBillingFailedStep) Execute(
	ctx dex.Context,
	refundCase refundmodel.RefundCase,
) (*dex.StepDecision, error) {
	if err := agenticBillingOutcome.Set(ctx, refundmodel.BillingDeclined); err != nil {
		return nil, err
	}
	if err := agenticCaseStatus.Set(ctx, statusBusinessFailure); err != nil {
		return nil, err
	}
	return dex.GoTo(agenticDraftCustomerMessageStep{}, refundCase), nil
}

// dex:group group-id:failure group-label:"Failure"
// dex:explanation text:"Handle a subscription update failure after a resolution."
type agenticSubscriptionFailedStep struct {
	dex.StepDefaultsNoWaitFor[refundmodel.RefundCase]
}

func (agenticSubscriptionFailedStep) GetStepType() string {
	return "SubscriptionFailedStep"
}

func (agenticSubscriptionFailedStep) Execute(
	ctx dex.Context,
	refundCase refundmodel.RefundCase,
) (*dex.StepDecision, error) {
	if err := agenticSubscriptionApplied.Set(ctx, "no"); err != nil {
		return nil, err
	}
	if err := agenticCaseStatus.Set(ctx, statusFollowUpSubscription); err != nil {
		return nil, err
	}
	return dex.GoTo(agenticDraftCustomerMessageStep{}, refundCase), nil
}

// dex:group group-id:failure group-label:"Failure"
// dex:explanation text:"Handle a failure sending the customer resolution message."
type agenticEmailFailedStep struct {
	dex.StepDefaultsNoWaitFor[refundmodel.RefundCase]
}

func (agenticEmailFailedStep) GetStepType() string {
	return "EmailFailedStep"
}

func (agenticEmailFailedStep) Execute(
	ctx dex.Context,
	refundCase refundmodel.RefundCase,
) (*dex.StepDecision, error) {
	if err := agenticEmailSent.Set(ctx, "no"); err != nil {
		return nil, err
	}
	if err := agenticCaseStatus.Set(ctx, statusCustomerUninformed); err != nil {
		return nil, err
	}
	return dex.GoTo(agenticCloseCaseStep{}, refundCase), nil
}

// dex:group group-id:close group-label:"Close"
// dex:explanation text:"Close the refund case once resolution is final."
type agenticCloseCaseStep struct {
	dex.StepDefaultsNoWaitFor[refundmodel.RefundCase]
}

func (agenticCloseCaseStep) GetStepType() string {
	return "CloseCaseStep"
}

func (agenticCloseCaseStep) Execute(
	_ dex.Context,
	refundCase refundmodel.RefundCase,
) (*dex.StepDecision, error) {
	return dex.GracefulComplete("refund:" + refundCase.CaseID), nil
}

func agenticChooseAction(
	ctx dex.Context,
	refundCase refundmodel.RefundCase,
) (string, string, error) {
	identityStatus, err := agenticIdentityStatus.Get(ctx)
	if err != nil {
		return "", "", err
	}
	usageStatus, err := agenticUsageStatus.Get(ctx)
	if err != nil {
		return "", "", err
	}
	usagePercent, err := agenticUsagePercent.Get(ctx)
	if err != nil {
		return "", "", err
	}
	priorRefunds, err := agenticPriorRefunds.Get(ctx)
	if err != nil {
		return "", "", err
	}
	cancellationPending, err := agenticCancellationPending.Get(ctx)
	if err != nil {
		return "", "", err
	}
	incidentStatus, err := agenticIncidentStatus.Get(ctx)
	if err != nil {
		return "", "", err
	}
	if identityStatus == "unavailable" || usageStatus == "unavailable" {
		return actionRequestHumanApproval, "material evidence is unavailable", nil
	}
	if cancellationPending {
		return actionIssueRefund, "the cancellation request was not actioned", nil
	}
	if incidentStatus == "present" {
		return actionIssueRefund, "a documented incident requires manager judgment", nil
	}
	if refundCase.OrderAgeDays <= 30 && usageStatus == "confirmed-empty" {
		return actionIssueRefund, "inside the refund window with no recorded usage", nil
	}
	if usagePercent >= 80 && priorRefunds > 0 {
		return actionOfferAccountCredit, "heavy usage and a prior refund favor account credit", nil
	}
	if refundCase.OrderAgeDays > 30 {
		return actionOfferAccountCredit, "outside the standard refund window", nil
	}
	return actionIssueRefund, "available evidence supports a refund", nil
}

func agenticValidateOpenMessageGate(ctx dex.Context) (string, error) {
	status, err := agenticCaseStatus.Get(ctx)
	if err != nil {
		return "", err
	}
	if status != statusAwaitingMessageOK {
		return "", fmt.Errorf("case is not awaiting a customer message decision")
	}
	gateRequestKey, err := agenticGateRequestKey.Get(ctx)
	if err != nil {
		return "", err
	}
	if gateRequestKey == "" {
		return "", fmt.Errorf("message gate is missing")
	}
	return gateRequestKey, nil
}

func agenticValidateOpenGate(ctx dex.Context) (string, error) {
	status, err := agenticCaseStatus.Get(ctx)
	if err != nil {
		return "", err
	}
	if status != statusAwaitingManagerRule && status != statusAwaitingManagerAgent {
		return "", fmt.Errorf("refund is not awaiting manager action")
	}
	gateRequestKey, err := agenticGateRequestKey.Get(ctx)
	if err != nil {
		return "", err
	}
	if gateRequestKey == "" {
		return "", fmt.Errorf("approval gate is missing")
	}
	return gateRequestKey, nil
}

func agenticOptionalAttribute[T any](
	ctx dex.Context,
	attribute dex.Attribute[T],
) (T, bool, error) {
	value, err := attribute.Get(ctx)
	if err == nil {
		return value, true, nil
	}
	var notFound *dex.AttributeNotFoundError
	if errors.As(err, &notFound) {
		var zero T
		return zero, false, nil
	}
	var zero T
	return zero, false, err
}

func agenticOptionalDisplayAttribute[T any](ctx dex.Context, attribute dex.Attribute[T]) (any, error) {
	value, found, err := agenticOptionalAttribute(ctx, attribute)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	return value, nil
}

var (
	_ dex.Flow                             = (*AgenticCustomerRefundFlow)(nil)
	_ dex.RPC[dex.None, map[string]any]    = (*AgenticCustomerRefundFlow)(nil).GetDexSummary
	_ dex.RPC[dex.None, map[string]any]    = (*AgenticCustomerRefundFlow)(nil).GetDexDisplay
	_ dex.RPC[dex.None, dex.None]          = (*AgenticCustomerRefundFlow)(nil).ApproveRefund
	_ dex.RPC[RejectRefundInput, dex.None] = (*AgenticCustomerRefundFlow)(nil).RejectRefund
)
