package manifest

import (
	"context"
	"fmt"
	"sort"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	nisv1 "github.com/thomas-maurice/nis/gen/nis/v1"
	"github.com/thomas-maurice/nis/gen/nis/v1/nisv1connect"
	"github.com/thomas-maurice/nis/internal/client"
)

// Action classifies what the apply engine should do for a plan item.
type Action int

const (
	ActionNoop   Action = iota
	ActionCreate        // entity does not exist server-side
	ActionUpdate        // entity exists but has drifted fields
)

// PlanItem is the classification result for one manifest Object.
type PlanItem struct {
	Object     Object
	Action     Action
	ExistingID uuid.UUID // uuid.Nil for ActionCreate
	Updates    []UpdateOp
	Note       string
}

// UpdateOp names a single Update* RPC and which fields will change.
type UpdateOp struct {
	RPC    string
	Fields []string
}

// Summary counts items by action.
type Summary struct {
	Create int
	Update int
	Noop   int
}

// PlanResult is the full output of Plan.
type PlanResult struct {
	Items   []PlanItem
	Summary Summary
}

// PlannerClient is the slice of *client.Client the planner uses.
// Defining it as an interface keeps the planner testable without a real server.
type PlannerClient interface {
	OperatorClient() nisv1connect.OperatorServiceClient
	AccountClient() nisv1connect.AccountServiceClient
	UserClient() nisv1connect.UserServiceClient
	ScopedSigningKeyClient() nisv1connect.ScopedSigningKeyServiceClient
	ClusterClient() nisv1connect.ClusterServiceClient
}

// clientAdapter wraps *client.Client to satisfy PlannerClient.
type clientAdapter struct{ c *client.Client }

func (a clientAdapter) OperatorClient() nisv1connect.OperatorServiceClient {
	return a.c.Operator
}
func (a clientAdapter) AccountClient() nisv1connect.AccountServiceClient {
	return a.c.Account
}
func (a clientAdapter) UserClient() nisv1connect.UserServiceClient {
	return a.c.User
}
func (a clientAdapter) ScopedSigningKeyClient() nisv1connect.ScopedSigningKeyServiceClient {
	return a.c.ScopedSigningKey
}
func (a clientAdapter) ClusterClient() nisv1connect.ClusterServiceClient {
	return a.c.Cluster
}

// NewClientAdapter wraps a *client.Client so it satisfies PlannerClient.
func NewClientAdapter(c *client.Client) PlannerClient { return clientAdapter{c} }

// serverState holds all current-server entities fetched during Step 1.
type serverState struct {
	operators  map[string]*nisv1.Operator                               // by name
	accounts   map[string]map[string]*nisv1.Account                     // [opName][accName]
	clusters   map[string]map[string]*nisv1.Cluster                     // [opName][clusterName]
	scopedKeys map[string]map[string]map[string]*nisv1.ScopedSigningKey // [opName][accName][keyName]
	users      map[string]map[string]map[string]*nisv1.User             // [opName][accName][userName]
}

func newServerState() *serverState {
	return &serverState{
		operators:  make(map[string]*nisv1.Operator),
		accounts:   make(map[string]map[string]*nisv1.Account),
		clusters:   make(map[string]map[string]*nisv1.Cluster),
		scopedKeys: make(map[string]map[string]map[string]*nisv1.ScopedSigningKey),
		users:      make(map[string]map[string]map[string]*nisv1.User),
	}
}

// Plan fetches current server state and classifies each Object in batch.
// Items in the returned PlanResult are ordered: Operator, Cluster, Account,
// ScopedSigningKey, User.
func Plan(ctx context.Context, c PlannerClient, batch []Object) (*PlanResult, error) {
	state, err := fetchState(ctx, c, batch)
	if err != nil {
		return nil, fmt.Errorf("manifest plan: fetch state: %w", err)
	}

	// Walk in topo order: Operator → Cluster → Account → ScopedSigningKey → User.
	order := []string{KindOperator, KindCluster, KindAccount, KindScopedSigningKey, KindUser}
	byKind := make(map[string][]Object)
	for _, obj := range batch {
		byKind[obj.Kind] = append(byKind[obj.Kind], obj)
	}

	// Track which accounts are being created so we can handle the default-key
	// special case (Step 3).
	newAccounts := make(map[string]map[string]bool) // [opName][accName]

	var result PlanResult
	for _, k := range order {
		for _, obj := range byKind[k] {
			item, err := classifyObject(obj, state, newAccounts)
			if err != nil {
				return nil, err
			}
			result.Items = append(result.Items, item)
			switch item.Action {
			case ActionCreate:
				result.Summary.Create++
				if obj.Kind == KindAccount {
					if newAccounts[obj.Metadata.Operator] == nil {
						newAccounts[obj.Metadata.Operator] = make(map[string]bool)
					}
					newAccounts[obj.Metadata.Operator][obj.Metadata.Name] = true
				}
			case ActionUpdate:
				result.Summary.Update++
			case ActionNoop:
				result.Summary.Noop++
			}
		}
	}
	return &result, nil
}

