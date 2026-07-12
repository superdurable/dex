// Package tasklist implements Cadence-style tasklist management for the
// dex matching service. Each tasklist partition is independently owned
// by a single matching-service instance and provides DB-backed task queues
// with rangeID fencing, opportunistic batch writes, FIFO read buffers, and
// BTree-based watermark GC.
//
// Wire-name encoding:
//   - User-submitted wire name (from worker SDK / taskprocessor): the
//     user-specified base name, e.g. "payment". This signals "I have not
//     picked a partition; please pick one for me."
//   - Server-internal wire name (after partition picking, including for
//     forwarding between matching nodes): "/__dex_sys/<base>/<i>"
//     for ANY partition i >= 0 (including the root partition i=0). This
//     signals "this partition has already been picked; do NOT re-pick."
//
// The reserved prefix `/__dex_sys/` is rejected as a user base name
// so the two forms never collide. Without this convention, a forwarded
// request whose picked partition happens to be 0 would be indistinguishable
// from a fresh user submission and the receiver would re-pick the
// partition — losing the original load-balancing decision.
package tasklist

import (
	"fmt"
	"strconv"
	"strings"
)

// reservedPartitionPrefix is the namespace under which partition-encoded
// wire names are constructed. User base names that start with this prefix
// are rejected at NewIdentifier.
const reservedPartitionPrefix = "/__dex_sys/"

// Identifier uniquely identifies a tasklist partition within a namespace.
// Constructed via NewIdentifier (always produces an encoded wire name) or
// ParseTasklistName (encoded vs base form preserved on the wireEncoded flag).
type Identifier struct {
	namespace   string
	baseName    string // user-specified name, e.g. "payment"
	partition   int32  // 0 = root, > 0 = leaf
	fullName    string // wire name: encoded "/__dex_sys/<base>/<i>" if wireEncoded; else == baseName
	wireEncoded bool   // true if fullName uses the reservedPartitionPrefix encoding
}

// NewIdentifier constructs an Identifier for a specific partition. The
// returned wire name is ALWAYS in the encoded form so that downstream
// receivers can tell "this partition was already picked; do not re-pick".
//
// Returns an error if baseName is empty or starts with the reserved
// prefix (which would create ambiguity with sub-partition names).
func NewIdentifier(namespace, baseName string, partition int32) (*Identifier, error) {
	if namespace == "" {
		return nil, fmt.Errorf("tasklist identifier: namespace must not be empty")
	}
	if baseName == "" {
		return nil, fmt.Errorf("tasklist identifier: base name must not be empty")
	}
	if strings.HasPrefix(baseName, reservedPartitionPrefix) {
		return nil, fmt.Errorf("tasklist identifier: base name %q must not start with reserved prefix %q", baseName, reservedPartitionPrefix)
	}
	if partition < 0 {
		return nil, fmt.Errorf("tasklist identifier: partition must be >= 0, got %d", partition)
	}
	return &Identifier{
		namespace:   namespace,
		baseName:    baseName,
		partition:   partition,
		fullName:    buildPartitionName(baseName, partition),
		wireEncoded: true,
	}, nil
}

// ParseTasklistName parses a wire-name. Two forms are accepted:
//   - User base form: bare name (e.g. "payment"). The returned Identifier
//     has partition=0 and wireEncoded=false. Callers should treat
//     wireEncoded=false as "the partition has not been picked yet" and
//     run the partition picker.
//   - Encoded form: "/__dex_sys/<base>/<i>" for any i >= 0. The
//     returned Identifier has wireEncoded=true. Callers should respect
//     the encoded partition and not re-pick.
func ParseTasklistName(namespace, wireName string) (*Identifier, error) {
	if namespace == "" {
		return nil, fmt.Errorf("tasklist identifier: namespace must not be empty")
	}
	if wireName == "" {
		return nil, fmt.Errorf("tasklist identifier: wire name must not be empty")
	}
	if !strings.HasPrefix(wireName, reservedPartitionPrefix) {
		// Bare base name: user submission, partition unset.
		return &Identifier{
			namespace:   namespace,
			baseName:    wireName,
			partition:   0,
			fullName:    wireName,
			wireEncoded: false,
		}, nil
	}
	// Encoded form: "/__dex_sys/<base>/<i>" with i >= 0.
	suffix := wireName[len(reservedPartitionPrefix):]
	slashIdx := strings.LastIndex(suffix, "/")
	if slashIdx <= 0 || slashIdx == len(suffix)-1 {
		return nil, fmt.Errorf("tasklist identifier: invalid partitioned name %q", wireName)
	}
	baseName := suffix[:slashIdx]
	partStr := suffix[slashIdx+1:]
	part64, err := strconv.ParseInt(partStr, 10, 32)
	if err != nil || part64 < 0 {
		return nil, fmt.Errorf("tasklist identifier: invalid partition number in %q: %v", wireName, err)
	}
	return &Identifier{
		namespace:   namespace,
		baseName:    baseName,
		partition:   int32(part64),
		fullName:    wireName,
		wireEncoded: true,
	}, nil
}

// Namespace returns the namespace that owns this tasklist.
func (id *Identifier) Namespace() string { return id.namespace }

// BaseName returns the user-specified tasklist name (without partition encoding).
func (id *Identifier) BaseName() string { return id.baseName }

// Partition returns the partition ID. 0 = root.
func (id *Identifier) Partition() int32 { return id.partition }

// IsRoot reports whether this identifier is the root partition.
// Independent of wireEncoded — purely a check on the partition number.
// Used by forwarder/manager to decide whether this partition has a parent
// to forward to (only non-root partitions do).
func (id *Identifier) IsRoot() bool { return id.partition == 0 }

// IsEncoded reports whether the wire name uses the reserved-prefix
// encoded form (the "partition already picked" signal). False only for
// identifiers parsed from a bare user base name.
//
// Use this — NOT IsRoot() — to decide whether the partition picker
// should be invoked at handler entry. IsRoot() conflates "partition 0"
// with "wire name was unencoded base form", which is the wrong predicate
// once partition 0 can be picked by the load balancer.
func (id *Identifier) IsEncoded() bool { return id.wireEncoded }

// FullName returns the wire name. For Identifiers built via NewIdentifier
// or ParseTasklistName from an encoded form, this is the encoded form.
// For Identifiers parsed from a bare user base name, this is the base
// name (callers should normally pick a partition and produce a fresh
// encoded Identifier rather than forwarding the bare form).
func (id *Identifier) FullName() string { return id.fullName }

// Parent returns the encoded wire name of the parent (root) partition
// for non-root identifiers, or empty string for root.
//
// In our two-level partitioning model (root + leaves, no intermediate),
// every non-root partition's parent is the root. Returns the encoded
// form ("/__dex_sys/<base>/0") so the receiver can tell "partition
// already picked, serve as partition 0, do not re-pick".
func (id *Identifier) Parent() string {
	if id.IsRoot() {
		return ""
	}
	return buildPartitionName(id.baseName, 0)
}

// String returns a debug representation: "<namespace>/<fullName>".
func (id *Identifier) String() string {
	return id.namespace + "/" + id.fullName
}

// buildPartitionName constructs the encoded wire name for any partition
// i >= 0. ALL partitions (including 0) get the reserved prefix so that
// "wire name has prefix" reliably signals "partition already picked".
func buildPartitionName(baseName string, partition int32) string {
	return fmt.Sprintf("%s%s/%d", reservedPartitionPrefix, baseName, partition)
}
