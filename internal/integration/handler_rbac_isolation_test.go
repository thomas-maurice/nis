package integration

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	pb "github.com/thomas-maurice/nis/gen/nis/v1"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/infrastructure/authctx"
	"github.com/thomas-maurice/nis/internal/interfaces/grpc/handlers"
)

func strPtr(s string) *string { return &s }

// HandlerRBACIsolationTestSuite exercises the per-row authz checks that live in
// the handler layer (NOT just PermissionService). The existing
// RBACIsolationTestSuite tests PermissionService.Can* directly; this one
// constructs real handlers and calls them with an authctx-loaded ctx, so a
// handler that fails to invoke permService.Can* will leak — exactly the bug
// class A5 was filed to prevent.
//
// Reuses the setup from RBACIsolationTestSuite (same operators, accounts,
// API users) via composition.
type HandlerRBACIsolationTestSuite struct {
	RBACIsolationTestSuite
	accountHandler interface {
		UpdateAccount(context.Context, *connect.Request[pb.UpdateAccountRequest]) (*connect.Response[pb.UpdateAccountResponse], error)
		UpdateJetStreamLimits(context.Context, *connect.Request[pb.UpdateJetStreamLimitsRequest]) (*connect.Response[pb.UpdateJetStreamLimitsResponse], error)
		DeleteAccount(context.Context, *connect.Request[pb.DeleteAccountRequest]) (*connect.Response[pb.DeleteAccountResponse], error)
		PushAccountJWT(context.Context, *connect.Request[pb.PushAccountJWTRequest]) (*connect.Response[pb.PushAccountJWTResponse], error)
	}
}

func TestHandlerRBACIsolationTestSuite(t *testing.T) {
	suite.Run(t, new(HandlerRBACIsolationTestSuite))
}

func (s *HandlerRBACIsolationTestSuite) SetupTest() {
	s.RBACIsolationTestSuite.SetupTest()
	s.accountHandler = handlers.NewAccountHandler(s.accountService, s.permService)
}

func ctxAs(user *entities.APIUser) context.Context {
	return authctx.SetUser(context.Background(), user)
}

// TestAccountHandler_UpdateAccount_OperatorAdminCannotMutateOtherOperatorAccount
// is the load-bearing regression test for the v1 A5 leak: operator-admin A
// could call UpdateAccount with operator-admin B's account ID because the
// handler did not invoke permService.CanUpdateAccount.
func (s *HandlerRBACIsolationTestSuite) TestAccountHandler_UpdateAccount_OperatorAdminCannotMutateOtherOperatorAccount() {
	ctx := ctxAs(s.operator1Admin)
	_, err := s.accountHandler.UpdateAccount(ctx, connect.NewRequest(&pb.UpdateAccountRequest{
		Id:          s.operator2Account1.ID.String(),
		Name:        strPtr("renamed-by-attacker"),
		Description: strPtr("should never apply"),
	}))
	require.Error(s.T(), err)
	s.Equal(connect.CodePermissionDenied, connect.CodeOf(err),
		"operator-admin must not be able to update an account in another operator")
}

// TestAccountHandler_UpdateAccount_OperatorAdminCanMutateOwnAccount confirms the
// fix does not regress the happy path.
func (s *HandlerRBACIsolationTestSuite) TestAccountHandler_UpdateAccount_OperatorAdminCanMutateOwnAccount() {
	ctx := ctxAs(s.operator1Admin)
	resp, err := s.accountHandler.UpdateAccount(ctx, connect.NewRequest(&pb.UpdateAccountRequest{
		Id:          s.operator1Account1.ID.String(),
		Name:        strPtr(s.operator1Account1.Name),
		Description: strPtr("updated by owner"),
	}))
	s.NoError(err)
	s.NotNil(resp)
}