// fetchState performs all List/Get RPCs needed to populate serverState.
func fetchState(ctx context.Context, c PlannerClient, batch []Object) (*serverState, error) {
	state := newServerState()

	// Collect unique operator names referenced by the batch.
	opNames := make(map[string]struct{})
	for _, obj := range batch {
		switch obj.Kind {
		case KindOperator:
			opNames[obj.Metadata.Name] = struct{}{}
		default:
			if obj.Metadata.Operator != "" {
				opNames[obj.Metadata.Operator] = struct{}{}
			}
		}
	}

	for opName := range opNames {
		resp, err := c.OperatorClient().GetOperatorByName(ctx, connect.NewRequest(&nisv1.GetOperatorByNameRequest{
			Name: opName,
		}))
		if err != nil {
			if connect.CodeOf(err) == connect.CodeNotFound {
				// Operator doesn't exist; downstream kinds will also be creates.
				continue
			}
			return nil, fmt.Errorf("GetOperatorByName(%q): %w", opName, err)
		}
		op := resp.Msg.GetOperator()
		state.operators[opName] = op

		// Fetch accounts for this operator.
		accResp, err := c.AccountClient().ListAccounts(ctx, connect.NewRequest(&nisv1.ListAccountsRequest{
			OperatorId: op.GetId(),
		}))
		if err != nil {
			return nil, fmt.Errorf("ListAccounts(operator=%q): %w", opName, err)
		}
		state.accounts[opName] = make(map[string]*nisv1.Account)
		for _, acc := range accResp.Msg.GetAccounts() {
			state.accounts[opName][acc.GetName()] = acc

			// Fetch scoped signing keys for this account.
			skkResp, err := c.ScopedSigningKeyClient().ListScopedSigningKeys(ctx, connect.NewRequest(&nisv1.ListScopedSigningKeysRequest{
				AccountId: acc.GetId(),
			}))
			if err != nil {
				return nil, fmt.Errorf("ListScopedSigningKeys(account=%q): %w", acc.GetName(), err)
			}
			if state.scopedKeys[opName] == nil {
				state.scopedKeys[opName] = make(map[string]map[string]*nisv1.ScopedSigningKey)
			}
			state.scopedKeys[opName][acc.GetName()] = make(map[string]*nisv1.ScopedSigningKey)
			for _, sk := range skkResp.Msg.GetKeys() {
				state.scopedKeys[opName][acc.GetName()][sk.GetName()] = sk
			}

			// Fetch users for this account.
			userResp, err := c.UserClient().ListUsers(ctx, connect.NewRequest(&nisv1.ListUsersRequest{
				AccountId: acc.GetId(),
			}))
			if err != nil {
				return nil, fmt.Errorf("ListUsers(account=%q): %w", acc.GetName(), err)
			}
			if state.users[opName] == nil {
				state.users[opName] = make(map[string]map[string]*nisv1.User)
			}
			state.users[opName][acc.GetName()] = make(map[string]*nisv1.User)
			for _, u := range userResp.Msg.GetUsers() {
				state.users[opName][acc.GetName()][u.GetName()] = u
			}
		}

		// Fetch clusters for this operator. ListClusters accepts an OperatorId filter.
		clusterResp, err := c.ClusterClient().ListClusters(ctx, connect.NewRequest(&nisv1.ListClustersRequest{
			OperatorId: op.GetId(),
		}))
		if err != nil {
			return nil, fmt.Errorf("ListClusters(operator=%q): %w", opName, err)
		}
		state.clusters[opName] = make(map[string]*nisv1.Cluster)
		for _, cl := range clusterResp.Msg.GetClusters() {
			state.clusters[opName][cl.GetName()] = cl
		}
	}

	return state, nil
}

