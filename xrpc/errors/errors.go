package errors

import (
	"encoding/json"
	"fmt"
)

type XrpcError struct {
	Tag     string `json:"error"`
	Message string `json:"message"`
}

func (x XrpcError) Error() string {
	if x.Message != "" {
		return fmt.Sprintf("%s: %s", x.Tag, x.Message)
	}
	return x.Tag
}

func NewXrpcError(opts ...ErrOpt) XrpcError {
	x := XrpcError{}
	for _, o := range opts {
		o(&x)
	}

	return x
}

type ErrOpt = func(xerr *XrpcError)

func WithTag(tag string) ErrOpt {
	return func(xerr *XrpcError) {
		xerr.Tag = tag
	}
}

func WithMessage[S ~string](s S) ErrOpt {
	return func(xerr *XrpcError) {
		xerr.Message = string(s)
	}
}

func WithError(e error) ErrOpt {
	return func(xerr *XrpcError) {
		xerr.Message = e.Error()
	}
}

var MissingActorDidError = NewXrpcError(
	WithTag("MissingActorDid"),
	WithMessage("actor DID not supplied"),
)

var AuthError = func(err error) XrpcError {
	return NewXrpcError(
		WithTag("Auth"),
		WithError(fmt.Errorf("signature verification failed: %w", err)),
	)
}

var InvalidRepoError = func(r string) XrpcError {
	return NewXrpcError(
		WithTag("InvalidRepo"),
		WithError(fmt.Errorf("supplied at-uri is not a repo: %s", r)),
	)
}

var GitError = func(e error) XrpcError {
	return NewXrpcError(
		WithTag("Git"),
		WithError(fmt.Errorf("git error: %w", e)),
	)
}

var AccessControlError = func(d string) XrpcError {
	return NewXrpcError(
		WithTag("AccessControl"),
		WithError(fmt.Errorf("DID does not have sufficent access permissions for this operation: %s", d)),
	)
}

var RepoExistsError = func(r string) XrpcError {
	return NewXrpcError(
		WithTag("RepoExists"),
		WithError(fmt.Errorf("repo already exists: %s", r)),
	)
}

func GenericError(err error) XrpcError {
	return NewXrpcError(
		WithTag("Generic"),
		WithError(err),
	)
}

func Unmarshal(errStr string) (XrpcError, error) {
	var xerr XrpcError
	err := json.Unmarshal([]byte(errStr), &xerr)
	if err != nil {
		return XrpcError{}, fmt.Errorf("failed to unmarshal XrpcError: %w", err)
	}
	return xerr, nil
}
