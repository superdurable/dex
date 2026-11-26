/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Sustainable Use License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0
 */

import io.superdurable.dex.Attribute;
import io.superdurable.dex.AttributeLock;
import io.superdurable.dex.AttributeMap;
import io.superdurable.dex.Client;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RPC;
import io.superdurable.dex.RPCAttributeMapLock;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepOptions;
import io.superdurable.dex.Wait;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.time.Duration;
import java.time.Instant;
import java.time.format.DateTimeFormatter;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.function.Supplier;
import java.util.logging.Logger;
import java.util.stream.Collectors;

class InjectedServiceFlow implements Flow<String> {
    static final Attribute<String> status = Attribute.define("status", String.class);
    private final Mailer mailer;

    InjectedServiceFlow(Mailer mailer) { this.mailer = mailer; }

    public StepList<String> getSteps() { return StepList.startStep(new Send()); }
    public PersistenceSchema getPersistenceSchema() { return PersistenceSchema.of(status); }

    @RPC(lockAttributes = {"status"})
    public void stop(Context context, String reason) { status.set(context, reason); }

    // dex:group group-id:send group-label:"Send"
    // dex:explanation text:"Send the reminder while holding the status lock."
    class Send implements Step<String> {
        public StepOptions getStepOptions() { return StepOptions.newBuilder().addExecuteLock(AttributeLock.of(status)).build(); }

        public StepDecision execute(Context context, String input) {
            mailer.send(input);
            return StepDecision.gracefulComplete(input);
        }
    }
}

class MovedCallFlow implements Flow<String> {
    static final Attribute<String> status = Attribute.define("status", String.class);
    private final Mailer mailer;

    MovedCallFlow(Mailer mailer) { this.mailer = mailer; }

    public StepList<String> getSteps() { return StepList.startStep(new Decide()).otherSteps(new Deliver()); }
    public PersistenceSchema getPersistenceSchema() { return PersistenceSchema.of(status); }

    @RPC(lockAttributes = {"status"})
    public void stop(Context context, String reason) { status.set(context, reason); }

    // dex:group group-id:send group-label:"Send"
    // dex:explanation text:"Commit the decision under the status lock."
    class Decide implements Step<String> {
        public StepOptions getStepOptions() { return StepOptions.newBuilder().addExecuteLock(AttributeLock.of(status)).build(); }

        public StepDecision execute(Context context, String input) {
            status.set(context, "sending");
            return StepDecision.goTo(Deliver.class, input);
        }
    }

    // dex:group group-id:send group-label:"Send"
    // dex:explanation text:"Send the reminder without a lock."
    class Deliver implements Step<String> {
        public StepDecision execute(Context context, String input) {
            mailer.send(input);
            return StepDecision.gracefulComplete(input);
        }
    }
}

class StepOnlyLockFlow implements Flow<String> {
    static final Attribute<String> status = Attribute.define("status", String.class);
    static final Attribute<Long> counter = Attribute.define("counter", Long.class);
    private final Mailer mailer;

    StepOnlyLockFlow(Mailer mailer) { this.mailer = mailer; }

    public StepList<String> getSteps() { return StepList.startStep(new First()).otherSteps(new Second()); }
    public PersistenceSchema getPersistenceSchema() { return PersistenceSchema.of(status, counter); }

    @RPC(lockAttributes = {"status"})
    public void stop(Context context, String reason) { status.set(context, reason); }

    // dex:group group-id:send group-label:"Send"
    // dex:explanation text:"Send under a lock only Steps take."
    class First implements Step<String> {
        public StepOptions getStepOptions() { return StepOptions.newBuilder().addExecuteLock(AttributeLock.of(counter)).build(); }

        public StepDecision execute(Context context, String input) {
            mailer.send(input);
            return StepDecision.goTo(Second.class, input);
        }
    }

    // dex:group group-id:send group-label:"Send"
    // dex:explanation text:"Send again under the same Step lock."
    class Second implements Step<String> {
        public StepOptions getStepOptions() { return StepOptions.newBuilder().addExecuteLock(AttributeLock.of(counter)).build(); }

