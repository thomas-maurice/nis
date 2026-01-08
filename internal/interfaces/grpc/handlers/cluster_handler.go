package handlers

import (
	"context"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/thomas-maurice/nis/internal/application/services"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/internal/interfaces/grpc/mappers"
	"github.com/thomas-maurice/nis/internal/interfaces/grpc/middleware"
	pb "github.com/thomas-maurice/nis/gen/nis/v1"
	"github.com/thomas-maurice/nis/gen/nis/v1/nisv1connect"
)

// ClusterHandler implements the ClusterService gRPC service
type ClusterHandler struct {
	service     *services.ClusterService
	permService *services.PermissionService
}

// NewClusterHandler creates a new ClusterHandler
func NewClusterHandler(service *services.ClusterService, permService *services.PermissionService) nisv1connect.ClusterServiceHandler {
	return &ClusterHandler{
		service:     service,
		permService: permService,
	}
}

// CreateCluster creates a new cluster
func (h *ClusterHandler) CreateCluster(
	ctx context.Context,
	req *connect.Request[pb.CreateClusterRequest],
) (*connect.Response[pb.CreateClusterResponse], error) {
	// Get requesting user from context
	requestingUser, ok := middleware.GetUserFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}

	operatorID, err := mappers.ParseUUID(req.Msg.OperatorId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	// Check permission to read the operator (admin-only, clusters are system-level)
	if err := h.permService.CanReadOperator(ctx, requestingUser, operatorID); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	// Parse system account user ID if provided
	var systemAccountUserID *uuid.UUID
	if req.Msg.SystemAccountCreds != "" {
		// The proto field contains a user ID string that we need to parse
		// TODO: In a real implementation, this should be a proper user ID field in the proto
		parsed, err := mappers.ParseUUID(req.Msg.SystemAccountCreds)
		if err == nil {
			systemAccountUserID = &parsed
		}
	}

	cluster, err := h.service.CreateCluster(ctx, services.CreateClusterRequest{
		OperatorID:          operatorID,
		Name:                req.Msg.Name,
		Description:         req.Msg.Description,
		ServerURLs:          req.Msg.ServerUrls,
		SystemAccountPubKey: req.Msg.SystemAccountPubKey,
		SystemAccountUserID: systemAccountUserID,
		SkipVerifyTLS:       req.Msg.SkipVerifyTls,
	})
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&pb.CreateClusterResponse{
		Cluster: mappers.ClusterToProto(cluster),
	}), nil
}

