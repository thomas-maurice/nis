package mappers

import (
	"github.com/thomas-maurice/nis/internal/domain/entities"
	pb "github.com/thomas-maurice/nis/gen/nis/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ClusterToProto converts domain Cluster to protobuf Cluster
func ClusterToProto(cluster *entities.Cluster) *pb.Cluster {
	if cluster == nil {
		return nil
	}

	var lastHealthCheck *timestamppb.Timestamp
	if cluster.LastHealthCheck != nil {
		lastHealthCheck = timestamppb.New(*cluster.LastHealthCheck)
	}

	return &pb.Cluster{
		Id:                   UUIDToString(cluster.ID),
		OperatorId:           UUIDToString(cluster.OperatorID),
		Name:                 cluster.Name,
		Description:          cluster.Description,
		ServerUrls:           cluster.ServerURLs,
		SystemAccountPubKey:  cluster.SystemAccountPubKey,
		CreatedAt:            timestamppb.New(cluster.CreatedAt),
		UpdatedAt:            timestamppb.New(cluster.UpdatedAt),
		Healthy:              cluster.Healthy,
		LastHealthCheck:      lastHealthCheck,
		HealthCheckError:     cluster.HealthCheckError,
		SkipVerifyTls:        cluster.SkipVerifyTLS,
	}
}

// ClustersToProto converts slice of domain Clusters to protobuf Clusters
func ClustersToProto(clusters []*entities.Cluster) []*pb.Cluster {
	result := make([]*pb.Cluster, len(clusters))
	for i, cluster := range clusters {
		result[i] = ClusterToProto(cluster)
	}
	return result
}
