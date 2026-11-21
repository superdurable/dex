/*
 * Copyright (c) 2022-2026 Super Durable, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package io.superdurable.dex.products.customerrefund;

import io.superdurable.dex.BlobCache;
import io.superdurable.dex.Client;
import io.superdurable.dex.ClientOptions;
import io.superdurable.dex.Registry;
import io.superdurable.dex.Worker;
import io.superdurable.dex.WorkerOptions;
import io.superdurable.dex.products.customerrefund.CustomerRefundFlow.DecisionReason;
import io.superdurable.dex.products.customerrefund.CustomerRefundFlow.RefundRequest;
import io.superdurable.dex.products.customerrefund.FakeBillingService.RecordedRefund;

import java.nio.file.Path;
import java.time.Duration;
import java.util.List;
import java.util.concurrent.CountDownLatch;

/**
 * The customer refund commands: worker, start FLOW_ID [SCENARIO], decide FLOW_ID approve|reject
 * REASON, and list-refunds.
 */
public final class CustomerRefundCommand {
    private static final String USAGE = String.join("\n",
            "usage: customer-refund worker",
            "       customer-refund start FLOW_ID [" + String.join("|", RefundSettings.SCENARIOS.keySet()) + "]",
            "       customer-refund decide FLOW_ID approve|reject REASON",
            "       customer-refund list-refunds");

    private CustomerRefundCommand() {
    }

    public static void main(final String[] args) throws Exception {
        final String command = args.length == 0 ? "" : args[0];
        switch (command) {
            case "worker" -> runWorker();
            case "start" -> {
                requireArguments(args, 2, 3);
                startRefund(args[1], args.length == 3 ? args[2] : "normal");
            }
            case "decide" -> {
                requireArguments(args, 4, 4);
                decideRefund(args[1], args[2], args[3]);
            }
            case "list-refunds" -> listRefunds();
            default -> exitWithUsage();
        }
    }

    /** Runs the Worker until Ctrl+C. */
    private static void runWorker() throws Exception {
        final Registry registry = new Registry(List.of(newRefundFlow()));
        final BlobCache cache = RefundSettings.openCache(Path.of(".dex-blob-cache/customer-refund-worker"));
        final String workerId = RefundSettings.newWorkerId();
        final WorkerOptions options = RefundSettings.refundWorkerOptions(RefundSettings.SERVER_ADDRESS, workerId);
        final Worker worker = new Worker(registry, cache, options);
        final CountDownLatch stopped = new CountDownLatch(1);
        Runtime.getRuntime().addShutdownHook(new Thread(() -> {
            System.out.println("Stopping the Worker.");
            worker.close();
            cache.close();
            stopped.countDown();
        }));
        final Thread workerThread = new Thread(worker::start, "customer-refund-worker");
        workerThread.setDaemon(true);
        workerThread.start();
        worker.waitPullRegistered(Duration.ofSeconds(30));
        System.out.printf(
                "Worker %s is ready (billing mode %s, pid %d). Press Ctrl+C to stop it.%n",
                workerId,
                RefundSettings.BILLING_MODE,
                ProcessHandle.current().pid());
        stopped.await();
    }

    private static void startRefund(final String flowId, final String scenario) {
        final RefundRequest request = RefundSettings.SCENARIOS.get(scenario);
        if (request == null) {
            exitWithUsage();
        }
        final CustomerRefundFlow flow = newRefundFlow();
        try (BlobCache cache = RefundSettings.openCache(Path.of(".dex-blob-cache/customer-refund-client"));
                Client client = new Client(
                        new Registry(List.of(flow)), cache, new ClientOptions(RefundSettings.SERVER_ADDRESS))) {
            client.startFlow(flow, flowId, request);
            System.out.printf("Started refund %s (%s).%n", flowId, scenario);
        }
    }

    private static void decideRefund(final String flowId, final String decision, final String reason) {
        if (!decision.equals("approve") && !decision.equals("reject")) {
            exitWithUsage();
        }
        final CustomerRefundFlow flow = newRefundFlow();
        try (BlobCache cache = RefundSettings.openCache(Path.of(".dex-blob-cache/customer-refund-client"));
                Client client = new Client(
                        new Registry(List.of(flow)), cache, new ClientOptions(RefundSettings.SERVER_ADDRESS))) {
            final CustomerRefundFlow stub = client.newRpcStub(CustomerRefundFlow.class, flowId);
            if (decision.equals("approve")) {
                client.invokeRPC(stub::approveRefund, new DecisionReason(reason));
                System.out.printf("Refund %s: approved.%n", flowId);
            } else {
                client.invokeRPC(stub::rejectRefund, new DecisionReason(reason));
                System.out.printf("Refund %s: rejected.%n", flowId);
            }
        }
    }

    private static void listRefunds() {
        final List<RecordedRefund> refunds =
                new FakeBillingService(FakeBillingService.DEFAULT_BILLING_LEDGER).listRefunds();
        if (refunds.isEmpty()) {
            System.out.println("No refunds recorded.");
        }
        for (RecordedRefund refund : refunds) {
            System.out.printf("%s  %s  %s USD  key %s%n",
                    refund.refundId(),
                    refund.orderId(),
                    FakeBillingService.formatUsd(refund.amountUsd()),
                    refund.idempotencyKey());
        }
    }

    private static CustomerRefundFlow newRefundFlow() {
        return RefundSettings.newRefundFlow(
                RefundSettings.BILLING_MODE,
                FakeBillingService.DEFAULT_BILLING_LEDGER,
                FakeEmailService.DEFAULT_EMAIL_LEDGER);
    }

    private static void requireArguments(final String[] args, final int least, final int most) {
        if (args.length < least || args.length > most) {
            exitWithUsage();
        }
    }

    private static void exitWithUsage() {
        System.err.println(USAGE);
        System.exit(2);
    }
}
