package tag

import (
	"log/slog"
)

// Tag is the interface for logging system
type Tag struct {
	// Add slog attribute for direct slog usage
	attr slog.Attr
	// In a special case, we need to put more attributes for a tag
	// Currently only used for error-at
	extraAttrs []slog.Attr
}

// Attr returns an slog attribute
func (t *Tag) Attr() slog.Attr {
	return t.attr
}

func (t *Tag) ExtraAttrs() []slog.Attr {
	return t.extraAttrs
}

func (t *Tag) setExtra(attrs []slog.Attr) {
	t.extraAttrs = attrs
}

type Integer interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64
}
