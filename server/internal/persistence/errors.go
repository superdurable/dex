package persistence

import (
	"github.com/superdurable/dex/server/common/errors"
)

const errSubCategory = "persistence"

func NewNotFoundError(msg string) errors.CategorizedError {
	return errors.NewNotFoundError(msg, nil)
}

func NewConflictError(msg string) errors.CategorizedError {
	return errors.NewConflictError(msg, nil)
}

// NewVersionMismatchError indicates a CAS failure (another instance modified the record).
func NewVersionMismatchError(msg string) errors.CategorizedError {
	return errors.NewCASError("version mismatch: "+msg, nil)
}

// NewLeaseNotExpiredError indicates the shard/tasklist lease hasn't expired yet.
func NewLeaseNotExpiredError(msg string) errors.CategorizedError {
	return errors.NewConflictError("lease not expired: "+msg, nil)
}

// NewRangeIDMismatchError indicates a fenced tasklist write was rejected
// because the caller's range_id does not match the partition's current
// metadata. The caller should treat this as ownership lost and stop
// processing the tasklist partition.
func NewRangeIDMismatchError(msg string) errors.CategorizedError {
	return errors.NewConflictError("range_id mismatch: "+msg, nil)
}

func NewInternalError(msg string, fromErr error) errors.CategorizedError {
	return errors.NewInternalErrorWithSubCategory(msg, fromErr, errSubCategory)
}

func NewTimeoutError(msg string, fromErr error) errors.CategorizedError {
	return errors.NewTimeoutError(msg, fromErr)
}
