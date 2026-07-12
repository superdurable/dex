package dex

import (
	"testing"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	"github.com/stretchr/testify/assert"
)

func cancelStepIDSet(ids ...string) map[string]bool {
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out
}

func TestResolveCancelTargets_PicksMatchingExeIDsSameParent(t *testing.T) {
	parent := "parent-1"
	caller := "callerStep-1"

	active := map[string]*pb.ActiveStepExecution{
		caller:       {FromStepExeId: parent},
		"siblingA-1": {FromStepExeId: parent},
		"siblingA-2": {FromStepExeId: parent},
		"siblingA-9": {FromStepExeId: "other-parent-1"},
		"siblingB-1": {FromStepExeId: parent},
		"siblingC-1": {FromStepExeId: parent},
	}

	got := resolveCancelTargets(caller, active, cancelStepIDSet("siblingA", "siblingB"))
	want := []string{"siblingA-1", "siblingA-2", "siblingB-1"}
	assert.ElementsMatch(t, want, got)
}

func TestResolveCancelTargets_NoCancelStepIDsReturnsNil(t *testing.T) {
	active := map[string]*pb.ActiveStepExecution{
		"self-1": {FromStepExeId: "p"},
		"b-1":    {FromStepExeId: "p"},
	}

	assert.Nil(t, resolveCancelTargets("self-1", active, nil))
	assert.Nil(t, resolveCancelTargets("self-1", active, map[string]bool{}))
}

func TestResolveCancelTargets_UnknownStepIDsIgnoredIdempotent(t *testing.T) {
	active := map[string]*pb.ActiveStepExecution{
		"self-1": {FromStepExeId: "p"},
		"keep-1": {FromStepExeId: "p"},
	}

	got := resolveCancelTargets("self-1", active, cancelStepIDSet("nonexistent", "missing"))
	assert.Nil(t, got)
}

func TestResolveCancelTargets_ParentMismatchExcludesAll(t *testing.T) {
	active := map[string]*pb.ActiveStepExecution{
		"self-1":   {FromStepExeId: "MY-parent"},
		"victim-1": {FromStepExeId: "OTHER-parent"},
		"victim-2": {FromStepExeId: "OTHER-parent"},
	}

	got := resolveCancelTargets("self-1", active, cancelStepIDSet("victim"))
	assert.Nil(t, got)
}

func TestResolveCancelTargets_ExcludesCaller(t *testing.T) {
	parent := "p-1"
	caller := "caller-1"
	active := map[string]*pb.ActiveStepExecution{
		caller:  {FromStepExeId: parent},
		"foo-1": {FromStepExeId: parent},
	}

	got := resolveCancelTargets(caller, active, cancelStepIDSet("caller", "foo"))
	assert.Equal(t, []string{"foo-1"}, got)
}
