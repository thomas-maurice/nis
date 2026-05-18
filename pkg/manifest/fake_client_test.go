package manifest

import (
	"context"
	"fmt"

	"connectrpc.com/connect"
	nisv1 "github.com/thomas-maurice/nis/gen/nis/v1"
	"github.com/thomas-maurice/nis/gen/nis/v1/nisv1connect"
)

// fakePlannerClient implements PlannerClient with in-memory state.
type fakePlannerClient struct {
	operators  map[string]*nisv1.Operator           // by name
	accounts   map[string][]*nisv1.Account          // by operatorID
	clusters   map[string][]*nisv1.Cluster          // by operatorID
	scopedKeys map[string][]*nisv1.ScopedSigningKey // by accountID
	users      map[string][]*nisv1.User             // by accountID

	// callLog records each RPC name dispatched, for assertion in tests.
	callLog []string
}

func (f *fakePlannerClient) recordCall(name string) {
	f.callLog = append(f.callLog, name)
}

// nextID generates a unique valid UUID for use in tests.
var nextIDCounter int

func nextID() string {
	nextIDCounter++
	// Format as a valid UUID: 00000000-0000-4000-8000-XXXXXXXXXXXX
	return fmt.Sprintf("00000000-0000-4000-8000-%012x", nextIDCounter)
}

// findOperatorByName returns nil if not found.
func (f *fakePlannerClient) findOperatorByName(name string) *nisv1.Operator {
	return f.operators[name]
}

func newFakePlannerClient() *fakePlannerClient {
	return &fakePlannerClient{
		operators:  make(map[string]*nisv1.Operator),
		accounts:   make(map[string][]*nisv1.Account),
		clusters:   make(map[string][]*nisv1.Cluster),
		scopedKeys: make(map[string][]*nisv1.ScopedSigningKey),
		users:      make(map[string][]*nisv1.User),
	}
}

func (f *fakePlannerClient) addOperator(op *nisv1.Operator) {
	f.operators[op.GetName()] = op
}

func (f *fakePlannerClient) addAccount(acc *nisv1.Account) {
	f.accounts[acc.GetOperatorId()] = append(f.accounts[acc.GetOperatorId()], acc)
}

func (f *fakePlannerClient) addCluster(cl *nisv1.Cluster) {
	f.clusters[cl.GetOperatorId()] = append(f.clusters[cl.GetOperatorId()], cl)
}

func (f *fakePlannerClient) addScopedKey(sk *nisv1.ScopedSigningKey) {
	f.scopedKeys[sk.GetAccountId()] = append(f.scopedKeys[sk.GetAccountId()], sk)
}

func (f *fakePlannerClient) addUser(u *nisv1.User) {
	f.users[u.GetAccountId()] = append(f.users[u.GetAccountId()], u)
}

func (f *fakePlannerClient) OperatorClient() nisv1connect.OperatorServiceClient {
	return &fakeOperatorClient{f}
}
func (f *fakePlannerClient) AccountClient() nisv1connect.AccountServiceClient {
	return &fakeAccountClient{f}
}
func (f *fakePlannerClient) UserClient() nisv1connect.UserServiceClient {
	return &fakeUserClient{f}
}
func (f *fakePlannerClient) ScopedSigningKeyClient() nisv1connect.ScopedSigningKeyServiceClient {
	return &fakeSKKClient{f}
}
func (f *fakePlannerClient) ClusterClient() nisv1connect.ClusterServiceClient {
	return &fakeClusterClient{f}
}

// ---- operator client ----

type fakeOperatorClient struct{ f *fakePlannerClient }

