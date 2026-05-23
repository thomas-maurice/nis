package handlers

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	nisv1connect "github.com/thomas-maurice/nis/gen/nis/v1/nisv1connect"
	"github.com/thomas-maurice/nis/internal/application/authz"
)

// TestHandlerAuthzLint is the permanent guardrail for the bug class A5 was
// filed to prevent: a handler method that calls a service mutation without
// invoking permService.Can* (or an equivalent explicit role gate). The lint
// is structural — adding a new RPC procedure to a *_handler.go file without
// classifying it in authz.Procedures fails the build, forcing a deliberate
// authz call.
//
// Four valid kinds (defined in internal/application/authz/registry.go):
//
//   - KindPerRow:     handler must invoke permService.{Can*,Filter*}, OR pass
//                     the authed user to a service method that does its own
//                     RBAC. Default for any mutation or per-tenant read.
//   - KindScopedList: list endpoint relying on SQL-level scope enforcement at
//                     the repo layer via authz.ScopeFromAPIUser.
//   - KindRoleOnly:   Casbin-style coarse role gate is the primary authority.
//                     Use sparingly — only for procedures whose policy row is
//                     admin-only AND for which there is no per-tenant
//                     narrowing (e.g. ListEvents, RunJWTExpirySweep, RetryJob).
//                     Per A19 (2026-05-23), every KindRoleOnly handler MUST
//                     also invoke the shared `requireAdmin(ctx)` helper from
//                     util.go as defense-in-depth. (Renamed from "casbinOnly"
//                     in A17 when Casbin was retired in favour of the
//                     authz registry. Semantics unchanged.)
//   - KindPublic:     procedure is exempt from auth. Login and ValidateToken
//                     are the only ones today.
//
// Pre-A17 (PROPOSALS.md A5/A17, 2026-05-23) the test held a local `want` map
// that paralleled the routing table in middleware/auth.go::extractAction and
// the (role, resource, action) policy in casbin_policy.csv. A17 consolidated
// all three into authz.Procedures — this test now reads directly from the
// registry, eliminating the parallel-table drift class entirely.
func TestHandlerAuthzLint(t *testing.T) {
	const handlersDir = "."

	// Authoritative routing map: authz.Procedures (registry.go). The test
	// cross-checks each handler method against its registry entry.

	// Discover all handler methods in this directory.
	got := map[string]handlerMethod{}
	entries, err := os.ReadDir(handlersDir)
	if err != nil {
		t.Fatalf("read handlers dir: %v", err)
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), "_handler.go") {
			continue
		}
		path := filepath.Join(handlersDir, e.Name())
		methods, err := parseHandlerMethods(path)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for k, v := range methods {
			got[k] = v
		}
	}

	// Cross-check 1: every handler method's procedure path must be in the registry.
	var missingFromRegistry []string
	for procPath := range got {
		if _, ok := authz.Procedures[procPath]; !ok {
			missingFromRegistry = append(missingFromRegistry, procPath)
		}
	}
	sort.Strings(missingFromRegistry)
	if len(missingFromRegistry) > 0 {
		t.Errorf("handler methods not classified in authz.Procedures (internal/application/authz/registry.go):\n  %s\nAdd each one with a deliberate Kind (KindPerRow / KindScopedList / KindRoleOnly / KindPublic).",
			strings.Join(missingFromRegistry, "\n  "))
	}

	// Cross-check 2: every registry entry must have an implementing handler.
	var extraInRegistry []string
	for procPath := range authz.Procedures {
		if _, ok := got[procPath]; !ok {
			extraInRegistry = append(extraInRegistry, procPath)
		}
	}
	sort.Strings(extraInRegistry)
	if len(extraInRegistry) > 0 {
		t.Errorf("entries in authz.Procedures without a handler implementation (proto removed without removing the row, or registry edited without a handler?):\n  %s",
			strings.Join(extraInRegistry, "\n  "))
	}

	// Per-kind body checks.
	var leaks []string
	for procPath, m := range got {
		p, ok := authz.Procedures[procPath]
		if !ok {
			continue // already reported above
		}
		switch p.Kind {
		case authz.KindPerRow:
			if !m.hasPermServiceCall && !m.passesUserToService {
				leaks = append(leaks, procPath+" (Kind=KindPerRow but body has no permService.* call AND does not pass the authed user to a service method)")
			}
		case authz.KindScopedList:
			if !m.hasScopeFromAPIUser {
				leaks = append(leaks, procPath+" (Kind=KindScopedList but body has no authz.ScopeFromAPIUser call)")
			}
		case authz.KindRoleOnly:
			if !m.hasRequireAdmin {
				leaks = append(leaks, procPath+" (Kind=KindRoleOnly but body has no requireAdmin(ctx) call — every KindRoleOnly handler must invoke the shared requireAdmin gate from util.go per A19)")
			}
		case authz.KindPublic:
			// no body requirement
		case authz.KindUnknown:
			leaks = append(leaks, procPath+" (Kind=KindUnknown — registry entry is missing a real Kind)")
		}
	}
	sort.Strings(leaks)
	if len(leaks) > 0 {
		t.Errorf("handler methods missing required authz pattern:\n  %s",
			strings.Join(leaks, "\n  "))
	}
}

