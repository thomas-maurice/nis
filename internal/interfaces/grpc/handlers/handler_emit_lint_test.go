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

// TestHandlerEmitLint is the P1 guardrail: every mutation handler MUST cause
// an event to be emitted via events.EmitTx (or events.EmitSystem) somewhere
// along its service-method call chain. Without this, a future mutation lands
// without an audit trail and the events table silently drifts away from "the
// system of record for who changed what" that P1 contracts for.
//
// Lint scope: procedures in authz.Procedures whose Action is Create / Update
// / Delete AND whose Kind is KindPerRow or KindRoleOnly. Reads/lists/public
// are not mutations. KindScopedList list endpoints are reads.
//
// Implementation per the design-review pin (DESIGN.md P1): declarative
// + naming-by-convention rather than a transitive call-graph walk.
//
//	- Conventional resolution: procedure on `XxxHandler.Foo` → service file
//	  derived from the registry resource → service method `Foo`. The lint
//	  parses the file, finds method `Foo`, and walks its body PLUS any
//	  method-body it calls in the same file (one-file transitive). An
//	  `events.EmitTx`/`events.EmitSystem` selector anywhere in that closure
//	  is success.
//
//	- Overrides: emitOverrides maps procedure path → (file, method) when
//	  the implementation doesn't live where convention says (e.g.
//	  RevokeUser lives in user_revocation_service.go, RotateScopedSigningKey
//	  in ssk_rotation.go).
//
//	- Carve-outs: emitCarveouts maps procedure path → reason for procedures
//	  that legitimately do not emit at the service-method layer (substrate
//	  emits, NATS-only side-effects, etc). Bounded — keep ≤5 entries; each
//	  needs a one-line justification.
func TestHandlerEmitLint(t *testing.T) {
	const servicesDir = "../../../application/services"

	var missing []string
	for procPath, p := range authz.Procedures {
		if !isMutationAction(p.Action) {
			continue
		}
		if p.Kind != authz.KindPerRow && p.Kind != authz.KindRoleOnly {
			continue
		}
		if reason, carved := emitCarveouts[procPath]; carved {
			_ = reason // justification lives next to the map entry; reading it just enforces the comment via go vet
			continue
		}

		file, method := resolveEmitSite(procPath)
		if file == "" {
			missing = append(missing, procPath+" (no service-file mapping; add to emitOverrides or emitCarveouts)")
			continue
		}
		fullPath := filepath.Join(servicesDir, file)
		ok, err := serviceMethodEmits(fullPath, method)
		if err != nil {
			missing = append(missing, procPath+" (parse "+fullPath+": "+err.Error()+")")
			continue
		}
		if !ok {
			missing = append(missing, procPath+" (P1: mutation handler with no events.EmitTx/EmitSystem reachable in "+file+"::"+method+" — add an emit or, if intentional, register in emitCarveouts with a justification)")
		}
	}

	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("P1 emit-coverage lint failures:\n  %s", strings.Join(missing, "\n  "))
	}

	// Carve-out cap (reviewer-flagged): keep the list small and justified.
	// Raise this number only with a code-review note explaining why.
	const maxCarveouts = 5
	if got := len(emitCarveouts); got > maxCarveouts {
		t.Errorf("emitCarveouts has %d entries (cap=%d). Each carve-out is an audit gap — justify in PR or delete the entry.", got, maxCarveouts)
	}
}

// emitCarveouts are mutation procedures that intentionally do NOT emit at the
// service-method layer. Cap=5. Add new entries sparingly with a clear reason.
var emitCarveouts = map[string]string{
	nisv1connect.AccountServicePushAccountJWTProcedure: "manual JWT push; no DB mutation, no semantic state change (mirrors SyncCluster)",

	nisv1connect.ClusterServiceDeleteResolverAccountProcedure: "direct NATS resolver delete; no NIS DB mutation. Drift dashboard surfaces results.",

	nisv1connect.OperatorServiceRunJWTExpirySweepProcedure: "sweeper emits per-user user.cred.* events at finer granularity than a single operator-scoped emit would carry",

	nisv1connect.JobServiceRetryJobProcedure:  "substrate emits job.retried in JobRunner when the row is reclaimed (see A2)",
	nisv1connect.JobServiceCancelJobProcedure: "substrate emits job.cancelled when the row transitions (see A2)",
}

