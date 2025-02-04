package log

import (
	"context"
	"log/slog"
	"os"
)

// NewHandler sets up a new slog.Handler with the service name
// as an attribute
func NewHandler(name string) slog.Handler {
	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{})

	var attrs []slog.Attr
	attrs = append(attrs, slog.Attr{Key: "service", Value: slog.StringValue(name)})
	handler.WithAttrs(attrs)
	return handler
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
