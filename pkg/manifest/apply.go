package manifest

import (
	"context"
	"fmt"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	nisv1 "github.com/thomas-maurice/nis/gen/nis/v1"
)

// Outcome describes what happened to a single item during apply or delete.
type Outcome int

const (
	OutcomeNoop    Outcome = iota
	OutcomeApplied         // create or update succeeded
	OutcomeFailed          // RPC or logic error
)

// ApplyResult is the full output of Apply.
type ApplyResult struct {
	Items []ApplyItem
}

// ApplyItem carries the per-object outcome of one apply dispatch.
type ApplyItem struct {
	Object  Object
	Action  Action
	Outcome Outcome
	Note    string
}

// applyCache holds name→UUID mappings built up as items succeed.
type applyCache struct {
	operatorByName  map[string]string // name → UUID
	accountByPath   map[string]string // "op/acc" → UUID
	scopedKeyByPath map[string]string // "op/acc/key" → UUID
	templateByPath  map[string]string // "op/tmpl" → UUID
}

func newApplyCache() *applyCache {
	return &applyCache{
		operatorByName:  make(map[string]string),
		accountByPath:   make(map[string]string),
		scopedKeyByPath: make(map[string]string),
		templateByPath:  make(map[string]string),
	}
}

// Apply executes a plan in topo order, dispatching Create/Update RPCs per item.
// Fails loud on the first RPC error: subsequent items are not attempted.
// Returns a non-nil ApplyResult in all cases (partial on error) plus the error.
func Apply(ctx context.Context, c PlannerClient, plan *PlanResult) (*ApplyResult, error) {
	cache := newApplyCache()
	result := &ApplyResult{}

	for _, item := range plan.Items {
		if item.Action == ActionNoop {
			result.Items = append(result.Items, ApplyItem{
				Object:  item.Object,
				Action:  item.Action,
				Outcome: OutcomeNoop,
				Note:    item.Note,
			})
			continue
		}

		out, err := applyItem(ctx, c, item, cache)
		result.Items = append(result.Items, out)
		if err != nil {
			return result, err
		}
	}
	return result, nil
}

func applyItem(ctx context.Context, c PlannerClient, item PlanItem, cache *applyCache) (ApplyItem, error) {
	switch item.Object.Kind {
	case KindOperator:
		return applyOperator(ctx, c, item, cache)
	case KindCluster:
		return applyCluster(ctx, c, item, cache)
	case KindAccount:
		return applyAccount(ctx, c, item, cache)
	case KindScopedSigningKey:
		return applyScopedSigningKey(ctx, c, item, cache)
	case KindUser:
		return applyUser(ctx, c, item, cache)
	case KindTemplate:
		return applyTemplate(ctx, c, item, cache)
	}
	return ApplyItem{Object: item.Object, Action: item.Action, Outcome: OutcomeFailed},
		fmt.Errorf("manifest apply: unknown kind %q", item.Object.Kind)
}

