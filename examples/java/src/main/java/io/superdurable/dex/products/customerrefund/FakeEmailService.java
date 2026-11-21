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

import com.fasterxml.jackson.core.type.TypeReference;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.SerializationFeature;

import java.io.IOException;
import java.io.UncheckedIOException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.ArrayList;
import java.util.List;

/** A fake email service that prints each message and sends one email per key. */
public class FakeEmailService {
    public static final Path DEFAULT_EMAIL_LEDGER = Path.of(".customer-refund/sent-emails.json");

    private static final ObjectMapper JSON = new ObjectMapper().enable(SerializationFeature.INDENT_OUTPUT);

    private final Path ledgerPath;

    public FakeEmailService(final Path ledgerPath) {
        this.ledgerPath = ledgerPath;
    }

    public synchronized void send(final String to, final String body, final String idempotencyKey) {
        final List<String> sent = listSentKeys();
        if (sent.contains(idempotencyKey)) {
            System.out.printf("Email to %s already sent (key %s)%n", to, idempotencyKey);
            return;
        }
        final List<String> updated = new ArrayList<>(sent);
        updated.add(idempotencyKey);
        try {
            Files.createDirectories(ledgerPath.toAbsolutePath().getParent());
            JSON.writeValue(ledgerPath.toFile(), updated);
        } catch (final IOException failure) {
            throw new UncheckedIOException(failure);
        }
        System.out.printf("Emailed %s: %s%n", to, body);
    }

    public List<String> listSentKeys() {
        if (!Files.exists(ledgerPath)) {
            return List.of();
        }
        try {
            return JSON.readValue(ledgerPath.toFile(), new TypeReference<List<String>>() {
            });
        } catch (final IOException failure) {
            throw new UncheckedIOException(failure);
        }
    }

    public Path ledgerPath() {
        return ledgerPath;
    }
}