type handlerMethod struct {
	hasPermServiceCall  bool
	hasScopeFromAPIUser bool
	passesUserToService bool
	hasRequireAdmin     bool
}

// connectProcedurePathFor reconstructs the Connect procedure path for a
// (recvType, methodName) pair. The receiver `XxxHandler` maps to service
// `XxxService` with two carve-outs (`ScopedKey` → `ScopedSigningKey`,
// `APIToken` → `APIToken`). The proto package is hardcoded as `nis.v1` —
// every NIS proto file lives in that package today; if a future proto adds
// a different package, generalise here.
func connectProcedurePathFor(recvType, methodName string) string {
	service := strings.TrimSuffix(recvType, "Handler") + "Service"
	if service == "ScopedKeyService" {
		service = "ScopedSigningKeyService"
	}
	return "/" + nisv1connectPackage + "." + service + "/" + methodName
}

// nisv1connectPackage is the proto package name. Pulled into a const so a
// rename via `buf` is caught: if the gen package's path changes, the
// nisv1connect import below stops resolving and the build fails.
const nisv1connectPackage = "nis.v1"

// _ pins the nisv1connect import — used only to fail the build if the gen
// package moves. The registry consumes it for real, but the lint test
// reaches it transitively.
var _ = nisv1connect.AccountServiceCreateAccountProcedure

func parseHandlerMethods(path string) (map[string]handlerMethod, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil, err
	}
	out := map[string]handlerMethod{}
	for _, decl := range file.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Recv == nil || len(fd.Recv.List) == 0 {
			continue
		}
		// Receiver: *XxxHandler
		recvType, ok := receiverTypeName(fd.Recv.List[0].Type)
		if !ok || !strings.HasSuffix(recvType, "Handler") {
			continue
		}
		if fd.Name == nil || !ast.IsExported(fd.Name.Name) {
			continue
		}
		// Signature: must match Connect handler shape
		// (context.Context, *connect.Request[X]) (*connect.Response[Y], error)
		if !looksLikeConnectHandler(fd.Type) {
			continue
		}
		key := connectProcedurePathFor(recvType, fd.Name.Name)

		m := handlerMethod{}
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.SelectorExpr:
				// h.permService.Can*  / h.permSvc.Can*  / h.permService.Filter*
				if isPermServiceSelector(v) {
					m.hasPermServiceCall = true
				}
				// authz.ScopeFromAPIUser
				if pkg, ok := v.X.(*ast.Ident); ok && pkg.Name == "authz" && v.Sel.Name == "ScopeFromAPIUser" {
					m.hasScopeFromAPIUser = true
				}
			case *ast.CallExpr:
				// h.svc.Foo(ctx, requestingUser, ...) / h.service.Foo(ctx, user, ...)
				if passesUserToServiceCall(v) {
					m.passesUserToService = true
				}
				// requireAdmin(ctx) — bare ident, the shared util.go helper.
				if id, ok := v.Fun.(*ast.Ident); ok && id.Name == "requireAdmin" {
					m.hasRequireAdmin = true
				}
			}
			return true
		})
		out[key] = m
	}
	return out, nil
}