// applyTemplate creates or updates a template + its v1 / next version.
// Description-only updates leave the version alone; permission changes
// surface server-side as new versions (TemplateService.UpdateTemplate
// handles the diff). We pass both description and permissions in a
// single Update call when either drifted.
func applyTemplate(ctx context.Context, c PlannerClient, item PlanItem, cache *applyCache) (ApplyItem, error) {
	spec := item.Object.Template
	if spec == nil {
		spec = &TemplateSpec{}
	}
	meta := item.Object.Metadata
	path := meta.Operator + "/" + meta.Name

	switch item.Action {
	case ActionCreate:
		opID, err := resolveOperatorID(ctx, c, meta.Operator, cache)
		if err != nil {
			return failedItem(item), fmt.Errorf("manifest apply: Template %s: %w", path, err)
		}
		resp, err := c.TemplateClient().CreateTemplate(ctx, connect.NewRequest(&nisv1.CreateTemplateRequest{
			OperatorId:  opID,
			Name:        meta.Name,
			Description: spec.Description,
			Permissions: &nisv1.UserPermissions{
				PubAllow: spec.PubAllow,
				PubDeny:  spec.PubDeny,
				SubAllow: spec.SubAllow,
				SubDeny:  spec.SubDeny,
			},
			ResponsePermission: &nisv1.ResponsePermission{
				MaxMsgs: int32(spec.ResponseMaxMsgs),
				Expires: int64(spec.ResponseTTL),
			},
			ChangeNote: spec.ChangeNote,
		}))
		if err != nil {
			return failedItem(item), fmt.Errorf("manifest apply: Template %s: CreateTemplate: %w", path, err)
		}
		cache.templateByPath[path] = resp.Msg.GetTemplate().GetId()
		return ApplyItem{Object: item.Object, Action: item.Action, Outcome: OutcomeApplied}, nil

	case ActionUpdate:
		tplID := item.ExistingID.String()
		cache.templateByPath[path] = tplID
		desc := spec.Description
		req := &nisv1.UpdateTemplateRequest{
			Id:          tplID,
			Description: &desc,
			Permissions: &nisv1.UserPermissions{
				PubAllow: spec.PubAllow,
				PubDeny:  spec.PubDeny,
				SubAllow: spec.SubAllow,
				SubDeny:  spec.SubDeny,
			},
			ResponsePermission: &nisv1.ResponsePermission{
				MaxMsgs: int32(spec.ResponseMaxMsgs),
				Expires: int64(spec.ResponseTTL),
			},
			ChangeNote: spec.ChangeNote,
		}
		if _, err := c.TemplateClient().UpdateTemplate(ctx, connect.NewRequest(req)); err != nil {
			return failedItem(item), fmt.Errorf("manifest apply: Template %s: UpdateTemplate: %w", path, err)
		}
		return ApplyItem{Object: item.Object, Action: item.Action, Outcome: OutcomeApplied}, nil
	}
	return failedItem(item), fmt.Errorf("manifest apply: Template %s: unexpected action %d", path, item.Action)
}

// ---- operator ----

func applyOperator(ctx context.Context, c PlannerClient, item PlanItem, cache *applyCache) (ApplyItem, error) {
	spec := item.Object.Operator
	if spec == nil {
		spec = &OperatorSpec{}
	}
	meta := item.Object.Metadata

	switch item.Action {
	case ActionCreate:
		resp, err := c.OperatorClient().CreateOperator(ctx, connect.NewRequest(&nisv1.CreateOperatorRequest{
			Name:        meta.Name,
			Description: spec.Description,
		}))
		if err != nil {
			return failedItem(item), fmt.Errorf("manifest apply: Operator %s: CreateOperator: %w", meta.Name, err)
		}
		opID := resp.Msg.GetOperator().GetId()
		cache.operatorByName[meta.Name] = opID

		if spec.JWTPolicy != nil {
			if err := callSetJWTPolicy(ctx, c, opID, spec.JWTPolicy); err != nil {
				return failedItem(item), fmt.Errorf("manifest apply: Operator %s: SetJWTPolicy: %w", meta.Name, err)
			}
		}
		return ApplyItem{Object: item.Object, Action: item.Action, Outcome: OutcomeApplied}, nil

	case ActionUpdate:
		opID := item.ExistingID.String()
		cache.operatorByName[meta.Name] = opID

		for _, op := range item.Updates {
			switch op.RPC {
			case "UpdateOperator":
				desc := spec.Description
				if _, err := c.OperatorClient().UpdateOperator(ctx, connect.NewRequest(&nisv1.UpdateOperatorRequest{
					Id:          opID,
					Description: &desc,
				})); err != nil {
					return failedItem(item), fmt.Errorf("manifest apply: Operator %s: UpdateOperator: %w", meta.Name, err)
				}
			case "SetJWTPolicy":
				if spec.JWTPolicy != nil {
					if err := callSetJWTPolicy(ctx, c, opID, spec.JWTPolicy); err != nil {
						return failedItem(item), fmt.Errorf("manifest apply: Operator %s: SetJWTPolicy: %w", meta.Name, err)
					}
				}
			}
		}
		return ApplyItem{Object: item.Object, Action: item.Action, Outcome: OutcomeApplied}, nil
	}
	return failedItem(item), fmt.Errorf("manifest apply: Operator %s: unexpected action %d", meta.Name, item.Action)
}

