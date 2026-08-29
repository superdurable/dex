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

// Deep-verify every examples/go product and design-pattern flow against a live
// Dex + samples worker (WaitForFlow / RPC / channels / attributes / side effects).
package main

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/superdurable/dex/blob-cache-go/blobcache"
	"github.com/superdurable/dex/examples/go/patterns/cron"
	drainexternal "github.com/superdurable/dex/examples/go/patterns/drain-channels/external-publishing"
	"github.com/superdurable/dex/examples/go/patterns/entity-store"
	"github.com/superdurable/dex/examples/go/patterns/interruptible"
	"github.com/superdurable/dex/examples/go/patterns/intervention"
	parallelsubflows "github.com/superdurable/dex/examples/go/patterns/parallel-subflows"
	"github.com/superdurable/dex/examples/go/patterns/recovery"
	"github.com/superdurable/dex/examples/go/patterns/wait-for-state-completion"
	"github.com/superdurable/dex/examples/go/products/engagement"
	"github.com/superdurable/dex/examples/go/products/job-post"
	"github.com/superdurable/dex/examples/go/products/microservices"
	"github.com/superdurable/dex/examples/go/products/money-transfer"
	"github.com/superdurable/dex/examples/go/products/polling"
	"github.com/superdurable/dex/examples/go/products/shortlist-candidates"
	"github.com/superdurable/dex/examples/go/products/signup"
	"github.com/superdurable/dex/examples/go/products/subscription"
	"github.com/superdurable/dex/examples/go/registry"
	"github.com/superdurable/dex/examples/go/shared/service"
	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/sdk-go/dex/ptr"
)

type result struct {
	name    string
	ok      bool
	details string
	err     error
}

func main() {
	ctx := context.Background()
	client, cleanup, err := newVerifyClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "bootstrap: %v\n", err)
		os.Exit(2)
	}
	defer cleanup()

	if _, err := client.HealthCheck(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "dex health: %v\n", err)
		os.Exit(2)
	}

	stamp := strconv.FormatInt(time.Now().UnixNano(), 10)
	only := parseOnly(os.Getenv("DEEPVERIFY_ONLY"))
	results := make([]result, 0, 40)
	var mu sync.Mutex
	record := func(r result) {
		mu.Lock()
		defer mu.Unlock()
		results = append(results, r)
		status := "PASS"
		if !r.ok {
			status = "FAIL"
		}
		line := fmt.Sprintf("[%s] %s — %s", status, r.name, r.details)
		if r.err != nil {
			line += " | err=" + r.err.Error()
		}
		fmt.Println(line)
	}
	want := func(name string) bool {
		if len(only) == 0 {
			return true
		}
		return only[name]
	}

	// Long-running flows first so their timers overlap with shorter scenarios.
	var shortlistID, resetID string
	if want("shortlist/email-path-start") || want("product/shortlist-email") {
		id, startErr := startShortlistEmailPath(ctx, client, stamp)
		if startErr != nil {
			record(result{name: "shortlist/email-path-start", ok: false, err: startErr})
		} else {
			shortlistID = id
			record(result{
				name:    "shortlist/email-path-start",
				ok:      true,
				details: "flowID=" + shortlistID + " (waiting 5m for email)",
			})
		}
	}
	if want("resettabletimer/start+reset") || want("pattern/resettabletimer-fire") {
		id, startErr := startResettableTimerPath(ctx, client, stamp)
		if startErr != nil {
			record(result{name: "resettabletimer/start+reset", ok: false, err: startErr})
		} else {
			resetID = id
			record(result{
				name:    "resettabletimer/start+reset",
				ok:      true,
				details: "flowID=" + resetID + " (waiting ~5m after reset)",
			})
		}
	}

	runProductScenarios(ctx, client, stamp, record, want)
	runPatternScenarios(ctx, client, stamp, record, want)

	if shortlistID != "" && want("product/shortlist-email") {
		record(waitShortlistEmail(ctx, client, shortlistID))
	}
	if resetID != "" && want("pattern/resettabletimer-fire") {
		record(waitResettableTimer(ctx, client, resetID))
	}

	failCount := 0
	fmt.Println()
	fmt.Println("=== SUMMARY ===")
	for _, item := range results {
		if item.ok {
			continue
		}
		failCount++
		fmt.Printf("FAIL %s: %s (%v)\n", item.name, item.details, item.err)
	}
	fmt.Printf("TOTAL=%d FAIL=%d\n", len(results), failCount)
	if failCount > 0 {
		os.Exit(1)
	}
}

func newVerifyClient() (*dex.Client, func(), error) {
	var client *dex.Client
	flows := registry.New(service.NewMyService(), func() *dex.Client { return client })
	registry, err := dex.NewRegistry(flows)
	if err != nil {
		return nil, nil, err
	}
	cacheDir, err := os.MkdirTemp("", "dex-go-deepverify-")
	if err != nil {
		return nil, nil, err
	}
	cache, err := blobcache.New(&blobcache.Config{
		Dir:      cacheDir,
		MaxBytes: 64 << 20,
	})
	if err != nil {
		_ = os.RemoveAll(cacheDir)
		return nil, nil, err
	}
	flowAddr := envOr("DEX_FLOW_SERVICE_ADDRESS", "127.0.0.1:8801")
	workerAddr := envOr("DEX_WORKER_TARGET", "127.0.0.1:8803")
	client, err = dex.NewClient(registry, cache, dex.ClientOptions{
		FlowServiceAddress: flowAddr,
		WorkerTarget:       &dex.WorkerTarget{Address: workerAddr},
	})
	if err != nil {
		_ = cache.Close()
		_ = os.RemoveAll(cacheDir)
		return nil, nil, err
	}
	cleanup := func() {
		_ = client.Close()
		_ = cache.Close()
		_ = os.RemoveAll(cacheDir)
	}
	return client, cleanup, nil
}