func (c *fakeOperatorClient) CreateOperator(_ context.Context, req *connect.Request[nisv1.CreateOperatorRequest]) (*connect.Response[nisv1.CreateOperatorResponse], error) {
	c.f.recordCall("CreateOperator")
	op := &nisv1.Operator{
		Id:          nextID(),
		Name:        req.Msg.GetName(),
		Description: req.Msg.GetDescription(),
	}
	c.f.operators[op.GetName()] = op
	return connect.NewResponse(&nisv1.CreateOperatorResponse{Operator: op}), nil
}
func (c *fakeOperatorClient) GetOperator(_ context.Context, req *connect.Request[nisv1.GetOperatorRequest]) (*connect.Response[nisv1.GetOperatorResponse], error) {
	for _, op := range c.f.operators {
		if op.GetId() == req.Msg.GetId() {
			return connect.NewResponse(&nisv1.GetOperatorResponse{Operator: op}), nil
		}
	}
	return nil, connect.NewError(connect.CodeNotFound, nil)
}
func (c *fakeOperatorClient) GetOperatorByName(_ context.Context, req *connect.Request[nisv1.GetOperatorByNameRequest]) (*connect.Response[nisv1.GetOperatorByNameResponse], error) {
	op, ok := c.f.operators[req.Msg.GetName()]
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, nil)
	}
	return connect.NewResponse(&nisv1.GetOperatorByNameResponse{Operator: op}), nil
}
func (c *fakeOperatorClient) ListOperators(_ context.Context, _ *connect.Request[nisv1.ListOperatorsRequest]) (*connect.Response[nisv1.ListOperatorsResponse], error) {
	var ops []*nisv1.Operator
	for _, op := range c.f.operators {
		ops = append(ops, op)
	}
	return connect.NewResponse(&nisv1.ListOperatorsResponse{Operators: ops}), nil
}
func (c *fakeOperatorClient) UpdateOperator(_ context.Context, req *connect.Request[nisv1.UpdateOperatorRequest]) (*connect.Response[nisv1.UpdateOperatorResponse], error) {
	c.f.recordCall("UpdateOperator")
	for _, op := range c.f.operators {
		if op.GetId() == req.Msg.GetId() {
			if req.Msg.Description != nil {
				op.Description = req.Msg.GetDescription()
			}
			return connect.NewResponse(&nisv1.UpdateOperatorResponse{Operator: op}), nil
		}
	}
	return nil, connect.NewError(connect.CodeNotFound, nil)
}
func (c *fakeOperatorClient) SetSystemAccount(_ context.Context, _ *connect.Request[nisv1.SetSystemAccountRequest]) (*connect.Response[nisv1.SetSystemAccountResponse], error) {
	panic("not used in apply/delete")
}
func (c *fakeOperatorClient) DeleteOperator(_ context.Context, req *connect.Request[nisv1.DeleteOperatorRequest]) (*connect.Response[nisv1.DeleteOperatorResponse], error) {
	c.f.recordCall("DeleteOperator")
	for name, op := range c.f.operators {
		if op.GetId() == req.Msg.GetId() {
			delete(c.f.operators, name)
			return connect.NewResponse(&nisv1.DeleteOperatorResponse{}), nil
		}
	}
	return nil, connect.NewError(connect.CodeNotFound, nil)
}
func (c *fakeOperatorClient) GenerateInclude(_ context.Context, _ *connect.Request[nisv1.GenerateIncludeRequest]) (*connect.Response[nisv1.GenerateIncludeResponse], error) {
	panic("not used in apply/delete")
}
func (c *fakeOperatorClient) SetJWTPolicy(_ context.Context, req *connect.Request[nisv1.SetJWTPolicyRequest]) (*connect.Response[nisv1.SetJWTPolicyResponse], error) {
	c.f.recordCall("SetJWTPolicy")
	for _, op := range c.f.operators {
		if op.GetId() == req.Msg.GetId() {
			if req.Msg.UserJwtTtlSeconds != nil {
				op.UserJwtTtlSeconds = req.Msg.GetUserJwtTtlSeconds()
			}
			if req.Msg.AccountJwtTtlSeconds != nil {
				op.AccountJwtTtlSeconds = req.Msg.GetAccountJwtTtlSeconds()
			}
			if req.Msg.JwtWarnWindowSeconds != nil {
				op.JwtWarnWindowSeconds = req.Msg.GetJwtWarnWindowSeconds()
			}
			if req.Msg.JwtAutoRenew != nil {
				op.JwtAutoRenew = req.Msg.GetJwtAutoRenew()
			}
			return connect.NewResponse(&nisv1.SetJWTPolicyResponse{Operator: op}), nil
		}
	}
	return nil, connect.NewError(connect.CodeNotFound, nil)
}
func (c *fakeOperatorClient) RunJWTExpirySweep(_ context.Context, _ *connect.Request[nisv1.RunJWTExpirySweepRequest]) (*connect.Response[nisv1.RunJWTExpirySweepResponse], error) {
	panic("not used in apply/delete")
}