func callSetJWTPolicy(ctx context.Context, c PlannerClient, opID string, p *JWTPolicySpec) error {
	userTTL := int64(p.UserJWTTTL.Seconds())
	accTTL := int64(p.AccountJWTTTL.Seconds())
	warn := int64(p.WarnWindow.Seconds())
	autoRenew := p.AutoRenew
	_, err := c.OperatorClient().SetJWTPolicy(ctx, connect.NewRequest(&nisv1.SetJWTPolicyRequest{
		Id:                   opID,
		UserJwtTtlSeconds:    &userTTL,
		AccountJwtTtlSeconds: &accTTL,
		JwtWarnWindowSeconds: &warn,
		JwtAutoRenew:         &autoRenew,
	}))
	return err
}

// ---- cluster ----

func applyCluster(ctx context.Context, c PlannerClient, item PlanItem, cache *applyCache) (ApplyItem, error) {
	if item.Action == ActionUpdate {
		return ApplyItem{
			Object:  item.Object,
			Action:  item.Action,
			Outcome: OutcomeFailed,
			Note:    "cluster updates via manifest are not supported",
		}, fmt.Errorf("manifest apply: Cluster %s: cluster updates via manifest are not supported", item.Object.Metadata.Name)
	}

	spec := item.Object.Cluster
	if spec == nil {
		spec = &ClusterSpec{}
	}
	meta := item.Object.Metadata

	opID, err := resolveOperatorID(ctx, c, meta.Operator, cache)
	if err != nil {
		return failedItem(item), fmt.Errorf("manifest apply: Cluster %s: %w", meta.Name, err)
	}

	resp, err := c.ClusterClient().CreateCluster(ctx, connect.NewRequest(&nisv1.CreateClusterRequest{
		OperatorId:  opID,
		Name:        meta.Name,
		Description: spec.Description,
		ServerUrls:  spec.ServerURLs,
	}))
	if err != nil {
		return failedItem(item), fmt.Errorf("manifest apply: Cluster %s: CreateCluster: %w", meta.Name, err)
	}
	_ = resp
	return ApplyItem{Object: item.Object, Action: item.Action, Outcome: OutcomeApplied}, nil
}

// ---- account ----

func applyAccount(ctx context.Context, c PlannerClient, item PlanItem, cache *applyCache) (ApplyItem, error) {
	spec := item.Object.Account
	if spec == nil {
		spec = &AccountSpec{}
	}
	meta := item.Object.Metadata
	path := meta.Operator + "/" + meta.Name

	switch item.Action {
	case ActionCreate:
		opID, err := resolveOperatorID(ctx, c, meta.Operator, cache)
		if err != nil {
			return failedItem(item), fmt.Errorf("manifest apply: Account %s: %w", path, err)
		}

		var limits *nisv1.JetStreamLimits
		if spec.JetStream != nil {
			limits = &nisv1.JetStreamLimits{
				Enabled:      spec.JetStream.Enabled,
				MaxMemory:    spec.JetStream.MaxMemory,
				MaxStorage:   spec.JetStream.MaxStorage,
				MaxStreams:   int32(spec.JetStream.MaxStreams),
				MaxConsumers: int32(spec.JetStream.MaxConsumers),
			}
		}
		resp, err := c.AccountClient().CreateAccount(ctx, connect.NewRequest(&nisv1.CreateAccountRequest{
			OperatorId:      opID,
			Name:            meta.Name,
			Description:     spec.Description,
			JetstreamLimits: limits,
		}))
		if err != nil {
			return failedItem(item), fmt.Errorf("manifest apply: Account %s: CreateAccount: %w", path, err)
		}
		cache.accountByPath[path] = resp.Msg.GetAccount().GetId()
		return ApplyItem{Object: item.Object, Action: item.Action, Outcome: OutcomeApplied}, nil

	case ActionUpdate:
		accID := item.ExistingID.String()
		cache.accountByPath[path] = accID

		for _, op := range item.Updates {
			switch op.RPC {
			case "UpdateAccount":
				desc := spec.Description
				if _, err := c.AccountClient().UpdateAccount(ctx, connect.NewRequest(&nisv1.UpdateAccountRequest{
					Id:          accID,
					Description: &desc,
				})); err != nil {
					return failedItem(item), fmt.Errorf("manifest apply: Account %s: UpdateAccount: %w", path, err)
				}
			case "UpdateJetStreamLimits":
				if spec.JetStream != nil {
					if _, err := c.AccountClient().UpdateJetStreamLimits(ctx, connect.NewRequest(&nisv1.UpdateJetStreamLimitsRequest{
						Id: accID,
						Limits: &nisv1.JetStreamLimits{
							Enabled:      spec.JetStream.Enabled,
							MaxMemory:    spec.JetStream.MaxMemory,
							MaxStorage:   spec.JetStream.MaxStorage,
							MaxStreams:   int32(spec.JetStream.MaxStreams),
							MaxConsumers: int32(spec.JetStream.MaxConsumers),
						},
					})); err != nil {
						return failedItem(item), fmt.Errorf("manifest apply: Account %s: UpdateJetStreamLimits: %w", path, err)
					}
				}
			}
		}
		return ApplyItem{Object: item.Object, Action: item.Action, Outcome: OutcomeApplied}, nil
	}
	return failedItem(item), fmt.Errorf("manifest apply: Account %s: unexpected action %d", path, item.Action)
}

