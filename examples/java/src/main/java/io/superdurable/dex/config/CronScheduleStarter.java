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

package io.superdurable.dex.config;

import io.superdurable.dex.Client;
import io.superdurable.dex.StartFlowOptions;
import io.superdurable.dex.exceptions.FlowAlreadyStartedException;
import io.superdurable.dex.patterns.cron.CronScheduleFlow;
import org.springframework.boot.context.event.ApplicationReadyEvent;
import org.springframework.context.event.EventListener;
import org.springframework.stereotype.Component;

import java.time.Duration;

@Component
public class CronScheduleStarter {
    public static final String CRON_SCHEDULE_FLOW_ID = "cron-schedule-sample";
    private static final String CRON_EXPRESSION = "*/1 * * * *";

    private final Client client;
    private final CronScheduleFlow cronScheduleFlow;

    public CronScheduleStarter(
            final Client client, final CronScheduleFlow cronScheduleFlow) {
        this.client = client;
        this.cronScheduleFlow = cronScheduleFlow;
    }

    @EventListener(ApplicationReadyEvent.class)
    public void startCronSchedule() {
        try {
            client.startFlow(
                    cronScheduleFlow,
                    CRON_SCHEDULE_FLOW_ID,
                    null,
                    StartFlowOptions.newBuilder()
                            .timeout(Duration.ofHours(1))
                            .cronSchedule(CRON_EXPRESSION)
                            .build());
        } catch (final FlowAlreadyStartedException ignored) {
        }
    }
}
