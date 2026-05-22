package handlers

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	nisv1 "github.com/thomas-maurice/nis/gen/nis/v1"
	"github.com/thomas-maurice/nis/gen/nis/v1/nisv1connect"
	"github.com/thomas-maurice/nis/internal/application/services"
	"github.com/thomas-maurice/nis/internal/interfaces/grpc/mappers"
)

// SearchHandler implements the global SearchService RPC (P11). The actual
// RBAC narrowing happens inside SearchService — this handler is a thin
// adapter that authenticates the caller, marshals the proto enums, and maps
// validation errors to Connect status codes.
type SearchHandler struct {
	svc *services.SearchService
}

func NewSearchHandler(svc *services.SearchService) nisv1connect.SearchServiceHandler {
	return &SearchHandler{svc: svc}
}

func (h *SearchHandler) Search(
	ctx context.Context,
	req *connect.Request[nisv1.SearchRequest],
) (*connect.Response[nisv1.SearchResponse], error) {
	user, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}

	kinds := protoKindsToService(req.Msg.GetKinds())

	results, err := h.svc.Search(ctx, user, req.Msg.GetQuery(), kinds, int(req.Msg.GetLimit()))
	if err != nil {
		if errors.Is(err, services.ErrSearchQueryInvalid) {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		if errors.Is(err, services.ErrPermissionDenied) {
			return nil, connect.NewError(connect.CodePermissionDenied, err)
		}
		return nil, repoErrToConnect(err)
	}

	resp := &nisv1.SearchResponse{
		Operators:         mappers.OperatorsToProto(results.Operators),
		Accounts:          mappers.AccountsToProto(results.Accounts),
		Users:             mappers.UsersToProto(results.Users),
		ScopedSigningKeys: mappers.ScopedSigningKeysToProto(results.ScopedSigningKeys),
		Clusters:          mappers.ClustersToProto(results.Clusters),
		OperatorNames:     results.OperatorNames,
		AccountOperators:  results.AccountOperators,
	}
	return connect.NewResponse(resp), nil
}

// protoKindsToService converts the proto enum slice to the service-layer enum.
// The proto sentinel SEARCH_KIND_UNSPECIFIED is filtered out, and an
// all-unspecified or empty input collapses to nil, which SearchService treats
// as "search all five kinds".
func protoKindsToService(in []nisv1.SearchKind) []services.SearchKind {
	if len(in) == 0 {
		return nil
	}
	out := make([]services.SearchKind, 0, len(in))
	for _, k := range in {
		switch k {
		case nisv1.SearchKind_SEARCH_KIND_OPERATOR:
			out = append(out, services.SearchKindOperator)
		case nisv1.SearchKind_SEARCH_KIND_ACCOUNT:
			out = append(out, services.SearchKindAccount)
		case nisv1.SearchKind_SEARCH_KIND_USER:
			out = append(out, services.SearchKindUser)
		case nisv1.SearchKind_SEARCH_KIND_SCOPED_SIGNING_KEY:
			out = append(out, services.SearchKindScopedSigningKey)
		case nisv1.SearchKind_SEARCH_KIND_CLUSTER:
			out = append(out, services.SearchKindCluster)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
