package log

import (
	"log/slog"

	"github.com/superdurable/dex/server/common/log/tag"
)

// Logger is our abstraction for logging
// Usage examples:
//
//	import "github.com/superdurable/dex/server/common/log/tag"
//	1) logger = logger.WithTags( tag.Component("myservice") )
//	   logger.Info("hello world")
//	2) logger.Info("hello error", tag.Error( err ) )
//	Note: msg should be static, it is not recommended to use fmt.Sprintf() for msg except for debugging message.
//	      Anything dynamic should be tagged as much as possible.
type Logger interface {
	Debugf(msg string, args ...any)
	Debug(msg string, tags ...tag.Tag)
	Info(msg string, tags ...tag.Tag)
	Warn(msg string, tags ...tag.Tag)
	Error(msg string, tags ...tag.Tag)
	// Fatal will emit the log and also os.Exit(1) the program
	Fatal(msg string, tags ...tag.Tag)
	WithTags(tags ...tag.Tag) Logger

	// GetSlogger returns slog.Logger for special case(grpc interceptor)
	GetSlogger() *slog.Logger
}

type noop struct{}

// NewNoop return a noop logger
func NewNoop() Logger {
	return &noop{}
}

func (n *noop) Debugf(msg string, args ...any)    {}
func (n *noop) Debug(msg string, tags ...tag.Tag)  {}
func (n *noop) Info(msg string, tags ...tag.Tag)   {}
func (n *noop) Warn(msg string, tags ...tag.Tag)   {}
func (n *noop) Error(msg string, tags ...tag.Tag)  {}
func (n *noop) Fatal(msg string, tags ...tag.Tag)  {}
func (n *noop) WithTags(tags ...tag.Tag) Logger    { return n }
func (n *noop) GetSlogger() *slog.Logger           { return nil }