// ---- scoped signing key ----

func applyScopedSigningKey(ctx context.Context, c PlannerClient, item PlanItem, cache *applyCache) (ApplyItem, error) {
	spec := item.Object.ScopedSigningKey
	if spec == nil {
		spec = &ScopedSigningKeySpec{}
	}
	meta := item.Object.Metadata
	path := meta.Operator + "/" + meta.Account + "/" + meta.Name
	accPath := meta.Operator + "/" + meta.Account

	switch item.Action {
	case ActionCreate:
		accID, err := resolveAccountID(ctx, c, meta.Operator, meta.Account, cache)
		if err != nil {
			return failedItem(item), fmt.Errorf("manifest apply: ScopedSigningKey %s: %w", path, err)
		}

		createReq := &nisv1.CreateScopedSigningKeyRequest{
			AccountId:   accID,
			Name:        meta.Name,
			Description: spec.Description,
			Permissions: &nisv1.UserPermissions{
				PubAllow: spec.PubAllow,
				PubDeny:  spec.PubDeny,
				SubAllow: spec.SubAllow,
				SubDeny:  spec.SubDeny,
			},
			ResponsePermission: &nisv1.ResponsePermission{
				MaxMsgs: int32(spec.ResponseMaxMsgs),
				Expires: int64(spec.ResponseTTL),
			},
			TrackLatest: spec.TrackLatest,
		}
		// Template ref: server snapshot wins over manifest pub/sub
		// fields on create. resolveOperatorID handles the cache or
		// remote lookup; templates are operator-scoped so we only need
		// the operator + template name to resolve server-side.
		if spec.Template != "" {
			opID, err := resolveOperatorID(ctx, c, meta.Operator, cache)
			if err != nil {
				return failedItem(item), fmt.Errorf("manifest apply: ScopedSigningKey %s: resolve operator for template ref: %w", path, err)
			}
			createReq.Template = &nisv1.TemplateRef{
				OperatorId:    opID,
				TemplateName:  spec.Template,
				VersionNumber: int32(spec.TemplateVersion),
			}
		}

		resp, err := c.ScopedSigningKeyClient().CreateScopedSigningKey(ctx, connect.NewRequest(createReq))
		if err != nil {
			return failedItem(item), fmt.Errorf("manifest apply: ScopedSigningKey %s: CreateScopedSigningKey: %w", path, err)
		}
		cache.scopedKeyByPath[path] = resp.Msg.GetKey().GetId()
		return ApplyItem{Object: item.Object, Action: item.Action, Outcome: OutcomeApplied}, nil

	case ActionUpdate:
		skkID := item.ExistingID.String()

		// Default scoped key on a brand-new account: ExistingID is uuid.Nil.
		// The server auto-created "default" when the account was created.
		// Fetch it by listing scoped keys for the account.
		if item.ExistingID == uuid.Nil {
			accID, err := resolveAccountID(ctx, c, meta.Operator, meta.Account, cache)
			if err != nil {
				return failedItem(item), fmt.Errorf("manifest apply: ScopedSigningKey %s: resolve account for default key: %w", path, err)
			}
			resp, err := c.ScopedSigningKeyClient().ListScopedSigningKeys(ctx, connect.NewRequest(&nisv1.ListScopedSigningKeysRequest{
				AccountId: accID,
			}))
			if err != nil {
				return failedItem(item), fmt.Errorf("manifest apply: ScopedSigningKey %s: ListScopedSigningKeys: %w", path, err)
			}
			found := false
			for _, sk := range resp.Msg.GetKeys() {
				if sk.GetName() == "default" {
					skkID = sk.GetId()
					found = true
					break
				}
			}
			if !found {
				return failedItem(item), fmt.Errorf("manifest apply: ScopedSigningKey %s: default key not found after account create", path)
			}
		}
		cache.scopedKeyByPath[path] = skkID
		_ = accPath

		for _, op := range item.Updates {
			switch op.RPC {
			case "UpdateScopedSigningKey":
				desc := spec.Description
				if _, err := c.ScopedSigningKeyClient().UpdateScopedSigningKey(ctx, connect.NewRequest(&nisv1.UpdateScopedSigningKeyRequest{
					Id:          skkID,
					Description: &desc,
				})); err != nil {
					return failedItem(item), fmt.Errorf("manifest apply: ScopedSigningKey %s: UpdateScopedSigningKey: %w", path, err)
				}
			case "UpdatePermissions":
				if _, err := c.ScopedSigningKeyClient().UpdatePermissions(ctx, connect.NewRequest(&nisv1.UpdatePermissionsRequest{
					Id: skkID,
					Permissions: &nisv1.UserPermissions{
						PubAllow: spec.PubAllow,
						PubDeny:  spec.PubDeny,
						SubAllow: spec.SubAllow,
						SubDeny:  spec.SubDeny,
					},
					ResponsePermission: &nisv1.ResponsePermission{
						MaxMsgs: int32(spec.ResponseMaxMsgs),
						Expires: int64(spec.ResponseTTL),
					},
				})); err != nil {
					return failedItem(item), fmt.Errorf("manifest apply: ScopedSigningKey %s: UpdatePermissions: %w", path, err)
				}
			case "SetTrackLatest":
				if _, err := c.ScopedSigningKeyClient().SetTrackLatest(ctx, connect.NewRequest(&nisv1.SetTrackLatestRequest{
					Id:      skkID,
					Enabled: spec.TrackLatest,
				})); err != nil {
					return failedItem(item), fmt.Errorf("manifest apply: ScopedSigningKey %s: SetTrackLatest: %w", path, err)
				}
			}
		}
		return ApplyItem{Object: item.Object, Action: item.Action, Outcome: OutcomeApplied}, nil
	}
	return failedItem(item), fmt.Errorf("manifest apply: ScopedSigningKey %s: unexpected action %d", path, item.Action)
}