func runProductScenarios(
	ctx context.Context,
	client *dex.Client,
	stamp string,
	record func(result),
	want func(string) bool,
) {
	type caseFn struct {
		name string
		run  func() result
	}
	cases := []caseFn{
		{"product/moneytransfer", func() result { return verifyMoneyTransfer(ctx, client, stamp) }},
		{"product/microservices", func() result { return verifyMicroservices(ctx, client, stamp) }},
		{"product/engagement", func() result { return verifyEngagement(ctx, client, stamp) }},
		{"product/subscription", func() result { return verifySubscription(ctx, client, stamp) }},
		{"product/polling", func() result { return verifyPolling(ctx, client, stamp) }},
		{"product/signup", func() result { return verifySignup(ctx, client, stamp) }},
		{"product/jobpost", func() result { return verifyJobPost(ctx, client, stamp) }},
		{"product/shortlist-revoke", func() result { return verifyShortlistRevoke(ctx, client, stamp) }},
	}
	for _, item := range cases {
		if want(item.name) {
			record(item.run())
		}
	}
}

func runPatternScenarios(
	ctx context.Context,
	client *dex.Client,
	stamp string,
	record func(result),
	want func(string) bool,
) {
	type caseFn struct {
		name string
		run  func() result
	}
	cases := []caseFn{
		{"pattern/timer-polling", func() result { return verifyTimerPolling(ctx, client, stamp) }},
		{"pattern/backoff-polling", func() result { return verifyBackoffPolling(ctx, client, stamp) }},
		{"pattern/interruptible", func() result { return verifyInterruptible(ctx, client, stamp) }},
		{"pattern/reminder", func() result { return verifyReminder(ctx, client, stamp) }},
		{"pattern/entity-store", func() result { return verifyEntityStore(ctx, client, stamp) }},
		{"pattern/manual-recovery-retry", func() result { return verifyManualRecoveryRetry(ctx, client, stamp) }},
		{"pattern/manual-recovery-skip", func() result { return verifyManualRecoverySkip(ctx, client, stamp) }},
		{"pattern/parallel-static", func() result { return verifyParallelStatic(ctx, client, stamp) }},
		{"pattern/parallel-dynamic", func() result { return verifyParallelDynamic(ctx, client, stamp) }},
		{"pattern/parallel-await", func() result { return verifyParallelAwait(ctx, client, stamp) }},
		{"pattern/parallel-first-win", func() result { return verifyParallelFirstWin(ctx, client, stamp) }},
		{"pattern/recovery", func() result { return verifyRecovery(ctx, client, stamp) }},
		{"pattern/parallel-subflows-basic", func() result { return verifyParallelSubFlowsBasic(ctx, client, stamp) }},
		{"pattern/parallel-subflows-long-lived", func() result { return verifyParallelSubFlowsLongLived(ctx, client, stamp) }},
		{"pattern/parallel-subflows-short-lived", func() result { return verifyParallelSubFlowsShortLived(ctx, client, stamp) }},
		{"pattern/drain-internal", func() result { return verifyDrainInternal(ctx, client, stamp) }},
		{"pattern/drain-external", func() result { return verifyDrainingChannel(ctx, client, stamp) }},
		{"pattern/wait-for-state-completion", func() result { return verifyWaitForStateCompletion(ctx, client, stamp) }},
		{"pattern/timeout-success", func() result { return verifyTimeoutSuccess(ctx, client, stamp) }},
		{"pattern/timeout-fail", func() result { return verifyTimeoutFail(ctx, client, stamp) }},
		{"pattern/cron-schedule", func() result { return verifyCronSchedule(ctx, client) }},
	}
	for _, item := range cases {
		if want(item.name) {
			record(item.run())
		}
	}
}

func parseOnly(raw string) map[string]bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	out := map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out[part] = true
		}
	}
	return out
}

func verifyMoneyTransfer(ctx context.Context, client *dex.Client, stamp string) result {
	name := "product/moneytransfer"
	flowID := "dv-money-" + stamp
	_, err := client.StartFlow(ctx, registry.MoneyTransfer, flowID, moneytransfer.TransferRequest{
		FromAccount: "from-dv",
		ToAccount:   "to-dv",
		Amount:      17,
		Notes:       "deepverify",
	}, dex.StartFlowOptions{})
	if err != nil {
		return fail(name, "", err)
	}
	wait, err := waitCompleted(ctx, client, flowID, 45*time.Second)
	if err != nil {
		return fail(name, "", err)
	}
	output, err := decodeStringCompletion(wait)
	if err != nil {
		return fail(name, "", err)
	}
	if !strings.Contains(output, "transfer is done") ||
		!strings.Contains(output, "from-dv") ||
		!strings.Contains(output, "to-dv") {
		return fail(name, "unexpected output="+output, nil)
	}
	return pass(name, "completed output="+output)
}

func verifyMicroservices(ctx context.Context, client *dex.Client, stamp string) result {
	name := "product/microservices"
	flowID := "dv-ms-" + stamp
	_, err := client.StartFlow(
		ctx, registry.Microservices, flowID, "initial-data", dex.StartFlowOptions{},
	)
	if err != nil {
		return fail(name, "", err)
	}
	if err := waitForAttributeEqual(
		ctx, client, flowID, microservices.Data, "initial-data", 20*time.Second,
	); err != nil {
		return fail(name, "wait attribute", err)
	}
	var oldData string
	if err := client.InvokeRPC(
		ctx, flowID, registry.Microservices.Swap, "updated-data", &oldData, dex.InvokeOptions{},
	); err != nil {
		return fail(name, "swap", err)
	}
	if oldData != "initial-data" {
		return fail(name, "swap returned "+oldData, nil)
	}
	if err := client.PublishToChannel(ctx, flowID, microservices.Ready, nil); err != nil {
		return fail(name, "publish ready", err)
	}
	wait, err := waitCompleted(ctx, client, flowID, 45*time.Second)
	if err != nil {
		return fail(name, "", err)
	}
	output, err := decodeStringCompletion(wait)
	if err != nil {
		return fail(name, "", err)
	}
	if output != "updated-data" {
		return fail(name, "output="+output, nil)
	}
	return pass(name, "swap+ready completed with updated-data")
}