// ---- account client ----

type fakeAccountClient struct{ f *fakePlannerClient }

func (c *fakeAccountClient) CreateAccount(_ context.Context, req *connect.Request[nisv1.CreateAccountRequest]) (*connect.Response[nisv1.CreateAccountResponse], error) {
	c.f.recordCall("CreateAccount")
	acc := &nisv1.Account{
		Id:              nextID(),
		OperatorId:      req.Msg.GetOperatorId(),
		Name:            req.Msg.GetName(),
		Description:     req.Msg.GetDescription(),
		JetstreamLimits: req.Msg.GetJetstreamLimits(),
	}
	c.f.accounts[acc.GetOperatorId()] = append(c.f.accounts[acc.GetOperatorId()], acc)
	// Auto-create "default" scoped signing key (mirrors server behaviour).
	defaultKey := &nisv1.ScopedSigningKey{
		Id:        nextID(),
		AccountId: acc.GetId(),
		Name:      "default",
		Permissions: &nisv1.UserPermissions{
			PubAllow: []string{">"},
			SubAllow: []string{">"},
		},
		ResponsePermission: &nisv1.ResponsePermission{},
	}
	c.f.scopedKeys[acc.GetId()] = append(c.f.scopedKeys[acc.GetId()], defaultKey)
	return connect.NewResponse(&nisv1.CreateAccountResponse{Account: acc}), nil
}
func (c *fakeAccountClient) GetAccount(_ context.Context, req *connect.Request[nisv1.GetAccountRequest]) (*connect.Response[nisv1.GetAccountResponse], error) {
	for _, accs := range c.f.accounts {
		for _, a := range accs {
			if a.GetId() == req.Msg.GetId() {
				return connect.NewResponse(&nisv1.GetAccountResponse{Account: a}), nil
			}
		}
	}
	return nil, connect.NewError(connect.CodeNotFound, nil)
}
func (c *fakeAccountClient) GetAccountByName(_ context.Context, req *connect.Request[nisv1.GetAccountByNameRequest]) (*connect.Response[nisv1.GetAccountByNameResponse], error) {
	for _, a := range c.f.accounts[req.Msg.GetOperatorId()] {
		if a.GetName() == req.Msg.GetName() {
			return connect.NewResponse(&nisv1.GetAccountByNameResponse{Account: a}), nil
		}
	}
	return nil, connect.NewError(connect.CodeNotFound, nil)
}
func (c *fakeAccountClient) ListAccounts(_ context.Context, req *connect.Request[nisv1.ListAccountsRequest]) (*connect.Response[nisv1.ListAccountsResponse], error) {
	accs := c.f.accounts[req.Msg.GetOperatorId()]
	return connect.NewResponse(&nisv1.ListAccountsResponse{Accounts: accs}), nil
}
func (c *fakeAccountClient) UpdateAccount(_ context.Context, req *connect.Request[nisv1.UpdateAccountRequest]) (*connect.Response[nisv1.UpdateAccountResponse], error) {
	c.f.recordCall("UpdateAccount")
	for _, accs := range c.f.accounts {
		for _, a := range accs {
			if a.GetId() == req.Msg.GetId() {
				if req.Msg.Description != nil {
					a.Description = req.Msg.GetDescription()
				}
				return connect.NewResponse(&nisv1.UpdateAccountResponse{Account: a}), nil
			}
		}
	}
	return nil, connect.NewError(connect.CodeNotFound, nil)
}
func (c *fakeAccountClient) UpdateJetStreamLimits(_ context.Context, req *connect.Request[nisv1.UpdateJetStreamLimitsRequest]) (*connect.Response[nisv1.UpdateJetStreamLimitsResponse], error) {
	c.f.recordCall("UpdateJetStreamLimits")
	for _, accs := range c.f.accounts {
		for _, a := range accs {
			if a.GetId() == req.Msg.GetId() {
				a.JetstreamLimits = req.Msg.GetLimits()
				return connect.NewResponse(&nisv1.UpdateJetStreamLimitsResponse{Account: a}), nil
			}
		}
	}
	return nil, connect.NewError(connect.CodeNotFound, nil)
}
func (c *fakeAccountClient) DeleteAccount(_ context.Context, req *connect.Request[nisv1.DeleteAccountRequest]) (*connect.Response[nisv1.DeleteAccountResponse], error) {
	c.f.recordCall("DeleteAccount")
	for opID, accs := range c.f.accounts {
		for i, a := range accs {
			if a.GetId() == req.Msg.GetId() {
				c.f.accounts[opID] = append(accs[:i], accs[i+1:]...)
				return connect.NewResponse(&nisv1.DeleteAccountResponse{}), nil
			}
		}
	}
	return nil, connect.NewError(connect.CodeNotFound, nil)
}
func (c *fakeAccountClient) PushAccountJWT(_ context.Context, _ *connect.Request[nisv1.PushAccountJWTRequest]) (*connect.Response[nisv1.PushAccountJWTResponse], error) {
	panic("not used in apply/delete")
}

