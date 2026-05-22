package authz

import (
	"testing"

	"github.com/google/uuid"

	"github.com/thomas-maurice/nis/internal/domain/entities"
)

func TestSystemScope(t *testing.T) {
	s := SystemScope()
	if !s.IsSystem() {
		t.Error("SystemScope() should be IsSystem")
	}
	if !s.IsAdmin() {
		t.Error("SystemScope() should be IsAdmin (admin-equivalent visibility)")
	}
	if s.IsOperatorAdmin() || s.IsAccountAdmin() {
		t.Error("SystemScope() must not be operator/account admin")
	}
	if s.IsZero() {
		t.Error("SystemScope() must not be IsZero")
	}
}

func TestScopeFromAPIUser_Admin(t *testing.T) {
	u := &entities.APIUser{ID: uuid.New(), Role: entities.RoleAdmin}
	s := ScopeFromAPIUser(u)
	if !s.IsAdmin() {
		t.Error("admin should be IsAdmin")
	}
	if s.IsSystem() {
		t.Error("admin should NOT be IsSystem (distinct synthetic role)")
	}
	if s.CallerUserID != u.ID {
		t.Error("CallerUserID not propagated")
	}
}

func TestScopeFromAPIUser_OperatorAdmin(t *testing.T) {
	opID := uuid.New()
	u := &entities.APIUser{ID: uuid.New(), Role: entities.RoleOperatorAdmin, OperatorID: &opID}
	s := ScopeFromAPIUser(u)
	if !s.IsOperatorAdmin() {
		t.Error("operator-admin should be IsOperatorAdmin")
	}
	if s.IsAdmin() {
		t.Error("operator-admin must NOT be IsAdmin")
	}
	if s.ScopeOperatorID == nil || *s.ScopeOperatorID != opID {
		t.Errorf("ScopeOperatorID not propagated: %v", s.ScopeOperatorID)
	}
}

func TestScopeFromAPIUser_AccountAdmin(t *testing.T) {
	accID := uuid.New()
	u := &entities.APIUser{ID: uuid.New(), Role: entities.RoleAccountAdmin, AccountID: &accID}
	s := ScopeFromAPIUser(u)
	if !s.IsAccountAdmin() {
		t.Error("account-admin should be IsAccountAdmin")
	}
	if s.IsAdmin() {
		t.Error("account-admin must NOT be IsAdmin")
	}
	if s.ScopeAccountID == nil || *s.ScopeAccountID != accID {
		t.Errorf("ScopeAccountID not propagated: %v", s.ScopeAccountID)
	}
}

func TestScopeFromAPIUser_Nil(t *testing.T) {
	s := ScopeFromAPIUser(nil)
	if !s.IsZero() {
		t.Errorf("nil APIUser must yield zero scope, got Role=%q", s.Role)
	}
	if s.IsAdmin() || s.IsSystem() || s.IsOperatorAdmin() || s.IsAccountAdmin() {
		t.Errorf("zero scope must have no privileges, got %+v", s)
	}
}

func TestScopeFromAPIUser_UnknownRole(t *testing.T) {
	// An APIUser carrying a role string that no current branch handles must
	// fall through to the zero scope (= no rows), not to an admin scope.
	u := &entities.APIUser{ID: uuid.New(), Role: entities.APIUserRole("future-role")}
	s := ScopeFromAPIUser(u)
	if !s.IsZero() {
		t.Errorf("unknown role must yield zero scope, got Role=%q", s.Role)
	}
}

func TestZeroScope_DeniesEverything(t *testing.T) {
	var s Scope
	if s.IsAdmin() || s.IsSystem() || s.IsOperatorAdmin() || s.IsAccountAdmin() {
		t.Errorf("zero Scope must deny everything, got %+v", s)
	}
	if !s.IsZero() {
		t.Error("var s Scope must be IsZero")
	}
}