func verifyEngagement(ctx context.Context, client *dex.Client, stamp string) result {
	name := "product/engagement"
	flowID := "dv-eng-" + stamp
	_, err := client.StartFlow(ctx, registry.Engagement, flowID, engagement.EngagementInput{
		EmployerID:  "employer-dv",
		JobSeekerID: "job-seeker-dv",
		Notes:       "deepverify",
	}, dex.StartFlowOptions{})
	if err != nil {
		return fail(name, "", err)
	}
	if err := waitForAttributeEqual(
		ctx, client, flowID, engagement.EngagementStatus, engagement.StatusInitiated,
		20*time.Second,
	); err != nil {
		return fail(name, "wait initiated", err)
	}
	var description engagement.EngagementDescription
	if err := client.InvokeRPC(
		ctx, flowID, registry.Engagement.Describe, nil, &description, dex.InvokeOptions{},
	); err != nil {
		return fail(name, "describe", err)
	}
	if description.CurrentStatus != engagement.StatusInitiated {
		return fail(name, "status="+string(description.CurrentStatus), nil)
	}
	if err := client.PublishToChannel(ctx, flowID, engagement.OptOutReminder, nil); err != nil {
		return fail(name, "opt-out reminder", err)
	}
	var status engagement.Status
	if err := client.InvokeRPC(
		ctx, flowID, registry.Engagement.Accept, "accepted deepverify", &status, dex.InvokeOptions{},
	); err != nil {
		return fail(name, "accept", err)
	}
	if status != engagement.StatusAccepted {
		return fail(name, "accept status="+string(status), nil)
	}
	wait, err := waitCompleted(ctx, client, flowID, 45*time.Second)
	if err != nil {
		return fail(name, "", err)
	}
	output, err := decodeStringCompletion(wait)
	if err != nil {
		return fail(name, "", err)
	}
	if output != "done" {
		return fail(name, "output="+output, nil)
	}
	return pass(name, "describe+optOut+accept completed")
}

