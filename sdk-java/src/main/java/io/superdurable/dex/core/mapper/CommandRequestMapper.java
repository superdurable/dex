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

package io.superdurable.dex.core.mapper;

import io.superdurable.dex.core.command.CommandRequest;
import io.superdurable.dex.core.command.TimerCommand;
import io.superdurable.dex.core.communication.InternalChannelCommand;
import io.superdurable.dex.core.communication.SignalCommand;

import java.util.List;
import java.util.stream.Collectors;

public class CommandRequestMapper {
    public static io.superdurable.dex.gen.models.CommandRequest toGenerated(CommandRequest commandRequest) {

        final List<io.superdurable.dex.gen.models.SignalCommand> signalCommands = commandRequest.getCommands().stream()
                .filter(baseCommand -> baseCommand instanceof SignalCommand)
                .map(baseCommand -> (SignalCommand) baseCommand)
                .map(SignalCommandMapper::toGenerated)
                .collect(Collectors.toList());

        final List<io.superdurable.dex.gen.models.TimerCommand> timerCommands = commandRequest.getCommands().stream()
                .filter(baseCommand -> baseCommand instanceof TimerCommand)
                .map(baseCommand -> (TimerCommand) baseCommand)
                .map(TimerCommandMapper::toGenerated)
                .collect(Collectors.toList());

        final List<io.superdurable.dex.gen.models.InterStateChannelCommand> interstateChannelCommands = commandRequest.getCommands().stream()
                .filter(baseCommand -> baseCommand instanceof InternalChannelCommand)
                .map(baseCommand -> (InternalChannelCommand) baseCommand)
                .map(InterStateChannelCommandMapper::toGenerated)
                .collect(Collectors.toList());

        final io.superdurable.dex.gen.models.CommandRequest commandRequestResults = new io.superdurable.dex.gen.models.CommandRequest()
                .commandWaitingType(commandRequest.getCommandWaitingType());

        if (signalCommands.size() > 0) {
            commandRequestResults.signalCommands(signalCommands);
        }
        if (timerCommands.size() > 0) {
            commandRequestResults.timerCommands(timerCommands);
        }
        if (interstateChannelCommands.size() > 0) {
            commandRequestResults.interStateChannelCommands(interstateChannelCommands);
        }
        if (commandRequest.getCommandCombinations().size() > 0) {
            commandRequestResults.commandCombinations(commandRequest.getCommandCombinations());
        }
        return commandRequestResults;
    }
}