        public StepDecision execute(Context context, String input) {
            mailer.send(input);
            return StepDecision.gracefulComplete(input);
        }
    }
}

class PureFlow implements Flow<PureFlow.Reminder> {
    static final Attribute<String> status = Attribute.define("status", String.class);
    static final Attribute<Reminder> latest = Attribute.define("latest", Reminder.class);
    private static final Logger LOGGER = Logger.getLogger(PureFlow.class.getName());
    private final Mailer mailer;

    PureFlow(Mailer mailer) { this.mailer = mailer; }

    record Reminder(String to, List<String> tags, Instant dueAt) {
        String label() { return to.trim() + "#" + tags.size(); }
    }

    enum Priority { LOW, HIGH }

    public StepList<Reminder> getSteps() { return StepList.startStep(new Capture()); }
    public PersistenceSchema getPersistenceSchema() { return PersistenceSchema.of(status, latest); }

    @RPC(lockAttributes = {"status"})
    public void stop(Context context, String reason) { status.set(context, reason); }

    static int countDown(int value) { return value <= 0 ? 0 : countUp(value - 1); }
    static int countUp(int value) { return countDown(value); }

    // dex:group group-id:record group-label:"Record"
    // dex:explanation text:"Record the reminder with SDK and pure JDK calls."
    class Capture implements Step<Reminder> {
        public StepOptions getStepOptions() { return StepOptions.newBuilder().addExecuteLock(AttributeLock.of(status)).build(); }

        public Wait waitFor(Context context, Reminder input) { return Wait.skipImmediately(); }

        public StepDecision execute(Context context, Reminder input) {
            var tags = input.tags().stream().map(tag -> tag.toUpperCase()).filter(tag -> !tag.isBlank())
                    .collect(Collectors.joining(","));
            var due = input.dueAt().plus(Duration.ofHours(2)).atZone(java.time.ZoneOffset.UTC).format(DateTimeFormatter.ISO_INSTANT);
            var text = new StringBuilder(String.format("%s due %s", input.label(), due)).append(tags).toString();
            Map<String, Integer> counts = Map.of("tags", input.tags().size());
            List<String> lines = new ArrayList<>();
            for (var line : input.tags()) lines.add(line.strip());
            counts.forEach((key, value) -> lines.add(key + value.intValue()));
            var previous = Optional.ofNullable(latest.get(context)).map(Reminder::to).orElse("");
            var priority = Priority.valueOf("HIGH").name();
            var ticket = new Ticket(input.to(), input.tags());
            lines.add(ticket.id().trim() + ticket.tags().size());
            LOGGER.info(text + previous + priority + Math.max(countDown(3), lines.size()) + context.getFlowId().length());
            status.set(context, previous.isEmpty() ? URI.create("https://example.com/" + input.to()).getPath() : previous);
            return StepDecision.gracefulComplete(input);
        }
    }
}

class SiblingMethodFlow implements Flow<String> {
    static final Attribute<String> status = Attribute.define("status", String.class);

    public StepList<String> getSteps() { return StepList.startStep(new Render()); }
    public PersistenceSchema getPersistenceSchema() { return PersistenceSchema.of(status); }

    @RPC(lockAttributes = {"status"})
    public void stop(Context context, String reason) { status.set(context, reason); }

    // dex:group group-id:render group-label:"Render"
    // dex:explanation text:"Render a ticket declared in another file."
    class Render implements Step<String> {
        public StepOptions getStepOptions() { return StepOptions.newBuilder().addExecuteLock(AttributeLock.of(status)).build(); }

        public StepDecision execute(Context context, String input) {
            status.set(context, new Ticket(input, List.of()).render());
            return StepDecision.gracefulComplete(input);
        }
    }
}

class HttpHelperFlow implements Flow<String> {
    static final Attribute<String> status = Attribute.define("status", String.class);

    public StepList<String> getSteps() { return StepList.startStep(new Notify()); }
    public PersistenceSchema getPersistenceSchema() { return PersistenceSchema.of(status); }

    @RPC(lockAttributes = {"status"})
    public void stop(Context context, String reason) { status.set(context, reason); }