// ---- user ----

func applyUser(ctx context.Context, c PlannerClient, item PlanItem, cache *applyCache) (ApplyItem, error) {
	spec := item.Object.User
	if spec == nil {
		spec = &UserSpec{}
	}
	meta := item.Object.Metadata
	path := meta.Operator + "/" + meta.Account + "/" + meta.Name

	switch item.Action {
	case ActionCreate:
		accID, err := resolveAccountID(ctx, c, meta.Operator, meta.Account, cache)
		if err != nil {
			return failedItem(item), fmt.Errorf("manifest apply: User %s: %w", path, err)
		}

		var skkID string
		if spec.ScopedKey != "" {
			skkPath := meta.Operator + "/" + meta.Account + "/" + spec.ScopedKey
			if cached, ok := cache.scopedKeyByPath[skkPath]; ok {
				skkID = cached
			} else {
				// Not in cache — look up from server.
				resp, err := c.ScopedSigningKeyClient().GetScopedSigningKeyByName(ctx, connect.NewRequest(&nisv1.GetScopedSigningKeyByNameRequest{
					AccountId: accID,
					Name:      spec.ScopedKey,
				}))
				if err != nil {
					return failedItem(item), fmt.Errorf("manifest apply: User %s: GetScopedSigningKeyByName(%q): %w", path, spec.ScopedKey, err)
				}
				skkID = resp.Msg.GetKey().GetId()
			}
		}

		if _, err := c.UserClient().CreateUser(ctx, connect.NewRequest(&nisv1.CreateUserRequest{
			AccountId:          accID,
			Name:               meta.Name,
			Description:        spec.Description,
			ScopedSigningKeyId: skkID,
		})); err != nil {
			return failedItem(item), fmt.Errorf("manifest apply: User %s: CreateUser: %w", path, err)
		}
		return ApplyItem{Object: item.Object, Action: item.Action, Outcome: OutcomeApplied}, nil

	case ActionUpdate:
		userID := item.ExistingID.String()

		for _, op := range item.Updates {
			if op.RPC != "UpdateUser" {
				continue
			}
			req := &nisv1.UpdateUserRequest{Id: userID}
			desc := spec.Description
			req.Description = &desc

			if spec.JWTTTL == nil {
				req.ClearJwtTtl = true
			} else {
				ttlSecs := int64(spec.JWTTTL.Seconds())
				req.JwtTtlSeconds = &ttlSecs
				req.ClearJwtTtl = false
			}

			if _, err := c.UserClient().UpdateUser(ctx, connect.NewRequest(req)); err != nil {
				return failedItem(item), fmt.Errorf("manifest apply: User %s: UpdateUser: %w", path, err)
			}
		}
		return ApplyItem{Object: item.Object, Action: item.Action, Outcome: OutcomeApplied}, nil
	}
	return failedItem(item), fmt.Errorf("manifest apply: User %s: unexpected action %d", path, item.Action)
}

