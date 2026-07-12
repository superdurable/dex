package tag

import "log/slog"

// Tag wraps an slog.Attr for structured logging.
type Tag struct {
	attr slog.Attr
}

// Attr returns the underlying slog attribute.
func (t Tag) Attr() slog.Attr { return t.attr }

func stringTag(key, value string) Tag   { return Tag{attr: slog.String(key, value)} }
func intTag(key string, value int) Tag   { return Tag{attr: slog.Int(key, value)} }

// ============ Tags used by benchmark ============

func Error(err error) Tag {
	if err == nil {
		return stringTag("error", "nil")
	}
	return stringTag("error", err.Error())
}

func Mode(mode string) Tag        { return stringTag("mode", mode) }
func RunID(id string) Tag         { return stringTag("run_id", id) }
func TaskListName(name string) Tag { return stringTag("task_list_name", name) }
func Namespace(ns string) Tag     { return stringTag("namespace", ns) }
func ChannelName(name string) Tag { return stringTag("channel_name", name) }
func Count(c int) Tag             { return intTag("count", c) }
func Attempt(a int) Tag           { return intTag("attempt", a) }
func NumSteps(n int) Tag          { return intTag("num_steps", n) }
func StateSize(n int) Tag         { return intTag("state_size", n) }

// MaxConcurrentSubAgents is the multi-agent benchmark's fan-out width
// (the N in mainInitStep's GoToMany). Logged by /agentTrigger.
func MaxConcurrentSubAgents(n int) Tag { return intTag("max_concurrent_subagents", n) }

// SubAgentRunID is the deterministic runID of a child SubAgent flow,
// distinct from the parent MainAgent's RunID. Used by startSubAgentStep
// and waitForSubAgentStep so log lines can be correlated across the
// two runs.
func SubAgentRunID(id string) Tag { return stringTag("subagent_run_id", id) }
