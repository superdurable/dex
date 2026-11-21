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

import java.nio.file.Path;
import java.util.List;

/** A fake email service that prints each message and sends one email per key. */
public class FakeEmailService {
    public static final Path DEFAULT_EMAIL_LEDGER = Path.of(".customer-refund/sent-emails.json");

    private final LockedJsonLedger<String> ledger;

    public FakeEmailService(final Path ledgerPath) {
        this.ledger = new LockedJsonLedger<>(ledgerPath, String.class);
    }

    /**
     * Sends the email unless one with this key was sent before.
     *
     * @return true when this call recorded and sent the email; false when the key was already sent
     */
    public boolean send(final String to, final String body, final String idempotencyKey) {
        // Recording the key first is the send; only that one call prints the email.
        final boolean sent = ledger.changeRecords(keys -> {
            if (keys.contains(idempotencyKey)) {
                return new LockedJsonLedger.Change<>(null, false);
            }
            keys.add(idempotencyKey);
            return new LockedJsonLedger.Change<>(keys, true);
        });
        if (sent) {
            System.out.printf("Emailed %s: %s%n", to, body);
        } else {
            System.out.printf("Email to %s already sent (key %s)%n", to, idempotencyKey);
        }
        return sent;
    }

    public List<String> listSentKeys() {
        return ledger.readRecords();
    }

    public Path ledgerPath() {
        return ledger.path();
    }
}
