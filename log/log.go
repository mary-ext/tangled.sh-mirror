package log

import (
	"context"
	"log/slog"
	"os"

	"github.com/charmbracelet/log"
)

func NewHandler(name string) slog.Handler {
	return log.NewWithOptions(os.Stderr, log.Options{
		ReportTimestamp: true,
		Prefix:          name,
		Level:           log.DebugLevel,
	})
}

func New(name string) *slog.Logger {
	return slog.New(NewHandler(name))
}

func NewContext(ctx context.Context, name string) context.Context {
	return IntoContext(ctx, New(name))
}

type ctxKey struct{}

// IntoContext adds a logger to a context. Use FromContext to
// pull the logger out.
func IntoContext(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

// FromContext returns a logger from a context.Context;
// if the passed context is nil, we return the default slog
// logger.
func FromContext(ctx context.Context) *slog.Logger {
	if ctx != nil {
		v := ctx.Value(ctxKey{})
		if v == nil {
			return slog.Default()
		}
		return v.(*slog.Logger)
	}

	return slog.Default()
}

// sublogger derives a new logger from an existing one by appending a suffix to its prefix.
func SubLogger(base *slog.Logger, suffix string) *slog.Logger {
	// try to get the underlying charmbracelet logger
	if cl, ok := base.Handler().(*log.Logger); ok {
		prefix := cl.GetPrefix()
		if prefix != "" {
			prefix = prefix + "/" + suffix
		} else {
			prefix = suffix
		}
		return slog.New(NewHandler(prefix))
	}

	// Fallback: no known handler type
	return slog.New(NewHandler(suffix))
}
