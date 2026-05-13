package handlers

import (
	"context"
	"errors"

	"connectrpc.com/connect"

	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/internal/interfaces/grpc/middleware"
)

// repoErrToConnect translates well-known repository sentinel errors into the
// matching Connect-RPC status codes, leaving everything else untouched (the
// Connect framework will surface those as `Unknown`/internal). It centralises
// what used to be a 5-line `if errors.Is(...) { return CodeX } return CodeY` block
// at ~40 handler sites.
func repoErrToConnect(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, repositories.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, repositories.ErrAlreadyExists):
		return connect.NewError(connect.CodeAlreadyExists, err)
	default:
		return err
	}
}

// authedUser returns the API user attached to the request by the auth interceptor.
// If the context has no user (request never passed through auth), the caller gets
// an `Unauthenticated` error suitable for returning directly from a handler.
func authedUser(ctx context.Context) (*entities.APIUser, error) {
	user, ok := middleware.GetUserFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}
	return user, nil
}