// TestAccountHandler_UpdateJetStreamLimits_OperatorAdminCannotMutateOtherOperatorAccount
// covers the same leak on the JetStream-limits path. This is also a
// resource-exhaustion vector (quota), not just a confidentiality leak.
func (s *HandlerRBACIsolationTestSuite) TestAccountHandler_UpdateJetStreamLimits_OperatorAdminCannotMutateOtherOperatorAccount() {
	ctx := ctxAs(s.operator1Admin)
	_, err := s.accountHandler.UpdateJetStreamLimits(ctx, connect.NewRequest(&pb.UpdateJetStreamLimitsRequest{
		Id: s.operator2Account1.ID.String(),
		Limits: &pb.JetStreamLimits{
			Enabled:      true,
			MaxMemory:    1 << 30,
			MaxStorage:   1 << 40,
			MaxStreams:   1000,
			MaxConsumers: 10000,
		},
	}))
	require.Error(s.T(), err)
	s.Equal(connect.CodePermissionDenied, connect.CodeOf(err),
		"operator-admin must not be able to set JetStream quota on another operator's account")
}

// TestAccountHandler_DeleteAccount_OperatorAdminCannotDelete pins the
// defense-in-depth check. Casbin already denies operator-admin on
// account.delete, but the handler now also calls CanDeleteAccount so a
// future Casbin policy edit cannot silently unlock cross-tenant deletion.
func (s *HandlerRBACIsolationTestSuite) TestAccountHandler_DeleteAccount_OperatorAdminCannotDelete() {
	ctx := ctxAs(s.operator1Admin)
	_, err := s.accountHandler.DeleteAccount(ctx, connect.NewRequest(&pb.DeleteAccountRequest{
		Id: s.operator1Account1.ID.String(),
	}))
	require.Error(s.T(), err)
	s.Equal(connect.CodePermissionDenied, connect.CodeOf(err),
		"only admin can DeleteAccount; operator-admin must be refused at the handler even though Casbin also denies")
}

// TestAccountHandler_DeleteAccount_AccountAdminCannotDelete pins the same
// defense-in-depth for account-admin.
func (s *HandlerRBACIsolationTestSuite) TestAccountHandler_DeleteAccount_AccountAdminCannotDelete() {
	ctx := ctxAs(s.account1Admin)
	_, err := s.accountHandler.DeleteAccount(ctx, connect.NewRequest(&pb.DeleteAccountRequest{
		Id: s.operator1Account1.ID.String(),
	}))
	require.Error(s.T(), err)
	s.Equal(connect.CodePermissionDenied, connect.CodeOf(err))
}

// TestAccountHandler_PushAccountJWT_OperatorAdminCannotPushOther covers the
// stub endpoint. Even though the stub returns Unimplemented after the auth
// check, the auth check must run first — otherwise a future implementer
// inherits the leak.
func (s *HandlerRBACIsolationTestSuite) TestAccountHandler_PushAccountJWT_OperatorAdminCannotPushOther() {
	ctx := ctxAs(s.operator1Admin)
	_, err := s.accountHandler.PushAccountJWT(ctx, connect.NewRequest(&pb.PushAccountJWTRequest{
		Id: s.operator2Account1.ID.String(),
	}))
	require.Error(s.T(), err)
	s.Equal(connect.CodePermissionDenied, connect.CodeOf(err),
		"cross-operator account JWT push must be denied at the handler, not at Unimplemented")
}

// TestAccountHandler_UpdateAccount_Unauthenticated covers the missing-bearer
// case at the handler layer (the middleware would normally reject, but the
// handler must still refuse on an unloaded ctx — defense in depth).
func (s *HandlerRBACIsolationTestSuite) TestAccountHandler_UpdateAccount_Unauthenticated() {
	_, err := s.accountHandler.UpdateAccount(context.Background(), connect.NewRequest(&pb.UpdateAccountRequest{
		Id: s.operator1Account1.ID.String(),
	}))
	require.Error(s.T(), err)
	s.Equal(connect.CodeUnauthenticated, connect.CodeOf(err))
}

// TestAccountHandler_UpdateAccount_AdminCanMutateAnyAccount confirms admin
// bypass continues to work (was the only path that previously worked at all).
func (s *HandlerRBACIsolationTestSuite) TestAccountHandler_UpdateAccount_AdminCanMutateAnyAccount() {
	ctx := ctxAs(s.adminUser)
	resp, err := s.accountHandler.UpdateAccount(ctx, connect.NewRequest(&pb.UpdateAccountRequest{
		Id:          s.operator2Account1.ID.String(),
		Name:        strPtr(s.operator2Account1.Name),
		Description: strPtr("admin can do anything"),
	}))
	s.NoError(err)
	s.NotNil(resp)
}
