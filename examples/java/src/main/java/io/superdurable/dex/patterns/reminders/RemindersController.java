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

package io.superdurable.dex.patterns.reminders;

import io.superdurable.dex.Client;
import io.superdurable.dex.shared.ExampleFlows;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

@RestController
@RequestMapping("/patterns/reminders")
public class RemindersController {
    private final Client client;
    private final ReminderFlow reminderFlow;

    public RemindersController(final Client client, final ReminderFlow reminderFlow) {
        this.client = client;
        this.reminderFlow = reminderFlow;
    }

    @GetMapping("/start")
    public ResponseEntity<String> start() {
        final String wfId = "reminder_test_id_" + System.nanoTime();
        client.startFlow(reminderFlow, wfId, null, ExampleFlows.startOptions());
        return ResponseEntity.ok(String.format("started workflowId: %s", wfId));
    }

    @GetMapping("/optout")
    public ResponseEntity<String> optout(@RequestParam final String workflowId) {
        client.publish(workflowId, reminderFlow.optOut, (Void) null);
        return ResponseEntity.ok("done");
    }
}