// ---- user client ----

type fakeUserClient struct{ f *fakePlannerClient }

func (c *fakeUserClient) CreateUser(_ context.Context, req *connect.Request[nisv1.CreateUserRequest]) (*connect.Response[nisv1.CreateUserResponse], error) {
	c.f.recordCall("CreateUser")
	u := &nisv1.User{
		Id:                 req.Msg.GetAccountId() + "_" + req.Msg.GetName() + "_" + nextID(),
		AccountId:          req.Msg.GetAccountId(),
		Name:               req.Msg.GetName(),
		Description:        req.Msg.GetDescription(),
		ScopedSigningKeyId: req.Msg.GetScopedSigningKeyId(),
	}
	c.f.users[u.GetAccountId()] = append(c.f.users[u.GetAccountId()], u)
	return connect.NewResponse(&nisv1.CreateUserResponse{User: u}), nil
}
func (c *fakeUserClient) GetUser(_ context.Context, req *connect.Request[nisv1.GetUserRequest]) (*connect.Response[nisv1.GetUserResponse], error) {
	for _, users := range c.f.users {
		for _, u := range users {
			if u.GetId() == req.Msg.GetId() {
				return connect.NewResponse(&nisv1.GetUserResponse{User: u}), nil
			}
		}
	}
	return nil, connect.NewError(connect.CodeNotFound, nil)
}
func (c *fakeUserClient) GetUserByName(_ context.Context, req *connect.Request[nisv1.GetUserByNameRequest]) (*connect.Response[nisv1.GetUserByNameResponse], error) {
	for _, u := range c.f.users[req.Msg.GetAccountId()] {
		if u.GetName() == req.Msg.GetName() {
			return connect.NewResponse(&nisv1.GetUserByNameResponse{User: u}), nil
		}
	}
	return nil, connect.NewError(connect.CodeNotFound, nil)
}
func (c *fakeUserClient) ListUsers(_ context.Context, req *connect.Request[nisv1.ListUsersRequest]) (*connect.Response[nisv1.ListUsersResponse], error) {
	users := c.f.users[req.Msg.GetAccountId()]
	return connect.NewResponse(&nisv1.ListUsersResponse{Users: users}), nil
}
func (c *fakeUserClient) UpdateUser(_ context.Context, req *connect.Request[nisv1.UpdateUserRequest]) (*connect.Response[nisv1.UpdateUserResponse], error) {
	c.f.recordCall("UpdateUser")
	for _, users := range c.f.users {
		for _, u := range users {
			if u.GetId() == req.Msg.GetId() {
				if req.Msg.Description != nil {
					u.Description = req.Msg.GetDescription()
				}
				if req.Msg.GetClearJwtTtl() {
					u.JwtTtlSeconds = nil
				} else if req.Msg.JwtTtlSeconds != nil {
					v := req.Msg.GetJwtTtlSeconds()
					u.JwtTtlSeconds = &v
				}
				return connect.NewResponse(&nisv1.UpdateUserResponse{User: u}), nil
			}
		}
	}
	return nil, connect.NewError(connect.CodeNotFound, nil)
}
func (c *fakeUserClient) DeleteUser(_ context.Context, req *connect.Request[nisv1.DeleteUserRequest]) (*connect.Response[nisv1.DeleteUserResponse], error) {
	c.f.recordCall("DeleteUser")
	for accID, users := range c.f.users {
		for i, u := range users {
			if u.GetId() == req.Msg.GetId() {
				c.f.users[accID] = append(users[:i], users[i+1:]...)
				return connect.NewResponse(&nisv1.DeleteUserResponse{}), nil
			}
		}
	}
	return nil, connect.NewError(connect.CodeNotFound, nil)
}
func (c *fakeUserClient) GetUserCredentials(_ context.Context, _ *connect.Request[nisv1.GetUserCredentialsRequest]) (*connect.Response[nisv1.GetUserCredentialsResponse], error) {
	panic("not used in apply/delete")
}
func (c *fakeUserClient) RevokeUser(_ context.Context, _ *connect.Request[nisv1.RevokeUserRequest]) (*connect.Response[nisv1.RevokeUserResponse], error) {
	panic("not used in apply/delete")
}
func (c *fakeUserClient) RegenerateUserCredentials(_ context.Context, _ *connect.Request[nisv1.RegenerateUserCredentialsRequest]) (*connect.Response[nisv1.RegenerateUserCredentialsResponse], error) {
	panic("not used in apply/delete")
}