func verifySubscription(ctx context.Context, client *dex.Client, stamp string) result {
	name := "product/subscription"
	flowID := "dv-sub-" + stamp
	customer := subscription.Customer{
		FirstName: "Deep",
		LastName:  "Verify",
		ID:        flowID,
		Email:     "deepverify@example.com",
		Subscription: subscription.Subscription{
			TrialPeriod:         30 * time.Second,
			BillingPeriod:       30 * time.Second,
			MaxBillingPeriods:   2,
			BillingPeriodCharge: 100,
		},
	}
	_, err := client.StartFlow(
		ctx, registry.Subscription, flowID, customer, dex.StartFlowOptions{},
	)
	if err != nil {
		return fail(name, "", err)
	}
	if err := waitForAttributeEqual(
		ctx, client, flowID, subscription.BillingPeriodNumber, 0, 20*time.Second,
	); err != nil {
		return fail(name, "wait initialized", err)
	}
	var current subscription.Subscription
	if err := client.InvokeRPC(
		ctx, flowID, registry.Subscription.Describe, nil, &current, dex.InvokeOptions{},
	); err != nil {
		return fail(name, "describe", err)
	}
	if current.BillingPeriodCharge != 100 {
		return fail(name, fmt.Sprintf("charge=%d", current.BillingPeriodCharge), nil)
	}
	if err := client.PublishToChannel(ctx, flowID, subscription.UpdateChargeAmount, 250); err != nil {
		return fail(name, "update charge", err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		err = client.InvokeRPC(
			ctx, flowID, registry.Subscription.Describe, nil, &current, dex.InvokeOptions{},
		)
		if err == nil && current.BillingPeriodCharge == 250 {
			break
		}
		if time.Now().After(deadline) {
			return fail(name, "charge not updated", err)
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err := client.PublishToChannel(ctx, flowID, subscription.CancelSubscription, nil); err != nil {
		return fail(name, "cancel", err)
	}
	wait, err := waitCompleted(ctx, client, flowID, 45*time.Second)
	if err != nil {
		return fail(name, "", err)
	}
	output, err := decodeStringCompletion(wait)
	if err != nil {
		return fail(name, "", err)
	}
	if output != "subscription canceled" {
		return fail(name, "output="+output, nil)
	}
	return pass(name, "describe+updateCharge+cancel completed")
}

func verifyPolling(ctx context.Context, client *dex.Client, stamp string) result {
	name := "product/polling"
	flowID := "dv-poll-" + stamp
	_, err := client.StartFlow(ctx, registry.Polling, flowID, 1, dex.StartFlowOptions{})
	if err != nil {
		return fail(name, "", err)
	}
	if err := client.PublishToChannel(ctx, flowID, polling.TaskACompleted, nil); err != nil {
		return fail(name, "task-a", err)
	}
	if err := client.PublishToChannel(ctx, flowID, polling.TaskBCompleted, nil); err != nil {
		return fail(name, "task-b", err)
	}
	wait, err := waitCompleted(ctx, client, flowID, 45*time.Second)
	if err != nil {
		return fail(name, "", err)
	}
	output, err := decodeStringCompletion(wait)
	if err != nil {
		return fail(name, "", err)
	}
	if output != "all tasks completed" {
		return fail(name, "output="+output, nil)
	}
	var pollCount int
	found, err := client.GetAttribute(ctx, flowID, polling.CurrentPolls, &pollCount)
	if err != nil || !found || pollCount != 1 {
		return fail(name, fmt.Sprintf("polls found=%v count=%d", found, pollCount), err)
	}
	return pass(name, "channels completed; CurrentPolls=1")
}

func verifySignup(ctx context.Context, client *dex.Client, stamp string) result {
	name := "product/signup"
	flowID := "dv-user-" + stamp
	form := signup.SignupForm{
		Username:  flowID,
		Email:     flowID + "@example.com",
		FirstName: "Deep",
		LastName:  "Verify",
	}
	_, err := client.StartFlow(ctx, registry.Signup, flowID, form, dex.StartFlowOptions{})
	if err != nil {
		return fail(name, "", err)
	}
	var verifyOutput string
	if err := client.InvokeRPC(
		ctx, flowID, registry.Signup.Verify, nil, &verifyOutput, dex.InvokeOptions{},
	); err != nil {
		return fail(name, "verify rpc", err)
	}
	if verifyOutput != "done" {
		return fail(name, "verify="+verifyOutput, nil)
	}
	wait, err := waitCompleted(ctx, client, flowID, 45*time.Second)
	if err != nil {
		return fail(name, "", err)
	}
	_ = wait
	var status string
	found, err := client.GetAttribute(ctx, flowID, signup.Status, &status)
	if err != nil || !found || status != "verified" {
		return fail(name, fmt.Sprintf("status found=%v value=%s", found, status), err)
	}
	return pass(name, "verify RPC + Status=verified + completed")
}

func verifyJobPost(ctx context.Context, client *dex.Client, stamp string) result {
	name := "product/jobpost"
	flowID := "dv-job-" + stamp
	timeout := 24 * time.Hour
	titleAttr, err := dex.InitialAttribute(jobpost.Title, "DeepVerify Engineer")
	if err != nil {
		return fail(name, "initial title", err)
	}
	descriptionAttr, err := dex.InitialAttribute(jobpost.JobDescription, "Build Dex examples")
	if err != nil {
		return fail(name, "initial description", err)
	}
	lastUpdateAttr, err := dex.InitialAttribute(jobpost.LastUpdateTimeMillis, time.Now().UnixMilli())
	if err != nil {
		return fail(name, "initial lastUpdate", err)
	}
	_, err = client.StartFlow(ctx, registry.JobPost, flowID, nil, dex.StartFlowOptions{
		Timeout: &timeout,
		Attributes: []dex.InitialAttributeDef{
			titleAttr, descriptionAttr, lastUpdateAttr,
		},
		ConfigOverride: &dex.FlowConfig{ContinueAsNewThreshold: ptr.Any(int32(10))},
	})
	if err != nil {
		return fail(name, "start", err)
	}
	var info jobpost.JobInfo
	if err := client.InvokeRPC(
		ctx, flowID, registry.JobPost.Get, nil, &info, dex.InvokeOptions{},
	); err != nil {
		return fail(name, "get", err)
	}
	if info.Title != "DeepVerify Engineer" || info.Description != "Build Dex examples" {
		return fail(name, fmt.Sprintf("get=%+v", info), nil)
	}
	var none dex.None
	if err := client.InvokeRPC(
		ctx, flowID, registry.JobPost.Update,
		jobpost.JobInfo{Title: "Senior DeepVerify", Description: "More depth", Notes: "n1"},
		&none, dex.InvokeOptions{},
	); err != nil {
		return fail(name, "update", err)
	}
	if err := client.InvokeRPC(
		ctx, flowID, registry.JobPost.Get, nil, &info, dex.InvokeOptions{},
	); err != nil {
		return fail(name, "get after update", err)
	}
	if info.Title != "Senior DeepVerify" || info.Notes != "n1" {
		return fail(name, fmt.Sprintf("after update=%+v", info), nil)
	}
	deadline := time.Now().Add(30 * time.Second)
	foundInSearch := false
	for time.Now().Before(deadline) {
		page, searchErr := client.SearchFlows(
			ctx, "Title = 'Senior DeepVerify'", 50, "",
		)
		if searchErr == nil {
			for _, entry := range page.Flows {
				if entry.FlowID == flowID {
					foundInSearch = true
					break
				}
			}
		}
		if foundInSearch {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !foundInSearch {
		return fail(name, "not found in SearchFlows Title='Senior DeepVerify'", nil)
	}
	if err := client.StopFlow(ctx, flowID, dex.StopOptions{}); err != nil {
		return fail(name, "stop", err)
	}
	return pass(name, "create+get+update+search+stop")
}

func verifyShortlistRevoke(ctx context.Context, client *dex.Client, stamp string) result {
	name := "product/shortlist-revoke"
	employerID := "emp-revoke-" + stamp
	candidateID := "cand-revoke-" + stamp
	optInID := shortlistcandidates.EmployerOptInFlowID(employerID)
	_, err := client.StartFlow(
		ctx, registry.EmployerOptIn, optInID,
		shortlistcandidates.EmployerOptInInput{EmployerID: employerID},
		dex.StartFlowOptions{},
	)
	if err != nil {
		return fail(name, "opt-in", err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		optedIn, checkErr := shortlistcandidates.IsOptedIn(
			ctx, client, registry.EmployerOptIn, employerID,
		)
		if checkErr == nil && optedIn {
			break
		}
		if time.Now().After(deadline) {
			return fail(name, "isOptedIn", checkErr)
		}
		time.Sleep(200 * time.Millisecond)
	}
	shortlistID := shortlistcandidates.ShortlistFlowID(employerID, candidateID)
	_, err = client.StartFlow(
		ctx, registry.Shortlist, shortlistID,
		shortlistcandidates.ShortlistInput{EmployerID: employerID, CandidateID: candidateID},
		dex.StartFlowOptions{},
	)
	if err != nil {
		return fail(name, "shortlist start", err)
	}
	if err := client.PublishToChannel(
		ctx, shortlistID, shortlistcandidates.RevokeShortlist, nil,
	); err != nil {
		return fail(name, "revoke", err)
	}
	wait, err := waitCompleted(ctx, client, shortlistID, 45*time.Second)
	if err != nil {
		return fail(name, "", err)
	}
	_ = wait
	var emailTS int64
	found, err := client.GetAttribute(
		ctx, shortlistID, shortlistcandidates.ShortlistEmailSentTimestamp, &emailTS,
	)
	if err != nil {
		return fail(name, "email timestamp", err)
	}
	if found && emailTS != 0 {
		return fail(name, fmt.Sprintf("email should not send; ts=%d", emailTS), nil)
	}
	return pass(name, "opt-in + shortlist + revoke completed without email")
}

func startShortlistEmailPath(
	ctx context.Context,
	client *dex.Client,
	stamp string,
) (string, error) {
	employerID := "emp-email-" + stamp
	candidateID := "cand-email-" + stamp
	optInID := shortlistcandidates.EmployerOptInFlowID(employerID)
	_, err := client.StartFlow(
		ctx, registry.EmployerOptIn, optInID,
		shortlistcandidates.EmployerOptInInput{EmployerID: employerID},
		dex.StartFlowOptions{},
	)
	if err != nil {
		return "", err
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		optedIn, checkErr := shortlistcandidates.IsOptedIn(
			ctx, client, registry.EmployerOptIn, employerID,
		)
		if checkErr == nil && optedIn {
			break
		}
		if time.Now().After(deadline) {
			if checkErr != nil {
				return "", checkErr
			}
			return "", fmt.Errorf("employer not opted in")
		}
		time.Sleep(200 * time.Millisecond)
	}
	shortlistID := shortlistcandidates.ShortlistFlowID(employerID, candidateID)
	_, err = client.StartFlow(
		ctx, registry.Shortlist, shortlistID,
		shortlistcandidates.ShortlistInput{EmployerID: employerID, CandidateID: candidateID},
		dex.StartFlowOptions{},
	)
	return shortlistID, err
}

func waitShortlistEmail(ctx context.Context, client *dex.Client, flowID string) result {
	name := "product/shortlist-email"
	wait, err := waitCompleted(ctx, client, flowID, 6*time.Minute)
	if err != nil {
		return fail(name, "", err)
	}
	_ = wait
	var emailTS int64
	found, err := client.GetAttribute(
		ctx, flowID, shortlistcandidates.ShortlistEmailSentTimestamp, &emailTS,
	)
	if err != nil || !found || emailTS == 0 {
		return fail(name, fmt.Sprintf("email ts found=%v ts=%d", found, emailTS), err)
	}
	return pass(name, fmt.Sprintf("email sent ts=%d after 5m timer", emailTS))
}

func startResettableTimerPath(
	ctx context.Context,
	client *dex.Client,
	stamp string,
) (string, error) {
	flowID := "dv-reset-" + stamp
	_, err := client.StartFlow(
		ctx, registry.ResettableTimer, flowID, nil, hourStartOptions(),
	)
	if err != nil {
		return "", err
	}
	time.Sleep(2 * time.Second)
	var none dex.None
	if err := client.InvokeRPC(
		ctx, flowID, registry.ResettableTimer.SendResetMessage, nil, &none, dex.InvokeOptions{},
	); err != nil {
		return "", err
	}
	return flowID, nil
}

func waitResettableTimer(ctx context.Context, client *dex.Client, flowID string) result {
	name := "pattern/resettabletimer-fire"
	wait, err := waitCompleted(ctx, client, flowID, 6*time.Minute)
	if err != nil {
		return fail(name, "", err)
	}
	_ = wait
	return pass(name, "completed after reset + 5m timer fire")
}

func verifyTimerPolling(ctx context.Context, client *dex.Client, stamp string) result {
	name := "pattern/simple-polling"
	flowID := "dv-simple-poll-" + stamp
	_, err := client.StartFlow(
		ctx, registry.PollingWithTimer, flowID, nil, hourStartOptions(),
	)
	if err != nil {
		return fail(name, "", err)
	}
	if _, err := waitCompleted(ctx, client, flowID, 45*time.Second); err != nil {
		return fail(name, "", err)
	}
	return pass(name, "completed after 10s poll timer")
}

func verifyBackoffPolling(ctx context.Context, client *dex.Client, stamp string) result {
	name := "pattern/backoff-polling"
	flowID := "dv-backoff-poll-" + stamp
	_, err := client.StartFlow(
		ctx, registry.BackoffPolling, flowID, nil, hourStartOptions(),
	)
	if err != nil {
		return fail(name, "", err)
	}
	wait, err := waitCompleted(ctx, client, flowID, 90*time.Second)
	if err != nil {
		return fail(name, "", err)
	}
	output, err := decodeStringCompletion(wait)
	if err != nil {
		return fail(name, "", err)
	}
	if output != "External data result" {
		return fail(name, "output="+output, nil)
	}
	return pass(name, "retries then completed with External data result")
}

func verifyInterruptible(ctx context.Context, client *dex.Client, stamp string) result {
	name := "pattern/interruptible"
	flowID := "dv-interrupt-" + stamp
	_, err := client.StartFlow(
		ctx, registry.Interruptible, flowID, nil, hourStartOptions(),
	)
	if err != nil {
		return fail(name, "", err)
	}
	time.Sleep(500 * time.Millisecond)
	var none dex.None
	if err := client.InvokeRPC(
		ctx, flowID, registry.Interruptible.Interrupt, nil, &none, dex.InvokeOptions{},
	); err != nil {
		return fail(name, "interrupt rpc", err)
	}
	if _, err := waitCompleted(ctx, client, flowID, 60*time.Second); err != nil {
		return fail(name, "", err)
	}
	var signal string
	found, err := client.GetAttribute(ctx, flowID, interruptible.InterruptSignal, &signal)
	if err != nil {
		return fail(name, "get interrupt signal", err)
	}
	if !found || signal != "cancel" {
		return fail(name, fmt.Sprintf("signal found=%v value=%s", found, signal), nil)
	}
	return pass(name, "Interrupt RPC set cancel; flow completed")
}

func verifyReminder(ctx context.Context, client *dex.Client, stamp string) result {
	name := "pattern/reminder"
	flowID := "dv-reminder-" + stamp
	_, err := client.StartFlow(ctx, registry.Reminder, flowID, nil, hourStartOptions())
	if err != nil {
		return fail(name, "", err)
	}
	time.Sleep(6 * time.Second) // allow at least one reminder timer tick
	var none dex.None
	if err := client.InvokeRPC(
		ctx, flowID, registry.Reminder.Accept, nil, &none, dex.InvokeOptions{},
	); err != nil {
		return fail(name, "accept", err)
	}
	if _, err := waitCompleted(ctx, client, flowID, 45*time.Second); err != nil {
		return fail(name, "", err)
	}
	return pass(name, "reminder tick + Accept completed")
}

func verifyEntityStore(ctx context.Context, client *dex.Client, stamp string) result {
	name := "pattern/entity-store"
	flowID := "dv-user-" + stamp
	profile := entitystore.UserProfile{
		DisplayName:    "Ada Lovelace",
		Email:          "ada-" + stamp + "@example.com",
		MarketingOptIn: true,
		Credits:        120,
		Weight:         59.5,
		LastLoggedIn:   time.Date(2026, 8, 11, 15, 30, 0, 0, time.UTC),
		Metadata: entitystore.UserProfileMetadata{
			Source: "deepverify",
			Tags:   []string{"example", "pro"},
		},
	}
	attributes, err := entitystore.InitialAttributes(profile)
	if err != nil {
		return fail(name, "initial attributes", err)
	}
	options := hourStartOptions()
	options.Attributes = attributes
	options.ConfigOverride = &dex.FlowConfig{
		AttributeStoreNames: []string{entitystore.StoreName},
	}
	if _, err := client.StartFlow(ctx, registry.UserProfile, flowID, nil, options); err != nil {
		return fail(name, "start", err)
	}
	var none dex.None
	if err := client.InvokeRPC(
		ctx, flowID, registry.UserProfile.UpdateProfile,
		entitystore.UserProfile{
			DisplayName:    "Ada Byron",
			Email:          profile.Email,
			MarketingOptIn: false,
			Credits:        180,
			Weight:         60.25,
			LastLoggedIn:   time.Date(2026, 8, 12, 9, 45, 0, 0, time.UTC),
			Metadata: entitystore.UserProfileMetadata{
				Source: "deepverify",
				Tags:   []string{"example", "enterprise"},
			},
		},
		&none, dex.InvokeOptions{},
	); err != nil {
		return fail(name, "update", err)
	}
	var got entitystore.UserProfile
	if err := client.InvokeRPC(
		ctx, flowID, registry.UserProfile.GetProfile, nil, &got, dex.InvokeOptions{},
	); err != nil {
		return fail(name, "get", err)
	}
	if got.DisplayName != "Ada Byron" || got.Email != profile.Email || got.MarketingOptIn ||
		got.Credits != 180 || got.Weight != 60.25 ||
		!got.LastLoggedIn.Equal(time.Date(2026, 8, 12, 9, 45, 0, 0, time.UTC)) ||
		!reflect.DeepEqual(got.Metadata, entitystore.UserProfileMetadata{
			Source: "deepverify",
			Tags:   []string{"example", "enterprise"},
		}) {
		return fail(name, fmt.Sprintf("unexpected profile: %+v", got), nil)
	}
	if err := client.InvokeRPC(
		ctx, flowID, registry.UserProfile.ClearProfile, nil, &none, dex.InvokeOptions{},
	); err != nil {
		return fail(name, "clear", err)
	}
	return pass(name, "create+update+get+clear on "+flowID)
}

func verifyManualRecoveryRetry(ctx context.Context, client *dex.Client, stamp string) result {
	name := "pattern/manual-recovery-retry"
	flowID := "dv-manual-recovery-retry-" + stamp
	_, err := client.StartFlow(
		ctx, registry.ManualRecovery, flowID, true, hourStartOptions(),
	)
	if err != nil {
		return fail(name, "start", err)
	}
	if err := client.PublishToChannel(
		ctx, flowID, intervention.RetryChannel, nil,
	); err != nil {
		return fail(name, "publish retry", err)
	}
	wait, err := waitCompleted(ctx, client, flowID, 60*time.Second)
	if err != nil {
		return fail(name, "", err)
	}
	output, err := decodeStringCompletion(wait)
	if err != nil {
		return fail(name, "", err)
	}
	if output != "work completed" {
		return fail(name, "output="+output, nil)
	}
	return pass(name, output)
}

func verifyManualRecoverySkip(ctx context.Context, client *dex.Client, stamp string) result {
	name := "pattern/manual-recovery-skip"
	flowID := "dv-manual-recovery-skip-" + stamp
	_, err := client.StartFlow(
		ctx, registry.ManualRecovery, flowID, true, hourStartOptions(),
	)
	if err != nil {
		return fail(name, "start", err)
	}
	if err := client.PublishToChannel(
		ctx, flowID, intervention.SkipChannel, nil,
	); err != nil {
		return fail(name, "publish skip", err)
	}
	wait, err := waitForFlow(ctx, client, flowID, false, 60*time.Second)
	if err != nil {
		return fail(name, "", err)
	}
	if wait.Status != dex.FlowFailed {
		return fail(name, fmt.Sprintf("expected FlowFailed got %v", wait.Status), nil)
	}
	return pass(name, wait.ErrorMessage)
}

func verifyParallelStatic(ctx context.Context, client *dex.Client, stamp string) result {
	name := "pattern/parallel-static"
	flowID := "dv-par-static-" + stamp
	_, err := client.StartFlow(
		ctx, registry.StaticParallel, flowID, "work", hourStartOptions(),
	)
	if err != nil {
		return fail(name, "", err)
	}
	if _, err := waitCompleted(ctx, client, flowID, 45*time.Second); err != nil {
		return fail(name, "", err)
	}
	return pass(name, "completed")
}

func verifyParallelDynamic(ctx context.Context, client *dex.Client, stamp string) result {
	name := "pattern/parallel-dynamic"
	flowID := "dv-par-dynamic-" + stamp
	_, err := client.StartFlow(
		ctx, registry.DynamicParallel, flowID, []string{"one", "two", "three"}, hourStartOptions(),
	)
	if err != nil {
		return fail(name, "", err)
	}
	if _, err := waitCompleted(ctx, client, flowID, 45*time.Second); err != nil {
		return fail(name, "", err)
	}
	return pass(name, "completed")
}

func verifyParallelAwait(ctx context.Context, client *dex.Client, stamp string) result {
	name := "pattern/parallel-await"
	flowID := "dv-par-await-" + stamp
	_, err := client.StartFlow(
		ctx, registry.AwaitParallel, flowID, 3, hourStartOptions(),
	)
	if err != nil {
		return fail(name, "", err)
	}
	if _, err := waitCompleted(ctx, client, flowID, 60*time.Second); err != nil {
		return fail(name, "", err)
	}
	return pass(name, "completed count=3")
}

func verifyParallelFirstWin(ctx context.Context, client *dex.Client, stamp string) result {
	name := "pattern/parallel-first-win"
	flowID := "dv-par-first-win-" + stamp
	_, err := client.StartFlow(
		ctx, registry.FirstWinParallel, flowID, 3, hourStartOptions(),
	)
	if err != nil {
		return fail(name, "", err)
	}
	if _, err := waitCompleted(ctx, client, flowID, 45*time.Second); err != nil {
		return fail(name, "", err)
	}
	return pass(name, "completed")
}

func verifyRecovery(ctx context.Context, client *dex.Client, stamp string) result {
	name := "pattern/recovery"
	flowID := "dv-recovery-" + stamp
	_, err := client.StartFlow(
		ctx, registry.FailureRecovery, flowID,
		recovery.FailureRecoveryWorkflowInput{ItemName: "widget", RequestedQuantity: 0},
		hourStartOptions(),
	)
	if err != nil {
		return fail(name, "", err)
	}
	result, err := waitForFlow(ctx, client, flowID, true, 90*time.Second)
	if err != nil {
		return fail(name, "", err)
	}
	if result.Status != dex.FlowFailed {
		return fail(name, fmt.Sprintf("expected FlowFailed got %v msg=%s", result.Status, result.ErrorMessage), nil)
	}
	if !strings.Contains(result.ErrorMessage, "Failed to process transaction") {
		return fail(name, "msg="+result.ErrorMessage, nil)
	}
	return pass(name, "payment fail → void → ForceFail as designed")
}

func verifyParallelSubFlowsBasic(ctx context.Context, client *dex.Client, stamp string) result {
	name := "pattern/parallel-subflows-basic"
	flowID := "dv-parallel-subflows-basic-" + stamp
	_, err := client.StartFlow(ctx, registry.BasicSubFlows, flowID, []string{"one", "two", "three"}, hourStartOptions())
	if err != nil {
		return fail(name, "start", err)
	}
	if _, err := waitCompleted(ctx, client, flowID, 90*time.Second); err != nil {
		return fail(name, "wait", err)
	}
	return pass(name, "completed after half of the SubFlows and stopped the rest")
}

func verifyParallelSubFlowsLongLived(ctx context.Context, client *dex.Client, stamp string) result {
	name := "pattern/parallel-subflows-long-lived"
	flowID := "dv-parallel-subflows-long-lived-" + stamp
	input := parallelsubflows.ParentInput{Requests: []string{"one", "two"}, Concurrency: 2}
	_, err := client.StartFlow(ctx, registry.LongLiveSubFlows, flowID, input, hourStartOptions())
	if err != nil {
		return fail(name, "start", err)
	}
	var output dex.None
	if err := client.InvokeRPC(ctx, flowID, registry.LongLiveSubFlows.Stop, nil, &output, dex.InvokeOptions{}); err != nil {
		return fail(name, "stop", err)
	}
	if _, err := waitCompleted(ctx, client, flowID, 90*time.Second); err != nil {
		return fail(name, "wait", err)
	}
	return pass(name, "workers completed after the durable stop attribute was set")
}

func verifyParallelSubFlowsShortLived(ctx context.Context, client *dex.Client, stamp string) result {
	name := "pattern/parallel-subflows-short-lived"
	flowID := "dv-parallel-subflows-short-lived-" + stamp
	input := parallelsubflows.ParentInput{Requests: []string{"one", "two", "three"}, Concurrency: 2}
	_, err := client.StartFlow(ctx, registry.ShortLiveSubFlows, flowID, input, hourStartOptions())
	if err != nil {
		return fail(name, "start", err)
	}
	if _, err := waitCompleted(ctx, client, flowID, 90*time.Second); err != nil {
		return fail(name, "wait", err)
	}
	return pass(name, "completed after the request Channel drained and every SubFlow finished")
}

func verifyDrainInternal(ctx context.Context, client *dex.Client, stamp string) result {
	name := "pattern/drain-internal"
	flowID := "dv-drain-int-" + stamp
	_, err := client.StartFlow(
		ctx, registry.DrainInternal, flowID, "doc-"+stamp, hourStartOptions(),
	)
	if err != nil {
		return fail(name, "", err)
	}
	if _, err := waitCompleted(ctx, client, flowID, 90*time.Second); err != nil {
		return fail(name, "", err)
	}
	return pass(name, "internal channel drain + finalize completed")
}

func verifyDrainingChannel(ctx context.Context, client *dex.Client, stamp string) result {
	name := "pattern/drain-external"
	flowID := "dv-drain-sig-" + stamp
	_, err := client.StartFlow(
		ctx, registry.DrainExternal, flowID, "first message", hourStartOptions(),
	)
	if err != nil {
		return fail(name, "", err)
	}
	time.Sleep(1 * time.Second)
	if err := client.PublishToChannel(
		ctx, flowID, drainexternal.QueueChannel, "second message",
	); err != nil {
		return fail(name, "second message", err)
	}
	if _, err := waitCompleted(ctx, client, flowID, 90*time.Second); err != nil {
		return fail(name, "", err)
	}
	return pass(name, "start + extra message drained to ForceComplete")
}

func verifyWaitForStateCompletion(
	ctx context.Context,
	client *dex.Client,
	stamp string,
) result {
	name := "pattern/wait-for-state-completion"
	flowID := "dv-waitstate-" + stamp
	input := waitforstatecompletion.JobSeekerData{ID: 42, Name: "deep", Email: "a@b.c"}
	_, err := client.StartFlow(
		ctx, registry.WaitForStateCompletion, flowID, input, hourStartOptions(),
	)
	if err != nil {
		return fail(name, "start", err)
	}
	if err := waitForStepCompletion(
		ctx, client, flowID,
		dex.StepExecutionID{StepType: "PersistData"}, 45*time.Second,
	); err != nil {
		return fail(name, "WaitForStepCompletion PersistData", err)
	}
	var persisted waitforstatecompletion.JobSeekerData
	if err := client.InvokeRPC(
		ctx, flowID, registry.WaitForStateCompletion.GetJobSeekerData,
		nil, &persisted, dex.InvokeOptions{},
	); err != nil {
		return fail(name, "GetJobSeekerData", err)
	}
	if persisted.ID != 42 || persisted.Name != "deep" {
		return fail(name, fmt.Sprintf("persisted=%+v", persisted), nil)
	}
	if _, err := waitCompleted(ctx, client, flowID, 45*time.Second); err != nil {
		return fail(name, "wait complete", err)
	}
	return pass(name, "PersistData wait + RPC + complete")
}

func verifyTimeoutSuccess(ctx context.Context, client *dex.Client, stamp string) result {
	name := "pattern/timeout-success"
	flowID := "dv-timeout-ok-" + stamp
	_, err := client.StartFlow(
		ctx, registry.GracefulTimeout, flowID, true, hourStartOptions(),
	)
	if err != nil {
		return fail(name, "", err)
	}
	wait, err := waitForFlow(ctx, client, flowID, true, 45*time.Second)
	if err != nil {
		return fail(name, "", err)
	}
	if wait.Status != dex.FlowCompleted {
		return fail(name, fmt.Sprintf("status=%v msg=%s", wait.Status, wait.ErrorMessage), nil)
	}
	output, err := decodeStringCompletion(wait)
	if err != nil {
		return fail(name, "", err)
	}
	if output != "Workflow completed successfully" {
		return fail(name, "output="+output, nil)
	}
	return pass(name, "ForceComplete before 1m timeout")
}

func verifyTimeoutFail(ctx context.Context, client *dex.Client, stamp string) result {
	name := "pattern/timeout-fail"
	flowID := "dv-timeout-fail-" + stamp
	_, err := client.StartFlow(
		ctx, registry.GracefulTimeout, flowID, false, hourStartOptions(),
	)
	if err != nil {
		return fail(name, "", err)
	}
	// 1m ForceFail + worker backlog under reminder load needs headroom.
	result, err := waitForFlow(ctx, client, flowID, true, 3*time.Minute)
	if err != nil {
		return fail(name, "", err)
	}
	if result.Status != dex.FlowFailed {
		return fail(name, fmt.Sprintf("expected failed got %v msg=%s", result.Status, result.ErrorMessage), nil)
	}
	if !strings.Contains(result.ErrorMessage, "did not finish the task in time") {
		return fail(name, "msg="+result.ErrorMessage, nil)
	}
	return pass(name, "1m timeout ForceFail as designed")
}

func verifyCronSchedule(ctx context.Context, client *dex.Client) result {
	name := "pattern/cron-schedule"
	flowID := fmt.Sprintf("cron-schedule-dv-%d", time.Now().UnixNano())
	_, err := client.StartFlow(
		ctx,
		registry.CronSchedule,
		flowID,
		cron.CronScheduleInput{
			Interval: cron.Interval{Value: 1, Unit: cron.Minute},
			RunCount: 2,
		},
		dex.StartFlowOptions{},
	)
	if err != nil {
		return fail(name, "start "+flowID, err)
	}
	if err := client.PublishToChannel(ctx, flowID, cron.Trigger, nil, nil); err != nil {
		return fail(name, "trigger "+flowID, err)
	}
	wait, err := waitForFlow(ctx, client, flowID, true, 30*time.Second)
	if err != nil {
		return fail(name, "WaitForFlow "+flowID, err)
	}
	if wait.Status != dex.FlowCompleted {
		return fail(name, fmt.Sprintf("status=%v msg=%s", wait.Status, wait.ErrorMessage), nil)
	}
	return pass(name, "completed two triggered durable timer runs")
}

func waitCompleted(
	ctx context.Context,
	client *dex.Client,
	flowID string,
	timeout time.Duration,
) (dex.FlowResult, error) {
	wait, err := waitForFlow(ctx, client, flowID, true, timeout)
	if err != nil {
		return wait, err
	}
	if wait.Status != dex.FlowCompleted {
		return wait, fmt.Errorf(
			"expected FlowCompleted got %v msg=%s", wait.Status, wait.ErrorMessage,
		)
	}
	return wait, nil
}

func waitForAttributeEqual(
	ctx context.Context,
	client *dex.Client,
	flowID string,
	attribute dex.AttributeDef,
	value any,
	timeout time.Duration,
) error {
	waitContext, cancelWait := context.WithTimeout(ctx, timeout)
	defer cancelWait()
	return client.WaitForAttributeEqual(waitContext, flowID, attribute, value)
}

func waitForStepCompletion(
	ctx context.Context,
	client *dex.Client,
	flowID string,
	stepExecutionID dex.StepExecutionID,
	timeout time.Duration,
) error {
	waitContext, cancelWait := context.WithTimeout(ctx, timeout)
	defer cancelWait()
	return client.WaitForStepCompletion(waitContext, flowID, stepExecutionID)
}

func waitForFlow(
	ctx context.Context,
	client *dex.Client,
	flowID string,
	needsResults bool,
	timeout time.Duration,
) (dex.FlowResult, error) {
	waitContext, cancelWait := context.WithTimeout(ctx, timeout)
	defer cancelWait()
	return client.WaitForFlow(
		waitContext,
		flowID,
		dex.WaitForFlowOptions{NeedsResults: needsResults},
	)
}

func decodeStringCompletion(wait dex.FlowResult) (string, error) {
	if len(wait.Completions) == 0 {
		return "", fmt.Errorf("no completions")
	}
	var output string
	if err := wait.Completions[0].Output.Decode(&output); err != nil {
		return "", err
	}
	return output, nil
}

func hourStartOptions() dex.StartFlowOptions {
	timeout := time.Hour
	return dex.StartFlowOptions{Timeout: &timeout}
}

func idReuseHourOptions() dex.StartFlowOptions {
	timeout := time.Hour
	return dex.StartFlowOptions{
		Timeout:       &timeout,
		IDReusePolicy: dex.IDReuseAllowIfPreviousFailed,
	}
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func pass(name, details string) result {
	return result{name: name, ok: true, details: details}
}

func fail(name, details string, err error) result {
	return result{name: name, ok: false, details: details, err: err}
}
