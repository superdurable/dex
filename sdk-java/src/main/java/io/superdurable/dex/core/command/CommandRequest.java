/*
 * Legacy Materials in this file remain under their original licenses.
 * See LEGACY_NOTICES.md.
 */

/*
 * Modifications Copyright (c) 2026 Super Durable, Inc.
 *
 * Modifications after the Legacy Cutoff are licensed under the
 * Super Durable Source License 1.0.
 * Legacy Materials remain under their original licenses.
 * See LICENSE and LEGACY_NOTICES.md.
 */

package io.superdurable.dex.core.command;

import io.superdurable.dex.core.exceptions.CommandNotFoundException;
import io.superdurable.dex.gen.models.CommandCombination;
import io.superdurable.dex.gen.models.CommandWaitingType;
import org.immutables.value.Value;

import java.util.ArrayList;
import java.util.Arrays;
import java.util.List;
import java.util.stream.Collectors;

@Value.Immutable
public abstract class CommandRequest {
    public abstract List<BaseCommand> getCommands();

    public abstract List<CommandCombination> getCommandCombinations();

    public abstract CommandWaitingType getCommandWaitingType();

    // empty command request will jump to decide stage immediately.
    // It doesn't matter whatever CommandWaitingType is provided. But it's required so we have to put one.
    public static final CommandRequest empty = ImmutableCommandRequest.builder().commandWaitingType(CommandWaitingType.ALL_COMPLETED).build();

    /**
     * forAllCommandCompleted will wait for all the commands to complete
     *
     * @param commands all the commands
     * @return the command request
     */
    public static CommandRequest forAllCommandCompleted(final BaseCommand... commands) {
        return forAllCommandCompleted(Arrays.asList(commands));
    }

    /**
     * forAllCommandCompleted will wait for all the commands to complete
     *
     * @param commands all the commands
     * @return the command request
     */
    public static CommandRequest forAllCommandCompleted(final List<BaseCommand> commands) {
        return ImmutableCommandRequest.builder()
                .addAllCommands(commands)
                .commandWaitingType(CommandWaitingType.ALL_COMPLETED)
                .build();
    }

    /**
     * forAnyCommandCompleted will wait for any the commands to complete
     *
     * @param commands all the commands
     * @return the command request
     */
    public static CommandRequest forAnyCommandCompleted(final BaseCommand... commands) {
        return forAnyCommandCompleted(Arrays.asList(commands));
    }

    /**
     * forAnyCommandCompleted will wait for any the commands to complete
     *
     * @param commands all the commands
     * @return the command request
     */
    public static CommandRequest forAnyCommandCompleted(final List<BaseCommand> commands) {
        return ImmutableCommandRequest.builder()
                .addAllCommands(commands)
                .commandWaitingType(CommandWaitingType.ANY_COMPLETED)
                .build();
    }

    /**
     * This will wait for any combination to complete.
     * Using this requires every command has a commandId when created.
     * Functionally this one can cover both forAllCommandCompleted, forAnyCommandCompleted. So the other two are like "shortcuts" of it.
     *
     * @param commandCombinationLists a list of different combinations, each combination is a list of String as CommandIds
     * @param commands                all the commands
     * @return the command request
     */
    public static CommandRequest forAnyCommandCombinationCompleted(final List<List<String>> commandCombinationLists, final BaseCommand... commands) {
        return forAnyCommandCombinationCompleted(commandCombinationLists, Arrays.asList(commands));
    }
    /**
     * This will wait for any combination to complete.
     * Using this requires every command has a commandId when created.
     * Functionally this one can cover both forAllCommandCompleted, forAnyCommandCompleted. So the other two are like "shortcuts" of it.
     *
     * @param commandCombinationLists a list of different combinations, each combination is a list of String as CommandIds
     * @param commands                all the commands
     * @return the command request
     */
    public static CommandRequest forAnyCommandCombinationCompleted(final List<List<String>> commandCombinationLists, final List<BaseCommand> commands) {
        final List<String> allNonEmptyCommandsIds = commands.stream()
                .filter(command -> command.getCommandId().isPresent())
                .map(command -> command.getCommandId().get())
                .collect(Collectors.toList());

        final List<CommandCombination> combinations = new ArrayList<>();
        commandCombinationLists.forEach(commandIds -> {
            commandIds.forEach(commandId -> {
                if (!allNonEmptyCommandsIds.contains(commandId)) {
                    throw new CommandNotFoundException(String.format("Found unknown commandId in the combination list: %s", commandId));
                }
            });
            combinations.add(new CommandCombination().commandIds(commandIds));
        });
        return ImmutableCommandRequest.builder()
                .commandCombinations(combinations)
                .addAllCommands(commands)
                .commandWaitingType(CommandWaitingType.ANY_COMBINATION_COMPLETED)
                .build();
    }
}