    static void notifyCustomer(String input) throws Exception {
        var request = HttpRequest.newBuilder(URI.create("https://example.com/" + input)).build();
        HttpClient.newHttpClient().send(request, HttpResponse.BodyHandlers.discarding());
    }

    // dex:group group-id:notify group-label:"Notify"
    // dex:explanation text:"Notify the customer through a helper."
    class Notify implements Step<String> {
        public StepOptions getStepOptions() { return StepOptions.newBuilder().addExecuteLock(AttributeLock.of(status)).build(); }

        public StepDecision execute(Context context, String input) throws Exception {
            notifyCustomer(input);
            return StepDecision.gracefulComplete(input);
        }
    }
}

class ClientFlow implements Flow<String> {
    static final Attribute<String> status = Attribute.define("status", String.class);
    private final Client client;

    ClientFlow(Client client) { this.client = client; }

    public StepList<String> getSteps() { return StepList.startStep(new StopOther()); }
    public PersistenceSchema getPersistenceSchema() { return PersistenceSchema.of(status); }

    @RPC(lockAttributes = {"status"})
    public void stop(Context context, String reason) { status.set(context, reason); }

    // dex:group group-id:stop group-label:"Stop"
    // dex:explanation text:"Stop another Flow through the Dex Client."
    class StopOther implements Step<String> {
        public StepOptions getStepOptions() { return StepOptions.newBuilder().addExecuteLock(AttributeLock.of(status)).build(); }

        public StepDecision execute(Context context, String input) {
            client.stopFlow(input);
            return StepDecision.gracefulComplete(input);
        }
    }
}

class WaitForFlow implements Flow<String> {
    static final Attribute<String> status = Attribute.define("status", String.class);
    private final Supplier<String> token;

    WaitForFlow(Supplier<String> token) { this.token = token; }

    public StepList<String> getSteps() { return StepList.startStep(new Pause()); }
    public PersistenceSchema getPersistenceSchema() { return PersistenceSchema.of(status); }

    @RPC(lockAttributes = {"status"})
    public void stop(Context context, String reason) { status.set(context, reason); }

    // dex:group group-id:pause group-label:"Pause"
    // dex:explanation text:"Pause before completing."
    class Pause implements Step<String> {
        public StepOptions getStepOptions() { return StepOptions.newBuilder().addWaitForLock(AttributeLock.of(status)).build(); }

        public Wait waitFor(Context context, String input) throws Exception {
            Thread.sleep(10);
            status.set(context, token.get());
            return Wait.skipImmediately();
        }

        public StepDecision execute(Context context, String input) { return StepDecision.gracefulComplete(input); }
    }
}

class MapInstanceFlow implements Flow<String> {
    static final AttributeMap<String> orders = AttributeMap.define("orders", String.class);
    private final Mailer mailer;

    MapInstanceFlow(Mailer mailer) { this.mailer = mailer; }

    public StepList<String> getSteps() { return StepList.startStep(new Computed()).otherSteps(new Other()); }
    public PersistenceSchema getPersistenceSchema() { return PersistenceSchema.of(orders); }

    @RPC(lockAttributeMaps = {@RPCAttributeMapLock(attribute = "orders", instance = "vip")})
    public void expedite(Context context, String reason) { orders.set(context, "vip", reason); }

    // dex:group group-id:orders group-label:"Orders"
    // dex:explanation text:"Send under a computed order instance lock."
    class Computed implements Step<String> {
        public StepOptions getStepOptions() { return StepOptions.newBuilder().addExecuteLock(AttributeLock.of(orders, current())).build(); }

        private String current() { return "order-" + Instant.now().getEpochSecond(); }

        public StepDecision execute(Context context, String input) {
            mailer.send(input);
            return StepDecision.goTo(Other.class, input);
        }
    }

    // dex:group group-id:orders group-label:"Orders"
    // dex:explanation text:"Send under a different constant order instance lock."
    class Other implements Step<String> {
        public StepOptions getStepOptions() { return StepOptions.newBuilder().addExecuteLock(AttributeLock.of(orders, "standard")).build(); }

        public StepDecision execute(Context context, String input) {
            mailer.send(input);
            return StepDecision.gracefulComplete(input);
        }
    }
}