func receiverTypeName(expr ast.Expr) (string, bool) {
	switch t := expr.(type) {
	case *ast.StarExpr:
		if id, ok := t.X.(*ast.Ident); ok {
			return id.Name, true
		}
	case *ast.Ident:
		return t.Name, true
	}
	return "", false
}

func looksLikeConnectHandler(ft *ast.FuncType) bool {
	if ft.Params == nil || ft.Results == nil {
		return false
	}
	// First param of any Connect handler shape: context.Context.
	if len(ft.Params.List) == 0 ||
		!isSelectorIdent(ft.Params.List[0].Type, "context", "Context") {
		return false
	}
	// Unary: (ctx, *connect.Request[X]) (*connect.Response[Y], error)
	if len(ft.Params.List) == 2 && len(ft.Results.List) == 2 {
		star, ok := ft.Params.List[1].Type.(*ast.StarExpr)
		if !ok || !isGenericSelector(star.X, "connect", "Request") {
			return false
		}
		star2, ok := ft.Results.List[0].Type.(*ast.StarExpr)
		if !ok || !isGenericSelector(star2.X, "connect", "Response") {
			return false
		}
		return isIdent(ft.Results.List[1].Type, "error")
	}
	// Server-streaming: (ctx, *connect.Request[X], *connect.ServerStream[Y]) error
	if len(ft.Params.List) == 3 && len(ft.Results.List) == 1 {
		star, ok := ft.Params.List[1].Type.(*ast.StarExpr)
		if !ok || !isGenericSelector(star.X, "connect", "Request") {
			return false
		}
		star2, ok := ft.Params.List[2].Type.(*ast.StarExpr)
		if !ok || !isGenericSelector(star2.X, "connect", "ServerStream") {
			return false
		}
		return isIdent(ft.Results.List[0].Type, "error")
	}
	return false
}

func isSelectorIdent(expr ast.Expr, pkg, name string) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	return id.Name == pkg && sel.Sel.Name == name
}

// isGenericSelector matches `pkg.Name[...]` (e.g. connect.Request[X]).
func isGenericSelector(expr ast.Expr, pkg, name string) bool {
	switch v := expr.(type) {
	case *ast.IndexExpr:
		return isSelectorIdent(v.X, pkg, name)
	case *ast.IndexListExpr:
		return isSelectorIdent(v.X, pkg, name)
	case *ast.SelectorExpr:
		return isSelectorIdent(expr, pkg, name)
	}
	return false
}

func isIdent(expr ast.Expr, name string) bool {
	id, ok := expr.(*ast.Ident)
	return ok && id.Name == name
}

// isPermServiceSelector matches h.permService.X, h.permSvc.X, or
// receiver.permService.Anything.
func isPermServiceSelector(sel *ast.SelectorExpr) bool {
	inner, ok := sel.X.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	// inner.Sel is the field name on the receiver
	name := inner.Sel.Name
	return name == "permService" || name == "permSvc"
}

// passesUserToServiceCall detects calls like h.service.Foo(ctx, requestingUser, ...)
// where one of the args is a known authed-user identifier. This covers the
// "service delegates the RBAC check" pattern used by AuthHandler.
func passesUserToServiceCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	// h.{service,svc,authService,...}.Method — receiver field on `h`.
	inner, ok := sel.X.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	recvIdent, ok := inner.X.(*ast.Ident)
	if !ok || recvIdent.Name != "h" {
		return false
	}
	field := inner.Sel.Name
	if field == "permService" || field == "permSvc" {
		// Permission service calls are accounted for elsewhere.
		return false
	}
	// Any arg matches one of the known user-identifier names.
	for _, arg := range call.Args {
		id, ok := arg.(*ast.Ident)
		if !ok {
			continue
		}
		switch id.Name {
		case "requestingUser", "apiUser", "user", "authedUserVal":
			return true
		}
	}
	return false
}
