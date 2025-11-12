package session

import (
	"context"

	toauth "tangled.org/core/appview/oauth"
)

type ctxKey struct{}

func IntoContext(ctx context.Context, sess Session) context.Context {
	return context.WithValue(ctx, ctxKey{}, &sess)
}

func FromContext(ctx context.Context) *Session {
	sess, ok := ctx.Value(ctxKey{}).(*Session)
	if !ok {
		return nil
	}
	return sess
}

func UserFromContext(ctx context.Context) *toauth.User {
	sess := FromContext(ctx)
	if sess == nil {
		return nil
	}
	return sess.User()
}