// ---- scoped signing key client ----

type fakeSKKClient struct{ f *fakePlannerClient }

func (c *fakeSKKClient) CreateScopedSigningKey(_ context.Context, req *connect.Request[nisv1.CreateScopedSigningKeyRequest]) (*connect.Response[nisv1.CreateScopedSigningKeyResponse], error) {
	c.f.recordCall("CreateScopedSigningKey")
	sk := &nisv1.ScopedSigningKey{
		Id:                 nextID(),
		AccountId:          req.Msg.GetAccountId(),
		Name:               req.Msg.GetName(),
		Description:        req.Msg.GetDescription(),
		Permissions:        req.Msg.GetPermissions(),
		ResponsePermission: req.Msg.GetResponsePermission(),
	}
	c.f.scopedKeys[sk.GetAccountId()] = append(c.f.scopedKeys[sk.GetAccountId()], sk)
	return connect.NewResponse(&nisv1.CreateScopedSigningKeyResponse{Key: sk}), nil
}
func (c *fakeSKKClient) GetScopedSigningKey(_ context.Context, req *connect.Request[nisv1.GetScopedSigningKeyRequest]) (*connect.Response[nisv1.GetScopedSigningKeyResponse], error) {
	for _, keys := range c.f.scopedKeys {
		for _, sk := range keys {
			if sk.GetId() == req.Msg.GetId() {
				return connect.NewResponse(&nisv1.GetScopedSigningKeyResponse{Key: sk}), nil
			}
		}
	}
	return nil, connect.NewError(connect.CodeNotFound, nil)
}
func (c *fakeSKKClient) GetScopedSigningKeyByName(_ context.Context, req *connect.Request[nisv1.GetScopedSigningKeyByNameRequest]) (*connect.Response[nisv1.GetScopedSigningKeyByNameResponse], error) {
	for _, sk := range c.f.scopedKeys[req.Msg.GetAccountId()] {
		if sk.GetName() == req.Msg.GetName() {
			return connect.NewResponse(&nisv1.GetScopedSigningKeyByNameResponse{Key: sk}), nil
		}
	}
	return nil, connect.NewError(connect.CodeNotFound, nil)
}
func (c *fakeSKKClient) ListScopedSigningKeys(_ context.Context, req *connect.Request[nisv1.ListScopedSigningKeysRequest]) (*connect.Response[nisv1.ListScopedSigningKeysResponse], error) {
	keys := c.f.scopedKeys[req.Msg.GetAccountId()]
	return connect.NewResponse(&nisv1.ListScopedSigningKeysResponse{Keys: keys}), nil
}
func (c *fakeSKKClient) UpdateScopedSigningKey(_ context.Context, req *connect.Request[nisv1.UpdateScopedSigningKeyRequest]) (*connect.Response[nisv1.UpdateScopedSigningKeyResponse], error) {
	c.f.recordCall("UpdateScopedSigningKey")
	for _, keys := range c.f.scopedKeys {
		for _, sk := range keys {
			if sk.GetId() == req.Msg.GetId() {
				if req.Msg.Description != nil {
					sk.Description = req.Msg.GetDescription()
				}
				return connect.NewResponse(&nisv1.UpdateScopedSigningKeyResponse{Key: sk}), nil
			}
		}
	}
	return nil, connect.NewError(connect.CodeNotFound, nil)
}
func (c *fakeSKKClient) UpdatePermissions(_ context.Context, req *connect.Request[nisv1.UpdatePermissionsRequest]) (*connect.Response[nisv1.UpdatePermissionsResponse], error) {
	c.f.recordCall("UpdatePermissions")
	for _, keys := range c.f.scopedKeys {
		for _, sk := range keys {
			if sk.GetId() == req.Msg.GetId() {
				sk.Permissions = req.Msg.GetPermissions()
				sk.ResponsePermission = req.Msg.GetResponsePermission()
				return connect.NewResponse(&nisv1.UpdatePermissionsResponse{Key: sk}), nil
			}
		}
	}
	return nil, connect.NewError(connect.CodeNotFound, nil)
}
func (c *fakeSKKClient) DeleteScopedSigningKey(_ context.Context, req *connect.Request[nisv1.DeleteScopedSigningKeyRequest]) (*connect.Response[nisv1.DeleteScopedSigningKeyResponse], error) {
	c.f.recordCall("DeleteScopedSigningKey")
	for accID, keys := range c.f.scopedKeys {
		for i, sk := range keys {
			if sk.GetId() == req.Msg.GetId() {
				c.f.scopedKeys[accID] = append(keys[:i], keys[i+1:]...)
				return connect.NewResponse(&nisv1.DeleteScopedSigningKeyResponse{}), nil
			}
		}
	}
	return nil, connect.NewError(connect.CodeNotFound, nil)
}

