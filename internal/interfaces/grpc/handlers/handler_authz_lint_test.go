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
)

// TestHandlerAuthzLint is the permanent guardrail for the bug class A5 was
// filed to prevent: a handler method that calls a service mutation without
// invoking permService.Can* (or an equivalent explicit role gate). The lint
// is structural — adding a new RPC procedure to a *_handler.go file without
// classifying it here fails the build, forcing a deliberate authz call.
//
// Three valid kinds:
//
//   - perRow:     handler must invoke permService.{Can*,Filter*}, OR pass the
//                 authed user to a service method that does its own RBAC.
//                 This is the default for any mutation or per-tenant read.
//   - scopedList: list endpoint relying on SQL-level scope enforcement at the
//                 repo layer via authz.ScopeFromAPIUser. Body must reference
//                 ScopeFromAPIUser (the only way to construct a Scope from
//                 ctx-loaded user).
//   - casbinOnly: Casbin's coarse role gate is the primary authorisation.
//                 Use sparingly — only for procedures whose policy row is
//                 admin-only AND for which there is no per-tenant narrowing
//                 to do (e.g. ListEvents, RunJWTExpirySweep, RetryJob).
//                 Per A19 (2026-05-23), every casbinOnly handler MUST also
//                 invoke the shared `requireAdmin(ctx)` helper from util.go
//                 as defense-in-depth — so a future Casbin-policy edit that
//                 accidentally widens access doesn't silently expose the
//                 handler. The lint enforces the call below.
//   - public:    procedure is in middleware.publicMethods (Login is the only
//                 such case today). No auth required.
//
// Adding a new RPC: pick a kind, add the row, and ensure the handler matches
// the pattern. The test refuses to be skipped on a "TODO" row.
func TestHandlerAuthzLint(t *testing.T) {
	const handlersDir = "."

	// Authoritative map: ServiceName.MethodName → kind.
	// Adding an RPC to a *_handler.go file without an entry here MUST fail.
	want := map[string]authzKind{
		// AccountService.
		"AccountService.CreateAccount":             perRow,
		"AccountService.GetAccount":                perRow,
		"AccountService.GetAccountByName":          perRow,
		"AccountService.ListAccounts":              scopedList,
		"AccountService.UpdateAccount":             perRow,
		"AccountService.UpdateJetStreamLimits":     perRow,
		"AccountService.DeleteAccount":             perRow,
		"AccountService.PushAccountJWT":            perRow,
		"AccountService.ListAccountJWTRevocations": perRow,
		"AccountService.GetAccountJetStreamUsage":  perRow,

		// APITokenService.
		"APITokenService.CreateAPIToken": perRow,
		"APITokenService.GetAPIToken":    perRow,
		"APITokenService.ListAPITokens":  perRow, // filters by caller inside the handler
		"APITokenService.RevokeAPIToken": perRow,
		"APITokenService.DeleteAPIToken": perRow,

		// AuthService. Most of these delegate to AuthService.* methods that
		// take requestingUser and do their own RBAC; classified perRow so the
		// service-delegate pattern is required.
		"AuthService.Login":                    public,
		"AuthService.ValidateToken":            public, // self-introspection of the bearer
		"AuthService.CreateAPIUser":            perRow,
		"AuthService.GetAPIUser":               perRow,
		"AuthService.GetAPIUserByUsername":     perRow,
		"AuthService.ListAPIUsers":             scopedList,
		"AuthService.UpdateAPIUserPassword":    perRow,
		"AuthService.UpdateAPIUserPermissions": perRow,
		"AuthService.DeleteAPIUser":            perRow,

		// BackupService.
		"BackupService.UpdateOperatorBackupSettings": perRow,
		"BackupService.GetOperatorBackupSettings":    perRow,
		"BackupService.RunOperatorBackup":            perRow,
		"BackupService.ListOperatorBackups":          scopedList,
		"BackupService.GetBackup":                    perRow,
		"BackupService.DownloadBackup":               perRow,
		"BackupService.DeleteBackup":                 perRow,

		// ClusterService.
		"ClusterService.CreateCluster":             perRow,
		"ClusterService.GetCluster":                perRow,
		"ClusterService.GetClusterByName":          perRow,
		"ClusterService.ListClusters":              scopedList,
		"ClusterService.UpdateCluster":             perRow,
		"ClusterService.UpdateClusterCredentials":  perRow,
		"ClusterService.DeleteCluster":             perRow,
		"ClusterService.GetClusterCredentials":     perRow,
		"ClusterService.GenerateServerConfig":      perRow,
		"ClusterService.SyncCluster":               perRow,
		"ClusterService.ListResolverAccounts":      perRow,
		"ClusterService.DeleteResolverAccount":     perRow,
		"ClusterService.GetClusterDriftStatus":     perRow,
		"ClusterService.ReconcileAccountOnCluster": perRow,

		// ConfigService. Admin-only via Casbin (config.read) + requireAdmin gate.
		"ConfigService.GetRunningConfig": casbinOnly,

		// EventService. Admin-only via Casbin (event.read) + requireAdmin gate.
		"EventService.ListEvents": casbinOnly,
		"EventService.GetEvent":   casbinOnly,

		// ExportService. Admin-only imports gated via requireAdmin; ExportOperator
		// is per-row (operator-admin can export own operator).
		"ExportService.ExportOperator":  perRow,
		"ExportService.ImportOperator":  casbinOnly,
		"ExportService.ImportFromNSC":   casbinOnly,

		// JobService. Admin-only via Casbin (job.*) + requireAdmin gate.
		"JobService.ListJobs":  casbinOnly,
		"JobService.GetJob":    casbinOnly,
		"JobService.RetryJob":  casbinOnly,
		"JobService.CancelJob": casbinOnly,

		// OperatorService.
		"OperatorService.CreateOperator":      casbinOnly, // admin-only via Casbin (operator.create) + requireAdmin gate
		"OperatorService.GetOperator":         perRow,
		"OperatorService.GetOperatorByName":   perRow,
		"OperatorService.ListOperators":       scopedList,
		"OperatorService.UpdateOperator":      perRow,
		"OperatorService.SetSystemAccount":    perRow,
		"OperatorService.DeleteOperator":      perRow,
		"OperatorService.GenerateInclude":     perRow,
		"OperatorService.SetJWTPolicy":        perRow,
		"OperatorService.RunJWTExpirySweep":   perRow,

		// ScopedSigningKeyService.
		"ScopedSigningKeyService.CreateScopedSigningKey":  perRow,
		"ScopedSigningKeyService.GetScopedSigningKey":     perRow,
		"ScopedSigningKeyService.GetScopedSigningKeyByName": perRow,
		"ScopedSigningKeyService.ListScopedSigningKeys":   scopedList,
		"ScopedSigningKeyService.UpdateScopedSigningKey":  perRow,
		"ScopedSigningKeyService.UpdatePermissions":       perRow,
		"ScopedSigningKeyService.DeleteScopedSigningKey":  perRow,
		"ScopedSigningKeyService.DetachFromTemplate":      perRow,
		"ScopedSigningKeyService.SetTrackLatest":          perRow,
		"ScopedSigningKeyService.RotateScopedSigningKey":  perRow,

		// SearchService. Handler passes the authed user to the service, which
		// builds authz.Scope and enforces narrowing at the SQL layer per repo
		// (A20). The perRow classification is the right fit because the
		// authority check is per-row, just expressed in SQL rather than via
		// PermissionService.Can*.
		"SearchService.Search": perRow,

		// TemplateService.
		"TemplateService.CreateTemplate":           perRow,
		"TemplateService.GetTemplate":              perRow,
		"TemplateService.GetTemplateByName":        perRow,
		"TemplateService.ListTemplates":            scopedList,
		"TemplateService.UpdateTemplate":           perRow,
		"TemplateService.DeleteTemplate":           perRow,
		"TemplateService.ListTemplateVersions":     perRow,
		"TemplateService.ListTemplateDependents":   perRow,
		"TemplateService.ApplyTemplateToScopedKey": perRow,

		// UserService.
		"UserService.CreateUser":                 perRow,
		"UserService.GetUser":                    perRow,
		"UserService.GetUserByName":              perRow,
		"UserService.ListUsers":                  scopedList,
		"UserService.UpdateUser":                 perRow,
		"UserService.DeleteUser":                 perRow,
		"UserService.GetUserCredentials":         perRow,
		"UserService.RevokeUser":                 perRow,
		"UserService.RegenerateUserCredentials":  perRow,

		// WebhookService.
		"WebhookService.CreateWebhookSubscription": perRow,
		"WebhookService.GetWebhookSubscription":    perRow,
		"WebhookService.ListWebhookSubscriptions":  scopedList,
		"WebhookService.UpdateWebhookSubscription": perRow,
		"WebhookService.DeleteWebhookSubscription": perRow,
		"WebhookService.TestWebhookSubscription":   perRow,
		"WebhookService.ListWebhookDeliveries":     scopedList,
	}

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

	// Cross-check 1: every handler method must be in the want map.
	var missingFromMap []string
	for key := range got {
		if _, ok := want[key]; !ok {
			missingFromMap = append(missingFromMap, key)
		}
	}
	sort.Strings(missingFromMap)
	if len(missingFromMap) > 0 {
		t.Errorf("handler methods not classified in TestHandlerAuthzLint's `want` map:\n  %s\nAdd each one with a kind (perRow/scopedList/casbinOnly/public).",
			strings.Join(missingFromMap, "\n  "))
	}

	// Cross-check 2: every want entry must have an implementing handler method.
	var extraInMap []string
	for key := range want {
		if _, ok := got[key]; !ok {
			extraInMap = append(extraInMap, key)
		}
	}
	sort.Strings(extraInMap)
	if len(extraInMap) > 0 {
		t.Errorf("entries in TestHandlerAuthzLint's `want` map without a handler method (proto removed without removing the row?):\n  %s",
			strings.Join(extraInMap, "\n  "))
	}

	// Per-kind body checks.
	var leaks []string
	for key, m := range got {
		k, ok := want[key]
		if !ok {
			continue // already reported above
		}
		switch k {
		case perRow:
			if !m.hasPermServiceCall && !m.passesUserToService {
				leaks = append(leaks, key+" (kind=perRow but body has no permService.* call AND does not pass the authed user to a service method)")
			}
		case scopedList:
			if !m.hasScopeFromAPIUser {
				leaks = append(leaks, key+" (kind=scopedList but body has no authz.ScopeFromAPIUser call)")
			}
		case casbinOnly:
			if !m.hasRequireAdmin {
				leaks = append(leaks, key+" (kind=casbinOnly but body has no requireAdmin(ctx) call — every casbinOnly handler must invoke the shared requireAdmin gate from util.go per A19)")
			}
		case public:
			// no body requirement
		}
	}
	sort.Strings(leaks)
	if len(leaks) > 0 {
		t.Errorf("handler methods missing required authz pattern:\n  %s",
			strings.Join(leaks, "\n  "))
	}
}

type authzKind int

const (
	perRow authzKind = iota
	scopedList
	casbinOnly
	public
)

type handlerMethod struct {
	hasPermServiceCall  bool
	hasScopeFromAPIUser bool
	passesUserToService bool
	hasRequireAdmin     bool
}

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
		// Service name derived from the receiver: AccountHandler → AccountService.
		// Two exceptions: ScopedKeyHandler → ScopedSigningKeyService;
		// APITokenHandler → APITokenService.
		serviceName := strings.TrimSuffix(recvType, "Handler") + "Service"
		if serviceName == "ScopedKeyService" {
			serviceName = "ScopedSigningKeyService"
		}
		key := serviceName + "." + fd.Name.Name

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