// GetCluster retrieves a cluster by ID
func (h *ClusterHandler) GetCluster(
	ctx context.Context,
	req *connect.Request[pb.GetClusterRequest],
) (*connect.Response[pb.GetClusterResponse], error) {
	// Get requesting user from context
	requestingUser, ok := middleware.GetUserFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}

	id, err := mappers.ParseUUID(req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	cluster, err := h.service.GetCluster(ctx, id)
	if err != nil {
		if err == repositories.ErrNotFound {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, err
	}

	// Check permission to read the operator that owns this cluster
	if err := h.permService.CanReadOperator(ctx, requestingUser, cluster.OperatorID); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	return connect.NewResponse(&pb.GetClusterResponse{
		Cluster: mappers.ClusterToProto(cluster),
	}), nil
}

// GetClusterByName retrieves a cluster by name
func (h *ClusterHandler) GetClusterByName(
	ctx context.Context,
	req *connect.Request[pb.GetClusterByNameRequest],
) (*connect.Response[pb.GetClusterByNameResponse], error) {
	// Get requesting user from context
	requestingUser, ok := middleware.GetUserFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}

	cluster, err := h.service.GetClusterByName(ctx, req.Msg.Name)
	if err != nil {
		if err == repositories.ErrNotFound {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, err
	}

	// Check permission to read the operator that owns this cluster
	if err := h.permService.CanReadOperator(ctx, requestingUser, cluster.OperatorID); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	return connect.NewResponse(&pb.GetClusterByNameResponse{
		Cluster: mappers.ClusterToProto(cluster),
	}), nil
}

// ListClusters lists clusters for an operator
func (h *ClusterHandler) ListClusters(
	ctx context.Context,
	req *connect.Request[pb.ListClustersRequest],
) (*connect.Response[pb.ListClustersResponse], error) {
	// Get requesting user from context
	requestingUser, ok := middleware.GetUserFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}

	// If operator_id is empty, list all clusters across all operators (filtered by permissions)
	if req.Msg.OperatorId == "" {
		clusters, err := h.service.ListAllClusters(ctx, mappers.ProtoToListOptions(req.Msg.Options))
		if err != nil {
			return nil, err
		}

		// Filter clusters based on operator permissions
		var filteredClusters []*entities.Cluster
		for _, cluster := range clusters {
			if err := h.permService.CanReadOperator(ctx, requestingUser, cluster.OperatorID); err == nil {
				filteredClusters = append(filteredClusters, cluster)
			}
		}

		return connect.NewResponse(&pb.ListClustersResponse{
			Clusters: mappers.ClustersToProto(filteredClusters),
		}), nil
	}

	operatorID, err := mappers.ParseUUID(req.Msg.OperatorId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	// Check permission to read this operator
	if err := h.permService.CanReadOperator(ctx, requestingUser, operatorID); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	clusters, err := h.service.ListClustersByOperator(ctx, operatorID, mappers.ProtoToListOptions(req.Msg.Options))
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&pb.ListClustersResponse{
		Clusters: mappers.ClustersToProto(clusters),
	}), nil
}