// ---- helpers ----

func failedItem(item PlanItem) ApplyItem {
	return ApplyItem{Object: item.Object, Action: item.Action, Outcome: OutcomeFailed}
}

// resolveOperatorID returns the operator UUID from cache or via GetOperatorByName.
func resolveOperatorID(ctx context.Context, c PlannerClient, opName string, cache *applyCache) (string, error) {
	if id, ok := cache.operatorByName[opName]; ok {
		return id, nil
	}
	resp, err := c.OperatorClient().GetOperatorByName(ctx, connect.NewRequest(&nisv1.GetOperatorByNameRequest{
		Name: opName,
	}))
	if err != nil {
		return "", fmt.Errorf("GetOperatorByName(%q): %w", opName, err)
	}
	id := resp.Msg.GetOperator().GetId()
	cache.operatorByName[opName] = id
	return id, nil
}

// resolveAccountID returns the account UUID from cache or via GetAccountByName.
func resolveAccountID(ctx context.Context, c PlannerClient, opName, accName string, cache *applyCache) (string, error) {
	path := opName + "/" + accName
	if id, ok := cache.accountByPath[path]; ok {
		return id, nil
	}
	opID, err := resolveOperatorID(ctx, c, opName, cache)
	if err != nil {
		return "", err
	}
	resp, err := c.AccountClient().GetAccountByName(ctx, connect.NewRequest(&nisv1.GetAccountByNameRequest{
		OperatorId: opID,
		Name:       accName,
	}))
	if err != nil {
		return "", fmt.Errorf("GetAccountByName(%q/%q): %w", opName, accName, err)
	}
	id := resp.Msg.GetAccount().GetId()
	cache.accountByPath[path] = id
	return id, nil
}