// classifyObject classifies a single manifest Object against server state.
// newAccounts tracks which accounts are new in this plan pass (for the
// default scoped-key special case).
func classifyObject(obj Object, state *serverState, newAccounts map[string]map[string]bool) (PlanItem, error) {
	switch obj.Kind {
	case KindOperator:
		return classifyOperator(obj, state)
	case KindCluster:
		return classifyCluster(obj, state)
	case KindAccount:
		return classifyAccount(obj, state)
	case KindScopedSigningKey:
		return classifyScopedSigningKey(obj, state, newAccounts)
	case KindUser:
		return classifyUser(obj, state)
	}
	// Validate already blocks unknown kinds; this is unreachable.
	return PlanItem{}, fmt.Errorf("manifest plan: unknown kind %q", obj.Kind)
}

func classifyOperator(obj Object, state *serverState) (PlanItem, error) {
	existing, ok := state.operators[obj.Metadata.Name]
	if !ok {
		return PlanItem{Object: obj, Action: ActionCreate}, nil
	}

	id, err := uuid.Parse(existing.GetId())
	if err != nil {
		return PlanItem{}, fmt.Errorf("manifest plan: operator %q: bad server ID: %w", obj.Metadata.Name, err)
	}

	spec := obj.Operator
	if spec == nil {
		spec = &OperatorSpec{}
	}

	var updates []UpdateOp

	// Check description drift.
	var descFields []string
	if existing.GetDescription() != spec.Description {
		descFields = append(descFields, "description")
	}
	if len(descFields) > 0 {
		updates = append(updates, UpdateOp{RPC: "UpdateOperator", Fields: descFields})
	}

	// nil JWTPolicy means "don't touch" — no drift considered.
	if spec.JWTPolicy != nil {
		var policyFields []string
		if time.Duration(existing.GetUserJwtTtlSeconds())*time.Second != spec.JWTPolicy.UserJWTTTL {
			policyFields = append(policyFields, "jwtPolicy.userJWTTTL")
		}
		if time.Duration(existing.GetAccountJwtTtlSeconds())*time.Second != spec.JWTPolicy.AccountJWTTTL {
			policyFields = append(policyFields, "jwtPolicy.accountJWTTTL")
		}
		if time.Duration(existing.GetJwtWarnWindowSeconds())*time.Second != spec.JWTPolicy.WarnWindow {
			policyFields = append(policyFields, "jwtPolicy.warnWindow")
		}
		if existing.GetJwtAutoRenew() != spec.JWTPolicy.AutoRenew {
			policyFields = append(policyFields, "jwtPolicy.autoRenew")
		}
		if len(policyFields) > 0 {
			updates = append(updates, UpdateOp{RPC: "SetJWTPolicy", Fields: policyFields})
		}
	}

	if len(updates) == 0 {
		return PlanItem{Object: obj, Action: ActionNoop, ExistingID: id}, nil
	}
	return PlanItem{Object: obj, Action: ActionUpdate, ExistingID: id, Updates: updates}, nil
}

func classifyCluster(obj Object, state *serverState) (PlanItem, error) {
	clusters := state.clusters[obj.Metadata.Operator]
	existing, ok := clusters[obj.Metadata.Name]
	if !ok {
		return PlanItem{Object: obj, Action: ActionCreate}, nil
	}

	id, err := uuid.Parse(existing.GetId())
	if err != nil {
		return PlanItem{}, fmt.Errorf("manifest plan: cluster %q: bad server ID: %w", obj.Metadata.Name, err)
	}

	// Cluster updates via manifest are not supported in v1.
	spec := obj.Cluster
	hasDrift := false
	if spec != nil {
		if existing.GetDescription() != spec.Description {
			hasDrift = true
		}
		if !sliceSetEqual(existing.GetServerUrls(), spec.ServerURLs) {
			hasDrift = true
		}
	}

	note := ""
	if hasDrift {
		note = "cluster updates via manifest are not supported in v1; use 'nisctl cluster update'"
	}
	return PlanItem{Object: obj, Action: ActionNoop, ExistingID: id, Note: note}, nil
}