// ---- cluster client ----

type fakeClusterClient struct{ f *fakePlannerClient }

func (c *fakeClusterClient) CreateCluster(_ context.Context, req *connect.Request[nisv1.CreateClusterRequest]) (*connect.Response[nisv1.CreateClusterResponse], error) {
	c.f.recordCall("CreateCluster")
	cl := &nisv1.Cluster{
		Id:          nextID(),
		OperatorId:  req.Msg.GetOperatorId(),
		Name:        req.Msg.GetName(),
		Description: req.Msg.GetDescription(),
		ServerUrls:  req.Msg.GetServerUrls(),
	}
	c.f.clusters[cl.GetOperatorId()] = append(c.f.clusters[cl.GetOperatorId()], cl)
	return connect.NewResponse(&nisv1.CreateClusterResponse{Cluster: cl}), nil
}
func (c *fakeClusterClient) GetCluster(_ context.Context, req *connect.Request[nisv1.GetClusterRequest]) (*connect.Response[nisv1.GetClusterResponse], error) {
	for _, cls := range c.f.clusters {
		for _, cl := range cls {
			if cl.GetId() == req.Msg.GetId() {
				return connect.NewResponse(&nisv1.GetClusterResponse{Cluster: cl}), nil
			}
		}
	}
	return nil, connect.NewError(connect.CodeNotFound, nil)
}
func (c *fakeClusterClient) GetClusterByName(_ context.Context, req *connect.Request[nisv1.GetClusterByNameRequest]) (*connect.Response[nisv1.GetClusterByNameResponse], error) {
	for _, cl := range c.f.clusters[req.Msg.GetOperatorId()] {
		if cl.GetName() == req.Msg.GetName() {
			return connect.NewResponse(&nisv1.GetClusterByNameResponse{Cluster: cl}), nil
		}
	}
	return nil, connect.NewError(connect.CodeNotFound, nil)
}
func (c *fakeClusterClient) ListClusters(_ context.Context, req *connect.Request[nisv1.ListClustersRequest]) (*connect.Response[nisv1.ListClustersResponse], error) {
	clusters := c.f.clusters[req.Msg.GetOperatorId()]
	return connect.NewResponse(&nisv1.ListClustersResponse{Clusters: clusters}), nil
}
func (c *fakeClusterClient) UpdateCluster(_ context.Context, _ *connect.Request[nisv1.UpdateClusterRequest]) (*connect.Response[nisv1.UpdateClusterResponse], error) {
	panic("not used in apply/delete")
}
func (c *fakeClusterClient) UpdateClusterCredentials(_ context.Context, _ *connect.Request[nisv1.UpdateClusterCredentialsRequest]) (*connect.Response[nisv1.UpdateClusterCredentialsResponse], error) {
	panic("not used in apply/delete")
}
func (c *fakeClusterClient) DeleteCluster(_ context.Context, req *connect.Request[nisv1.DeleteClusterRequest]) (*connect.Response[nisv1.DeleteClusterResponse], error) {
	c.f.recordCall("DeleteCluster")
	for opID, cls := range c.f.clusters {
		for i, cl := range cls {
			if cl.GetId() == req.Msg.GetId() {
				c.f.clusters[opID] = append(cls[:i], cls[i+1:]...)
				return connect.NewResponse(&nisv1.DeleteClusterResponse{}), nil
			}
		}
	}
	return nil, connect.NewError(connect.CodeNotFound, nil)
}
func (c *fakeClusterClient) GetClusterCredentials(_ context.Context, _ *connect.Request[nisv1.GetClusterCredentialsRequest]) (*connect.Response[nisv1.GetClusterCredentialsResponse], error) {
	panic("not used in apply/delete")
}
func (c *fakeClusterClient) GenerateServerConfig(_ context.Context, _ *connect.Request[nisv1.GenerateServerConfigRequest]) (*connect.Response[nisv1.GenerateServerConfigResponse], error) {
	panic("not used in apply/delete")
}
func (c *fakeClusterClient) SyncCluster(_ context.Context, _ *connect.Request[nisv1.SyncClusterRequest]) (*connect.Response[nisv1.SyncClusterResponse], error) {
	panic("not used in apply/delete")
}
func (c *fakeClusterClient) ListResolverAccounts(_ context.Context, _ *connect.Request[nisv1.ListResolverAccountsRequest]) (*connect.Response[nisv1.ListResolverAccountsResponse], error) {
	panic("not used in apply/delete")
}
func (c *fakeClusterClient) DeleteResolverAccount(_ context.Context, _ *connect.Request[nisv1.DeleteResolverAccountRequest]) (*connect.Response[nisv1.DeleteResolverAccountResponse], error) {
	panic("not used in apply/delete")
}