// UpdateCluster updates a cluster
func (h *ClusterHandler) UpdateCluster(
	ctx context.Context,
	req *connect.Request[pb.UpdateClusterRequest],
) (*connect.Response[pb.UpdateClusterResponse], error) {
	// Get requesting user from context
	requestingUser, ok := middleware.GetUserFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}

	id, err := mappers.ParseUUID(req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	// First get the cluster to check which operator it belongs to
	existingCluster, err := h.service.GetCluster(ctx, id)
	if err != nil {
		if err == repositories.ErrNotFound {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, err
	}

	// Check permission to update the operator that owns this cluster
	if err := h.permService.CanUpdateOperator(requestingUser, existingCluster.OperatorID); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	updateReq := services.UpdateClusterRequest{
		Name:        mappers.StringPtr(req.Msg.GetName()),
		Description: mappers.StringPtr(req.Msg.GetDescription()),
		ServerURLs:  req.Msg.ServerUrls,
	}
	if req.Msg.SkipVerifyTls != nil {
		updateReq.SkipVerifyTLS = req.Msg.SkipVerifyTls
	}

	cluster, err := h.service.UpdateCluster(ctx, id, updateReq)
	if err != nil {
		if err == repositories.ErrNotFound {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, err
	}

	return connect.NewResponse(&pb.UpdateClusterResponse{
		Cluster: mappers.ClusterToProto(cluster),
	}), nil
}

// UpdateClusterCredentials updates cluster credentials
func (h *ClusterHandler) UpdateClusterCredentials(
	ctx context.Context,
	req *connect.Request[pb.UpdateClusterCredentialsRequest],
) (*connect.Response[pb.UpdateClusterCredentialsResponse], error) {
	// Get requesting user from context
	requestingUser, ok := middleware.GetUserFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}

	id, err := mappers.ParseUUID(req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	// First get the cluster to check which operator it belongs to
	existingCluster, err := h.service.GetCluster(ctx, id)
	if err != nil {
		if err == repositories.ErrNotFound {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, err
	}

	// Check permission to update the operator that owns this cluster
	if err := h.permService.CanUpdateOperator(requestingUser, existingCluster.OperatorID); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	// Parse system account user ID from the credentials field
	// TODO: The proto should have a proper user_id field instead of reusing system_account_creds
	systemAccountUserID, err := mappers.ParseUUID(req.Msg.SystemAccountCreds)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	cluster, err := h.service.UpdateClusterCredentials(ctx, id, systemAccountUserID)
	if err != nil {
		if err == repositories.ErrNotFound {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, err
	}

	return connect.NewResponse(&pb.UpdateClusterCredentialsResponse{
		Cluster: mappers.ClusterToProto(cluster),
	}), nil
}

// DeleteCluster deletes a cluster
func (h *ClusterHandler) DeleteCluster(
	ctx context.Context,
	req *connect.Request[pb.DeleteClusterRequest],
) (*connect.Response[pb.DeleteClusterResponse], error) {
	// Get requesting user from context
	requestingUser, ok := middleware.GetUserFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}

	id, err := mappers.ParseUUID(req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	// First get the cluster to check which operator it belongs to
	existingCluster, err := h.service.GetCluster(ctx, id)
	if err != nil {
		if err == repositories.ErrNotFound {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, err
	}

	// Check permission to delete the operator that owns this cluster
	if err := h.permService.CanDeleteOperator(requestingUser, existingCluster.OperatorID); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	err = h.service.DeleteCluster(ctx, id)
	if err != nil {
		if err == repositories.ErrNotFound {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, err
	}

	return connect.NewResponse(&pb.DeleteClusterResponse{}), nil
}

// GetClusterCredentials retrieves cluster credentials
func (h *ClusterHandler) GetClusterCredentials(
	ctx context.Context,
	req *connect.Request[pb.GetClusterCredentialsRequest],
) (*connect.Response[pb.GetClusterCredentialsResponse], error) {
	// Get requesting user from context
	requestingUser, ok := middleware.GetUserFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}

	id, err := mappers.ParseUUID(req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	// First get the cluster to check which operator it belongs to
	cluster, err := h.service.GetCluster(ctx, id)
	if err != nil {
		if err == repositories.ErrNotFound {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, err
	}

	// Check permission to read the operator that owns this cluster
	if err := h.permService.CanReadOperator(ctx, requestingUser, cluster.OperatorID); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	creds, err := h.service.GetClusterCredentials(ctx, id)
	if err != nil {
		if err == repositories.ErrNotFound {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, err
	}

	return connect.NewResponse(&pb.GetClusterCredentialsResponse{
		Credentials: creds,
	}), nil
}

// GenerateServerConfig generates a NATS server configuration
func (h *ClusterHandler) GenerateServerConfig(
	ctx context.Context,
	req *connect.Request[pb.GenerateServerConfigRequest],
) (*connect.Response[pb.GenerateServerConfigResponse], error) {
	// TODO: Implement server config generation
	// This requires integration with the NATS config generator from internal/infrastructure/nats
	return nil, connect.NewError(connect.CodeUnimplemented, nil)
}

// SyncCluster pushes all account JWTs to the NATS cluster resolver
func (h *ClusterHandler) SyncCluster(
	ctx context.Context,
	req *connect.Request[pb.SyncClusterRequest],
) (*connect.Response[pb.SyncClusterResponse], error) {
	// Get requesting user from context
	requestingUser, ok := middleware.GetUserFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}

	id, err := mappers.ParseUUID(req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	// First get the cluster to check which operator it belongs to
	cluster, err := h.service.GetCluster(ctx, id)
	if err != nil {
		if err == repositories.ErrNotFound {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, err
	}

	// Check permission to update the operator that owns this cluster (sync requires update permission)
	if err := h.permService.CanUpdateOperator(requestingUser, cluster.OperatorID); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	accountNames, err := h.service.SyncCluster(ctx, id)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&pb.SyncClusterResponse{
		AccountCount: int32(len(accountNames)),
		Accounts:     accountNames,
	}), nil
}