// emitOverrides maps procedures whose service-method implementation doesn't
// live where convention says (handler-name → service-method same-name in
// {resource}_service.go).
var emitOverrides = map[string]struct{ file, method string }{
	nisv1connect.UserServiceRevokeUserProcedure: {file: "user_revocation_service.go", method: "RevokeUser"},

	nisv1connect.ScopedSigningKeyServiceRotateScopedSigningKeyProcedure: {file: "ssk_rotation.go", method: "RotateScopedSigningKey"},

	// Backup service has internally-renamed methods.
	nisv1connect.BackupServiceRunOperatorBackupProcedure:            {file: "backup_service.go", method: "RunBackup"},
	nisv1connect.BackupServiceUpdateOperatorBackupSettingsProcedure: {file: "backup_service.go", method: "UpdateSettings"},

	// Template apply-to-scoped-key is the same code path as BumpScopedKeyTemplate.
	nisv1connect.TemplateServiceApplyTemplateToScopedKeyProcedure: {file: "scoped_signing_key_service.go", method: "BumpScopedKeyTemplate"},

	// Webhook subscription mutations use shorter method names (CreateSubscription
	// etc., not CreateWebhookSubscription).
	nisv1connect.WebhookServiceCreateWebhookSubscriptionProcedure: {file: "webhook_service.go", method: "CreateSubscription"},
	nisv1connect.WebhookServiceUpdateWebhookSubscriptionProcedure: {file: "webhook_service.go", method: "UpdateSubscription"},
	nisv1connect.WebhookServiceDeleteWebhookSubscriptionProcedure: {file: "webhook_service.go", method: "DeleteSubscription"},
	nisv1connect.WebhookServiceTestWebhookSubscriptionProcedure:   {file: "webhook_service.go", method: "TestSubscription"},

	// API token service methods drop the "APIToken" suffix internally.
	nisv1connect.APITokenServiceCreateAPITokenProcedure: {file: "api_token_service.go", method: "CreateToken"},
	nisv1connect.APITokenServiceRevokeAPITokenProcedure: {file: "api_token_service.go", method: "RevokeToken"},
	nisv1connect.APITokenServiceDeleteAPITokenProcedure: {file: "api_token_service.go", method: "DeleteToken"},

	// Operator SetSystemAccount delegates to its Tx variant where the emit lives.
	nisv1connect.OperatorServiceSetSystemAccountProcedure: {file: "operator_service.go", method: "SetSystemAccountTx"},

	// Cluster credentials: the public method delegates to the Tx variant (P1).
	nisv1connect.ClusterServiceUpdateClusterCredentialsProcedure: {file: "cluster_service.go", method: "UpdateClusterCredentialsTx"},

	// SSK detach: handler calls DetachScopedKeyTemplate.
	nisv1connect.ScopedSigningKeyServiceDetachFromTemplateProcedure: {file: "scoped_signing_key_service.go", method: "DetachScopedKeyTemplate"},

	// Backup recipients live in backup_age.go.
	nisv1connect.BackupServiceAddBackupRecipientProcedure:    {file: "backup_age.go", method: "AddRecipient"},
	nisv1connect.BackupServiceRemoveBackupRecipientProcedure: {file: "backup_age.go", method: "RemoveRecipient"},
}

// resolveEmitSite returns the service file + method for a procedure, using
// emitOverrides if present, otherwise deriving from the procedure path by
// convention.
func resolveEmitSite(procPath string) (string, string) {
	if o, ok := emitOverrides[procPath]; ok {
		return o.file, o.method
	}
	// Procedure path: /nis.v1.XxxService/Method
	slash := strings.LastIndex(procPath, "/")
	if slash < 0 {
		return "", ""
	}
	method := procPath[slash+1:]
	// Extract the service name segment.
	// e.g. /nis.v1.AccountService/UpdateAccount → AccountService
	dot := strings.LastIndex(procPath[:slash], ".")
	if dot < 0 {
		return "", ""
	}
	service := procPath[dot+1 : slash]
	// AccountService → account_service.go; ScopedSigningKeyService →
	// scoped_signing_key_service.go (camel → snake).
	resource := strings.TrimSuffix(service, "Service")
	file := camelToSnake(resource) + "_service.go"
	return file, method
}

// camelToSnake: AccountService → account_service; ScopedSigningKey → scoped_signing_key.
func camelToSnake(s string) string {
	var b strings.Builder
	for i, r := range s {
		if i > 0 && r >= 'A' && r <= 'Z' {
			b.WriteByte('_')
		}
		if r >= 'A' && r <= 'Z' {
			b.WriteRune(r + 32)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// isMutationAction reports whether the registry action represents a write.
func isMutationAction(a string) bool {
	return a == authz.ActionCreate || a == authz.ActionUpdate || a == authz.ActionDelete
}

// serviceMethodEmits parses file, finds the named method (FuncDecl), and
// reports whether its body — or any in-file method it transitively calls —
// invokes events.EmitTx or events.EmitSystem. Returns false (no error) if
// the file or method doesn't exist; the surfaced error is reserved for parse
// failures.
func serviceMethodEmits(file, method string) (bool, error) {
	if _, err := os.Stat(file); os.IsNotExist(err) {
		return false, nil
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, 0)
	if err != nil {
		return false, err
	}
	bodies := map[string]*ast.BlockStmt{}
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Body == nil || fd.Name == nil {
			continue
		}
		bodies[fd.Name.Name] = fd.Body
	}
	seen := map[string]bool{}
	var walk func(name string) bool
	walk = func(name string) bool {
		if seen[name] {
			return false
		}
		seen[name] = true
		body, ok := bodies[name]
		if !ok {
			return false
		}
		found := false
		ast.Inspect(body, func(n ast.Node) bool {
			if found {
				return false
			}
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == "events" && (sel.Sel.Name == "EmitTx" || sel.Sel.Name == "EmitSystem") {
				found = true
				return false
			}
			// One-file transitive: follow s.helperMethod(...) calls into the
			// same-file body if it exists.
			if inner, ok := sel.X.(*ast.Ident); ok && (inner.Name == "s" || inner.Name == "svc") {
				if walk(sel.Sel.Name) {
					found = true
					return false
				}
			}
			return true
		})
		return found
	}
	return walk(method), nil
}
