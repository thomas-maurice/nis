package handlers

import (
	"context"

	"connectrpc.com/connect"
	nisv1 "github.com/thomas-maurice/nis/gen/nis/v1"
	"github.com/thomas-maurice/nis/internal/application/services"
)

// ConfigHandler is the admin-only RPC surface for the running-config
// inspection feature. Admin-only at the handler layer via the shared
// requireAdmin gate (util.go); Casbin also gates `config:read` to admin
// only (casbin_policy.csv).
type ConfigHandler struct {
	svc *services.ConfigService
}

func NewConfigHandler(svc *services.ConfigService) *ConfigHandler {
	return &ConfigHandler{svc: svc}
}

func (h *ConfigHandler) GetRunningConfig(ctx context.Context, _ *connect.Request[nisv1.GetRunningConfigRequest]) (*connect.Response[nisv1.GetRunningConfigResponse], error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	yamlOut, err := h.svc.GetRunningConfig(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&nisv1.GetRunningConfigResponse{Yaml: yamlOut}), nil
}
