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

import io.superdurable.dex.products.customerrefund.FakeBillingService.Order;
import io.superdurable.dex.products.customerrefund.FakeBillingService.RecordedRefund;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.ArrayList;
import java.util.HashSet;
import java.util.List;
import java.util.Set;
import java.util.concurrent.Callable;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.Future;
import java.util.concurrent.TimeUnit;
import java.util.function.IntFunction;
import java.util.stream.Collectors;
import java.util.stream.Stream;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

/** Many Workers' billing and email calls racing on the same ledger files. */
class RefundLedgerConcurrencyTest {
    private static final int CALLERS = 8;
    private static final int REFUNDS_PER_PROCESS = 40;

    @TempDir
    Path directory;

    @Test
    void sameKeyRefundsRacingRecordOneRefund() throws Exception {
        final Path ledger = directory.resolve("billing-refunds.json");
        final Order order = new FakeBillingService(ledger).getOrder("INV-1249");

        final List<String> refundIds = runConcurrently(caller ->
                () -> new FakeBillingService(ledger).refund(order, "refund-same", false));

        final List<RecordedRefund> refunds = new FakeBillingService(ledger).listRefunds();
        assertEquals(1, refunds.size());
        assertEquals(Set.of(refunds.get(0).refundId()), new HashSet<>(refundIds));
        assertNoTemporaryFiles();
    }

    @Test
    void differentKeyRefundsRacingGetDistinctIds() throws Exception {
        final Path ledger = directory.resolve("billing-refunds.json");
        final Order order = new FakeBillingService(ledger).getOrder("INV-1249");

        final List<String> refundIds = runConcurrently(caller ->
                () -> new FakeBillingService(ledger).refund(order, "refund-" + caller, false));

        final List<RecordedRefund> refunds = new FakeBillingService(ledger).listRefunds();
        assertEquals(CALLERS, refunds.size());
        assertEquals(CALLERS, new HashSet<>(refundIds).size());
        assertEquals(
                Stream.iterate(1042, id -> id + 1).limit(CALLERS).map(id -> "rf_" + id).collect(Collectors.toSet()),
                refunds.stream().map(RecordedRefund::refundId).collect(Collectors.toSet()));
        assertNoTemporaryFiles();
    }

    @Test
    void sameKeyEmailsRacingSendOnce() throws Exception {
        final Path ledger = directory.resolve("sent-emails.json");

        final List<Boolean> sent = runConcurrently(caller ->
                () -> new FakeEmailService(ledger).send("a@example.com", "hello", "email-same"));

        assertEquals(1, sent.stream().filter(Boolean::booleanValue).count());
        assertEquals(List.of("email-same"), new FakeEmailService(ledger).listSentKeys());
        assertNoTemporaryFiles();
    }

    @Test
    void differentKeyEmailsRacingAllSend() throws Exception {
        final Path ledger = directory.resolve("sent-emails.json");

        final List<Boolean> sent = runConcurrently(caller ->
                () -> new FakeEmailService(ledger).send("a@example.com", "hello", "email-" + caller));

        assertEquals(CALLERS, sent.stream().filter(Boolean::booleanValue).count());
        assertEquals(CALLERS, new HashSet<>(new FakeEmailService(ledger).listSentKeys()).size());
        assertNoTemporaryFiles();
    }

    @Test
    void refundsRacingFromSeparateProcessesKeepEveryRecord() throws Exception {
        final Path ledger = directory.resolve("billing-refunds.json");
        final String java = ProcessHandle.current().info().command().orElse("java");
        final Path outputDirectory = Files.createDirectories(directory.resolve("process-output"));
        final List<Process> processes = new ArrayList<>();
        final List<Path> outputs = new ArrayList<>();
        try {
            for (int caller = 0; caller < CALLERS; caller++) {
                // Even processes share key names; odd ones use their own.
                final String key = caller % 2 == 0 ? "refund-shared" : "refund-" + caller;
                final Path output = outputDirectory.resolve("caller-" + caller + ".log");
                outputs.add(output);
                processes.add(new ProcessBuilder(
                        java, "-cp", System.getProperty("java.class.path"),
                        RefundInSeparateProcess.class.getName(), ledger.toString(), key)
                        .redirectErrorStream(true)
                        .redirectOutput(output.toFile())
                        .start());
            }
            for (int caller = 0; caller < processes.size(); caller++) {
                final Process process = processes.get(caller);
                assertTrue(process.waitFor(60, TimeUnit.SECONDS), "caller " + caller + " did not finish");
                assertEquals(0, process.exitValue(), Files.readString(outputs.get(caller)));
            }
        } finally {
            processes.forEach(Process::destroyForcibly);
        }

        final List<RecordedRefund> refunds = new FakeBillingService(ledger).listRefunds();
        assertEquals(REFUNDS_PER_PROCESS * (1 + CALLERS / 2), refunds.size());
        assertEquals(refunds.size(), refunds.stream().map(RecordedRefund::refundId).distinct().count());
        assertEquals(refunds.size(), refunds.stream().map(RecordedRefund::idempotencyKey).distinct().count());
        assertNoTemporaryFiles();
    }

    @Test
    void refundsThroughADirectoryAliasShareTheLock() throws Exception {
        final Path realDirectory = Files.createDirectories(directory.resolve("ledgers"));
        final Path alias = Files.createSymbolicLink(directory.resolve("ledgers-alias"), realDirectory);
        final Path realLedger = realDirectory.resolve("billing-refunds.json");
        final Path aliasLedger = alias.resolve("billing-refunds.json");
        final Order order = new FakeBillingService(realLedger).getOrder("INV-1249");

        final List<String> refundIds = runConcurrently(caller -> {
            final Path ledger = caller % 2 == 0 ? realLedger : aliasLedger;
            return () -> new FakeBillingService(ledger).refund(order, "refund-" + caller, false);
        });

        assertEquals(CALLERS, new HashSet<>(refundIds).size());
        assertEquals(CALLERS, new FakeBillingService(aliasLedger).listRefunds().size());
    }

    /** Issues numbered refunds from its own JVM: LEDGER_PATH KEY_PREFIX. */
    static final class RefundInSeparateProcess {
        public static void main(final String[] args) {
            final FakeBillingService billing = new FakeBillingService(Path.of(args[0]));
            for (int index = 0; index < REFUNDS_PER_PROCESS; index++) {
                billing.refund(billing.getOrder("INV-1249"), args[1] + "-" + index, false);
            }
        }
    }

    private static <T> List<T> runConcurrently(final IntFunction<Callable<T>> calls) throws Exception {
        final ExecutorService executor = Executors.newFixedThreadPool(CALLERS);
        final CountDownLatch startGate = new CountDownLatch(1);
        try {
            final List<Future<T>> futures = new ArrayList<>();
            for (int caller = 0; caller < CALLERS; caller++) {
                final Callable<T> call = calls.apply(caller);
                futures.add(executor.submit(() -> {
                    startGate.await();
                    return call.call();
                }));
            }
            startGate.countDown();
            final List<T> results = new ArrayList<>();
            for (Future<T> future : futures) {
                results.add(future.get(30, TimeUnit.SECONDS));
            }
            return results;
        } finally {
            executor.shutdownNow();
        }
    }

    private void assertNoTemporaryFiles() throws IOException {
        try (Stream<Path> files = Files.list(directory)) {
            assertEquals(List.of(), files.filter(file -> file.toString().endsWith(".tmp")).toList());
        }
    }
}