func classifyAccount(obj Object, state *serverState) (PlanItem, error) {
	accounts := state.accounts[obj.Metadata.Operator]
	existing, ok := accounts[obj.Metadata.Name]
	if !ok {
		return PlanItem{Object: obj, Action: ActionCreate}, nil
	}

	id, err := uuid.Parse(existing.GetId())
	if err != nil {
		return PlanItem{}, fmt.Errorf("manifest plan: account %q: bad server ID: %w", obj.Metadata.Name, err)
	}

	spec := obj.Account
	if spec == nil {
		spec = &AccountSpec{}
	}

	var updates []UpdateOp

	if existing.GetDescription() != spec.Description {
		updates = append(updates, UpdateOp{RPC: "UpdateAccount", Fields: []string{"description"}})
	}

	// nil JetStream means "don't touch" — no drift considered.
	if spec.JetStream != nil {
		srv := existing.GetJetstreamLimits()
		var jsFields []string
		if srv.GetEnabled() != spec.JetStream.Enabled {
			jsFields = append(jsFields, "jetStream.enabled")
		}
		if srv.GetMaxMemory() != spec.JetStream.MaxMemory {
			jsFields = append(jsFields, "jetStream.maxMemory")
		}
		if srv.GetMaxStorage() != spec.JetStream.MaxStorage {
			jsFields = append(jsFields, "jetStream.maxStorage")
		}
		if int64(srv.GetMaxStreams()) != spec.JetStream.MaxStreams {
			jsFields = append(jsFields, "jetStream.maxStreams")
		}
		if int64(srv.GetMaxConsumers()) != spec.JetStream.MaxConsumers {
			jsFields = append(jsFields, "jetStream.maxConsumers")
		}
		if len(jsFields) > 0 {
			updates = append(updates, UpdateOp{RPC: "UpdateJetStreamLimits", Fields: jsFields})
		}
	}

	if len(updates) == 0 {
		return PlanItem{Object: obj, Action: ActionNoop, ExistingID: id}, nil
	}
	return PlanItem{Object: obj, Action: ActionUpdate, ExistingID: id, Updates: updates}, nil
}

func classifyScopedSigningKey(obj Object, state *serverState, newAccounts map[string]map[string]bool) (PlanItem, error) {
	opName := obj.Metadata.Operator
	accName := obj.Metadata.Account
	keyName := obj.Metadata.Name

	// Default scoped key on a brand-new account: server will auto-create it.
	// We can't CREATE it via API; plan it as Update with uuid.Nil so Chunk C
	// knows to fetch it after the parent account create.
	if keyName == "default" {
		if newAccounts[opName] != nil && newAccounts[opName][accName] {
			spec := skkSpecOrDefault(obj.ScopedSigningKey)
			updates := diffScopedSigningKeySpec(spec, defaultSKKBaseline())
			return PlanItem{
				Object:     obj,
				Action:     ActionUpdate,
				ExistingID: uuid.Nil,
				Updates:    updates,
				Note:       "auto-created by server on account create; will be updated after",
			}, nil
		}
	}

	existing := state.scopedKeys[opName][accName][keyName]
	if existing == nil {
		return PlanItem{Object: obj, Action: ActionCreate}, nil
	}

	id, err := uuid.Parse(existing.GetId())
	if err != nil {
		return PlanItem{}, fmt.Errorf("manifest plan: scoped key %q: bad server ID: %w", keyName, err)
	}

	spec := skkSpecOrDefault(obj.ScopedSigningKey)
	updates := diffScopedSigningKey(spec, existing)
	if len(updates) == 0 {
		return PlanItem{Object: obj, Action: ActionNoop, ExistingID: id}, nil
	}
	return PlanItem{Object: obj, Action: ActionUpdate, ExistingID: id, Updates: updates}, nil
}

func classifyUser(obj Object, state *serverState) (PlanItem, error) {
	opName := obj.Metadata.Operator
	accName := obj.Metadata.Account
	userName := obj.Metadata.Name

	existing := state.users[opName][accName][userName]
	if existing == nil {
		return PlanItem{Object: obj, Action: ActionCreate}, nil
	}

	id, err := uuid.Parse(existing.GetId())
	if err != nil {
		return PlanItem{}, fmt.Errorf("manifest plan: user %q: bad server ID: %w", userName, err)
	}

	spec := obj.User
	if spec == nil {
		spec = &UserSpec{}
	}

	// Scoped-key reassignment is not supported. Resolve both sides to name for
	// comparison: server gives us an ID; we look it up by name in the batch
	// (via spec.ScopedKey), and compare against the server key's ID indirectly
	// by checking the server-stored scoped_signing_key_id -> name mismatch.
	// We compare spec.ScopedKey (name) against the server key's name by
	// looking up the server key ID in the state.
	serverSKName := ""
	if existing.GetScopedSigningKeyId() != "" {
		// Walk the account's known keys to find the matching name.
		for name, sk := range state.scopedKeys[opName][accName] {
			if sk.GetId() == existing.GetScopedSigningKeyId() {
				serverSKName = name
				break
			}
		}
	}
	if spec.ScopedKey != serverSKName {
		return PlanItem{}, fmt.Errorf(
			"manifest: User %s/%s/%s: spec.scopedKey changed from %q (server) to %q (manifest); "+
				"reassignment requires delete+recreate which the planner refuses "+
				"(NKey + existing creds would be invalidated); "+
				"run nisctl user delete + nisctl user create to do this explicitly",
			opName, accName, userName, serverSKName, spec.ScopedKey,
		)
	}

	var updateFields []string
	if existing.GetDescription() != spec.Description {
		updateFields = append(updateFields, "description")
	}

	// nil JWTTTL means "don't touch".
	if spec.JWTTTL != nil {
		serverTTL := time.Duration(existing.GetJwtTtlSeconds()) * time.Second
		if serverTTL != *spec.JWTTTL {
			updateFields = append(updateFields, "jwtTTL")
		}
	}

	if len(updateFields) == 0 {
		return PlanItem{Object: obj, Action: ActionNoop, ExistingID: id}, nil
	}
	return PlanItem{Object: obj, Action: ActionUpdate, ExistingID: id, Updates: []UpdateOp{
		{RPC: "UpdateUser", Fields: updateFields},
	}}, nil
}

// skkSpecOrDefault returns the spec if non-nil, else an empty ScopedSigningKeySpec.
func skkSpecOrDefault(spec *ScopedSigningKeySpec) ScopedSigningKeySpec {
	if spec != nil {
		return *spec
	}
	return ScopedSigningKeySpec{}
}

// defaultSKKBaseline is the permissive default the server auto-creates.
func defaultSKKBaseline() *nisv1.ScopedSigningKey {
	return &nisv1.ScopedSigningKey{
		Permissions: &nisv1.UserPermissions{
			PubAllow: []string{">"},
			SubAllow: []string{">"},
		},
		ResponsePermission: &nisv1.ResponsePermission{},
	}
}

// diffScopedSigningKey computes UpdateOps between a manifest spec and server state.
func diffScopedSigningKey(spec ScopedSigningKeySpec, existing *nisv1.ScopedSigningKey) []UpdateOp {
	var descFields []string
	if existing.GetDescription() != spec.Description {
		descFields = append(descFields, "description")
	}
	var ops []UpdateOp
	if len(descFields) > 0 {
		ops = append(ops, UpdateOp{RPC: "UpdateScopedSigningKey", Fields: descFields})
	}

	srvPerms := existing.GetPermissions()
	srvResp := existing.GetResponsePermission()
	var permFields []string
	if !sliceSetEqual(srvPerms.GetPubAllow(), spec.PubAllow) {
		permFields = append(permFields, "pubAllow")
	}
	if !sliceSetEqual(srvPerms.GetPubDeny(), spec.PubDeny) {
		permFields = append(permFields, "pubDeny")
	}
	if !sliceSetEqual(srvPerms.GetSubAllow(), spec.SubAllow) {
		permFields = append(permFields, "subAllow")
	}
	if !sliceSetEqual(srvPerms.GetSubDeny(), spec.SubDeny) {
		permFields = append(permFields, "subDeny")
	}
	if int(srvResp.GetMaxMsgs()) != spec.ResponseMaxMsgs {
		permFields = append(permFields, "responseMaxMsgs")
	}
	// ResponsePermission.Expires is in nanoseconds.
	if time.Duration(srvResp.GetExpires()) != spec.ResponseTTL {
		permFields = append(permFields, "responseTTL")
	}
	if len(permFields) > 0 {
		ops = append(ops, UpdateOp{RPC: "UpdatePermissions", Fields: permFields})
	}

	return ops
}

// diffScopedSigningKeySpec computes UpdateOps between a manifest spec and a
// baseline server entity (used for the default-key-on-new-account case).
func diffScopedSigningKeySpec(spec ScopedSigningKeySpec, baseline *nisv1.ScopedSigningKey) []UpdateOp {
	return diffScopedSigningKey(spec, baseline)
}

// sliceSetEqual returns true when a and b contain the same strings
// regardless of order. Both nil and empty slice are treated as equivalent.
// Why order-insensitive: pubAllow=["a","b"] and ["b","a"] are semantically
// identical in NATS permission templates.
func sliceSetEqual(a, b []string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	if len(a) != len(b) {
		return false
	}
	ac := make([]string, len(a))
	bc := make([]string, len(b))
	copy(ac, a)
	copy(bc, b)
	sort.Strings(ac)
	sort.Strings(bc)
	for i := range ac {
		if ac[i] != bc[i] {
			return false
		}
	}
	return true
}
