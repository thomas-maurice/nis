package authz

import (
	nisv1connect "github.com/thomas-maurice/nis/gen/nis/v1/nisv1connect"
	"github.com/thomas-maurice/nis/internal/domain/entities"
)

// This file is the SINGLE SOURCE OF TRUTH for RPC authorization routing in NIS.
// It replaces three parallel structures that previously had to be edited in
// lockstep on every new RPC (A17, 2026-05-23):
//
//   1. internal/application/services/casbin_policy.csv — the (role, resource,
//      action) allow list. Deleted; RolePolicy below is the canonical form.
//   2. internal/interfaces/grpc/middleware/auth.go::extractAction — the
//      verb-prefix → action heuristic. Deleted; Procedures below maps each
//      RPC path to its (resource, action) explicitly.
//   3. internal/interfaces/grpc/handlers/handler_authz_lint_test.go::want —
//      the perRow/scopedList/roleOnly/public kind classification. Deleted;
//      Procedures.Kind below is the canonical form, read by the lint at test
//      time.
//
// All three carried the same routing information. They could (and did) drift
// silently — A5 (2026-05-23) shipped the lint guardrail without consolidating
// the other two, leaving a moving target for the next RPC.
//
// Casbin was retired in the same change. Its model file (NIS-flavoured)
// reduced to `r.sub == p.sub && r.obj == p.obj && r.act == p.act` — pure
// string-equality set membership. RolePermits below does the same lookup as a
// pre-computed map[string]bool with no dependency.
//
// Adding a new RPC:
//   1. Add a row to Procedures with the typed Connect constant as key, the
//      desired (Resource, Action, Kind) as value.
//   2. If the (Resource, Action) tuple is new, add the corresponding
//      RolePolicy rows for every role that should be permitted.
//   3. Implement the handler body to match the Kind — perRow handlers must
//      invoke permService.Can*, scopedList handlers must build
//      ScopeFromAPIUser, roleOnly handlers must invoke requireAdmin.
//   4. The lint test fails the build if any step is skipped.

// Kind classifies how an RPC handler is expected to authorize requests. The
// lint test in internal/interfaces/grpc/handlers/handler_authz_lint_test.go
// enforces that each handler body matches its declared Kind.
type Kind int

const (
	// KindUnknown is the zero value — reserved as a defensive default. Never
	// declared explicitly on a procedure; an unknown procedure is denied by
	// RolePermits regardless.
	KindUnknown Kind = iota

	// KindPerRow is the default for any mutation or per-tenant read. Handler
	// body MUST invoke permService.Can*/Filter*, OR pass the authed user to a
	// service method that performs its own RBAC narrowing (the AuthHandler
	// delegate pattern). Casbin's coarse (resource, action) grant alone is
	// not sufficient — operator-admin A can see they're allowed to mutate
	// accounts, but only the per-row check stops them mutating B's accounts.
	KindPerRow

	// KindScopedList is for list endpoints whose tenant scope is enforced at
	// the SQL layer via authz.ScopeFromAPIUser + repo ListPage. Handler body
	// MUST reference ScopeFromAPIUser. See SKILL.md §15 for the mechanism.
	KindScopedList

	// KindRoleOnly is for handlers whose policy is admin-only and where no
	// per-tenant narrowing exists (jobs, events, config, scheduled-backup
	// imports). Handler body MUST invoke requireAdmin(ctx) for
	// defense-in-depth — Casbin's coarse role gate is the primary
	// authorisation, but a future policy edit that widens access must not
	// silently expose the handler. (Renamed from "casbinOnly" in A17 when
	// Casbin itself was removed; the semantics are unchanged.)
	KindRoleOnly

	// KindPublic is for procedures that require no authentication at all —
	// Login (issue a token) and ValidateToken (introspect a token; the token
	// is in the request body, not the Authorization header). The middleware
	// short-circuits these before reading the Authorization header.
	KindPublic
)

// Procedure describes the authz triple for a single Connect RPC.
type Procedure struct {
	// Resource is the noun-shaped tag the policy is keyed on (e.g. "account",
	// "user", "scoped_key"). The value space is OPAQUE — these strings exist
	// only to key RolePolicy and the historical casbin_policy.csv format.
	// Some are snake_case ("api_user", "scoped_key"), some are single-word
	// ("apitoken", "webhook"); the inconsistency is preserved from the
	// pre-A17 CSV intentionally so behaviour is byte-identical.
	Resource string

	// Action is the verb tag ("create", "read", "update", "delete"). Same
	// opaque-string contract as Resource.
	Action string

	// Kind is the authz pattern the handler body must implement. Enforced by
	// the lint test at test time and (for KindPublic) by the middleware at
	// request time.
	Kind Kind
}

// Resource names — declared as constants so the registry is grep-friendly and
// typos in the policy table are compile errors. The values preserve the
// pre-A17 casbin_policy.csv strings exactly.
const (
	ResourceOperator   = "operator"
	ResourceAccount    = "account"
	ResourceUser       = "user"
	ResourceScopedKey  = "scoped_key"
	ResourceCluster    = "cluster"
	ResourceExport     = "export"
	ResourceAPIUser    = "api_user"
	ResourceAuth       = "auth"
	ResourceEvent      = "event"
	ResourceWebhook    = "webhook"
	ResourceAPIToken   = "apitoken"
	ResourceSearch     = "search"
	ResourceTemplate   = "template"
	ResourceJob        = "job"
	ResourceBackup     = "backup"
	ResourceConfig     = "config"
)

// Action names. Same constants-for-typo-protection rationale as Resource.
const (
	ActionCreate = "create"
	ActionRead   = "read"
	ActionUpdate = "update"
	ActionDelete = "delete"
)

// Procedures is the single source of truth for "which RPC maps to which
// (resource, action, kind)". Keys are the typed nisv1connect Procedure
// constants — using strings here would silently break on a proto rename.
//
// Every Connect RPC defined in the gen package MUST have a row here. The
// handler_authz_lint_test cross-checks both ways: a handler with no entry
// fails the build, and an entry without a matching handler fails the build.
//
// Convention: rows are grouped by service and ordered to match the proto.
var Procedures = map[string]Procedure{
	// AccountService.
	nisv1connect.AccountServiceCreateAccountProcedure:             {Resource: ResourceAccount, Action: ActionCreate, Kind: KindPerRow},
	nisv1connect.AccountServiceGetAccountProcedure:                {Resource: ResourceAccount, Action: ActionRead, Kind: KindPerRow},
	nisv1connect.AccountServiceGetAccountByNameProcedure:          {Resource: ResourceAccount, Action: ActionRead, Kind: KindPerRow},
	nisv1connect.AccountServiceListAccountsProcedure:              {Resource: ResourceAccount, Action: ActionRead, Kind: KindScopedList},
	nisv1connect.AccountServiceUpdateAccountProcedure:             {Resource: ResourceAccount, Action: ActionUpdate, Kind: KindPerRow},
	nisv1connect.AccountServiceUpdateJetStreamLimitsProcedure:     {Resource: ResourceAccount, Action: ActionUpdate, Kind: KindPerRow},
	nisv1connect.AccountServiceDeleteAccountProcedure:             {Resource: ResourceAccount, Action: ActionDelete, Kind: KindPerRow},
	nisv1connect.AccountServicePushAccountJWTProcedure:            {Resource: ResourceAccount, Action: ActionUpdate, Kind: KindPerRow},
	nisv1connect.AccountServiceListAccountJWTRevocationsProcedure: {Resource: ResourceAccount, Action: ActionRead, Kind: KindPerRow},
	nisv1connect.AccountServiceGetAccountJetStreamUsageProcedure:  {Resource: ResourceAccount, Action: ActionRead, Kind: KindPerRow},

	// APITokenService.
	nisv1connect.APITokenServiceCreateAPITokenProcedure: {Resource: ResourceAPIToken, Action: ActionCreate, Kind: KindPerRow},
	nisv1connect.APITokenServiceGetAPITokenProcedure:    {Resource: ResourceAPIToken, Action: ActionRead, Kind: KindPerRow},
	nisv1connect.APITokenServiceListAPITokensProcedure:  {Resource: ResourceAPIToken, Action: ActionRead, Kind: KindPerRow},
	nisv1connect.APITokenServiceRevokeAPITokenProcedure: {Resource: ResourceAPIToken, Action: ActionDelete, Kind: KindPerRow},
	nisv1connect.APITokenServiceDeleteAPITokenProcedure: {Resource: ResourceAPIToken, Action: ActionDelete, Kind: KindPerRow},

	// AuthService. Login + ValidateToken are KindPublic — no auth header
	// required. Pre-A17, Login was public via the publicMethods map but
	// ValidateToken was inadvertently NOT, so any caller would 403 on a
	// procedure that takes a token in the request body and exists precisely
	// to introspect it. A17 aligns the wire behaviour with the documented
	// intent; this is a deliberate bundled fix, called out in PROPOSALS.md.
	nisv1connect.AuthServiceLoginProcedure:                    {Resource: ResourceAuth, Action: ActionRead, Kind: KindPublic},
	nisv1connect.AuthServiceValidateTokenProcedure:            {Resource: ResourceAuth, Action: ActionRead, Kind: KindPublic},
	nisv1connect.AuthServiceCreateAPIUserProcedure:            {Resource: ResourceAPIUser, Action: ActionCreate, Kind: KindPerRow},
	nisv1connect.AuthServiceGetAPIUserProcedure:               {Resource: ResourceAPIUser, Action: ActionRead, Kind: KindPerRow},
	nisv1connect.AuthServiceGetAPIUserByUsernameProcedure:     {Resource: ResourceAPIUser, Action: ActionRead, Kind: KindPerRow},
	nisv1connect.AuthServiceListAPIUsersProcedure:             {Resource: ResourceAPIUser, Action: ActionRead, Kind: KindScopedList},
	nisv1connect.AuthServiceUpdateAPIUserPasswordProcedure:    {Resource: ResourceAPIUser, Action: ActionUpdate, Kind: KindPerRow},
	nisv1connect.AuthServiceUpdateAPIUserPermissionsProcedure: {Resource: ResourceAPIUser, Action: ActionUpdate, Kind: KindPerRow},
	nisv1connect.AuthServiceDeleteAPIUserProcedure:            {Resource: ResourceAPIUser, Action: ActionDelete, Kind: KindPerRow},

	// BackupService. RunOperatorBackup → update preserves the pre-A17 "run*"
	// verb-prefix routing.
	nisv1connect.BackupServiceUpdateOperatorBackupSettingsProcedure: {Resource: ResourceBackup, Action: ActionUpdate, Kind: KindPerRow},
	nisv1connect.BackupServiceGetOperatorBackupSettingsProcedure:    {Resource: ResourceBackup, Action: ActionRead, Kind: KindPerRow},
	nisv1connect.BackupServiceRunOperatorBackupProcedure:            {Resource: ResourceBackup, Action: ActionUpdate, Kind: KindPerRow},
	nisv1connect.BackupServiceListOperatorBackupsProcedure:          {Resource: ResourceBackup, Action: ActionRead, Kind: KindScopedList},
	nisv1connect.BackupServiceGetBackupProcedure:                    {Resource: ResourceBackup, Action: ActionRead, Kind: KindPerRow},
	nisv1connect.BackupServiceDownloadBackupProcedure:               {Resource: ResourceBackup, Action: ActionRead, Kind: KindPerRow},
	nisv1connect.BackupServiceDeleteBackupProcedure:                 {Resource: ResourceBackup, Action: ActionDelete, Kind: KindPerRow},

	// ClusterService. SyncCluster / ReconcileAccountOnCluster / GenerateServerConfig
	// all routed to (cluster, read) by the pre-A17 extractAction fallthrough;
	// per-row narrowing in the handler (CanSyncCluster, etc.) is the actual
	// authority. The pre-A17 dead "cluster.sync" CSV row is dropped — it was
	// never reached by the verb-prefix routing.
	nisv1connect.ClusterServiceCreateClusterProcedure:             {Resource: ResourceCluster, Action: ActionCreate, Kind: KindPerRow},
	nisv1connect.ClusterServiceGetClusterProcedure:                {Resource: ResourceCluster, Action: ActionRead, Kind: KindPerRow},
	nisv1connect.ClusterServiceGetClusterByNameProcedure:          {Resource: ResourceCluster, Action: ActionRead, Kind: KindPerRow},
	nisv1connect.ClusterServiceListClustersProcedure:              {Resource: ResourceCluster, Action: ActionRead, Kind: KindScopedList},
	nisv1connect.ClusterServiceUpdateClusterProcedure:             {Resource: ResourceCluster, Action: ActionUpdate, Kind: KindPerRow},
	nisv1connect.ClusterServiceUpdateClusterCredentialsProcedure:  {Resource: ResourceCluster, Action: ActionUpdate, Kind: KindPerRow},
	nisv1connect.ClusterServiceDeleteClusterProcedure:             {Resource: ResourceCluster, Action: ActionDelete, Kind: KindPerRow},
	nisv1connect.ClusterServiceGetClusterCredentialsProcedure:     {Resource: ResourceCluster, Action: ActionRead, Kind: KindPerRow},
	nisv1connect.ClusterServiceGenerateServerConfigProcedure:      {Resource: ResourceCluster, Action: ActionRead, Kind: KindPerRow},
	nisv1connect.ClusterServiceSyncClusterProcedure:               {Resource: ResourceCluster, Action: ActionRead, Kind: KindPerRow},
	nisv1connect.ClusterServiceListResolverAccountsProcedure:      {Resource: ResourceCluster, Action: ActionRead, Kind: KindPerRow},
	nisv1connect.ClusterServiceDeleteResolverAccountProcedure:     {Resource: ResourceCluster, Action: ActionDelete, Kind: KindPerRow},
	nisv1connect.ClusterServiceGetClusterDriftStatusProcedure:     {Resource: ResourceCluster, Action: ActionRead, Kind: KindPerRow},
	nisv1connect.ClusterServiceReconcileAccountOnClusterProcedure: {Resource: ResourceCluster, Action: ActionRead, Kind: KindPerRow},

	// ConfigService.
	nisv1connect.ConfigServiceGetRunningConfigProcedure: {Resource: ResourceConfig, Action: ActionRead, Kind: KindRoleOnly},

	// EventService.
	nisv1connect.EventServiceListEventsProcedure: {Resource: ResourceEvent, Action: ActionRead, Kind: KindRoleOnly},
	nisv1connect.EventServiceGetEventProcedure:   {Resource: ResourceEvent, Action: ActionRead, Kind: KindRoleOnly},

	// ExportService. ImportOperator + ImportFromNSC route to (export, read)
	// because the pre-A17 extractAction had no "import*" prefix — they fell
	// through to "read". requireAdmin() in the handler is what actually denies
	// non-admins. Preserved verbatim to avoid changing wire semantics.
	nisv1connect.ExportServiceExportOperatorProcedure: {Resource: ResourceExport, Action: ActionRead, Kind: KindPerRow},
	nisv1connect.ExportServiceImportOperatorProcedure: {Resource: ResourceExport, Action: ActionRead, Kind: KindRoleOnly},
	nisv1connect.ExportServiceImportFromNSCProcedure:  {Resource: ResourceExport, Action: ActionRead, Kind: KindRoleOnly},

	// JobService. retry → update, cancel → delete (pre-A17 verb-prefix rules).
	nisv1connect.JobServiceListJobsProcedure:  {Resource: ResourceJob, Action: ActionRead, Kind: KindRoleOnly},
	nisv1connect.JobServiceGetJobProcedure:    {Resource: ResourceJob, Action: ActionRead, Kind: KindRoleOnly},
	nisv1connect.JobServiceRetryJobProcedure:  {Resource: ResourceJob, Action: ActionUpdate, Kind: KindRoleOnly},
	nisv1connect.JobServiceCancelJobProcedure: {Resource: ResourceJob, Action: ActionDelete, Kind: KindRoleOnly},

	// OperatorService. CreateOperator is KindRoleOnly (admin-only via Casbin
	// pre-A17, requireAdmin defense-in-depth added by A19). GenerateInclude,
	// SetSystemAccount, SetJWTPolicy, RunJWTExpirySweep all routed to
	// (operator, ...) per pre-A17 verb-prefix rules; preserved.
	nisv1connect.OperatorServiceCreateOperatorProcedure:    {Resource: ResourceOperator, Action: ActionCreate, Kind: KindRoleOnly},
	nisv1connect.OperatorServiceGetOperatorProcedure:       {Resource: ResourceOperator, Action: ActionRead, Kind: KindPerRow},
	nisv1connect.OperatorServiceGetOperatorByNameProcedure: {Resource: ResourceOperator, Action: ActionRead, Kind: KindPerRow},
	nisv1connect.OperatorServiceListOperatorsProcedure:     {Resource: ResourceOperator, Action: ActionRead, Kind: KindScopedList},
	nisv1connect.OperatorServiceUpdateOperatorProcedure:    {Resource: ResourceOperator, Action: ActionUpdate, Kind: KindPerRow},
	nisv1connect.OperatorServiceSetSystemAccountProcedure:  {Resource: ResourceOperator, Action: ActionUpdate, Kind: KindPerRow},
	nisv1connect.OperatorServiceDeleteOperatorProcedure:    {Resource: ResourceOperator, Action: ActionDelete, Kind: KindPerRow},
	nisv1connect.OperatorServiceGenerateIncludeProcedure:   {Resource: ResourceOperator, Action: ActionRead, Kind: KindPerRow},
	nisv1connect.OperatorServiceSetJWTPolicyProcedure:      {Resource: ResourceOperator, Action: ActionUpdate, Kind: KindPerRow},
	nisv1connect.OperatorServiceRunJWTExpirySweepProcedure: {Resource: ResourceOperator, Action: ActionUpdate, Kind: KindPerRow},

	// ScopedSigningKeyService. rotate / detach / settracklatest map to
	// "update" per pre-A17 verb-prefix table.
	nisv1connect.ScopedSigningKeyServiceCreateScopedSigningKeyProcedure:    {Resource: ResourceScopedKey, Action: ActionCreate, Kind: KindPerRow},
	nisv1connect.ScopedSigningKeyServiceGetScopedSigningKeyProcedure:       {Resource: ResourceScopedKey, Action: ActionRead, Kind: KindPerRow},
	nisv1connect.ScopedSigningKeyServiceGetScopedSigningKeyByNameProcedure: {Resource: ResourceScopedKey, Action: ActionRead, Kind: KindPerRow},
	nisv1connect.ScopedSigningKeyServiceListScopedSigningKeysProcedure:     {Resource: ResourceScopedKey, Action: ActionRead, Kind: KindScopedList},
	nisv1connect.ScopedSigningKeyServiceUpdateScopedSigningKeyProcedure:    {Resource: ResourceScopedKey, Action: ActionUpdate, Kind: KindPerRow},
	nisv1connect.ScopedSigningKeyServiceUpdatePermissionsProcedure:         {Resource: ResourceScopedKey, Action: ActionUpdate, Kind: KindPerRow},
	nisv1connect.ScopedSigningKeyServiceDeleteScopedSigningKeyProcedure:    {Resource: ResourceScopedKey, Action: ActionDelete, Kind: KindPerRow},
	nisv1connect.ScopedSigningKeyServiceDetachFromTemplateProcedure:        {Resource: ResourceScopedKey, Action: ActionUpdate, Kind: KindPerRow},
	nisv1connect.ScopedSigningKeyServiceSetTrackLatestProcedure:            {Resource: ResourceScopedKey, Action: ActionUpdate, Kind: KindPerRow},
	nisv1connect.ScopedSigningKeyServiceRotateScopedSigningKeyProcedure:    {Resource: ResourceScopedKey, Action: ActionUpdate, Kind: KindPerRow},

	// SearchService.
	nisv1connect.SearchServiceSearchProcedure: {Resource: ResourceSearch, Action: ActionRead, Kind: KindPerRow},

	// TemplateService. apply → update (pre-A17 verb-prefix rule).
	nisv1connect.TemplateServiceCreateTemplateProcedure:           {Resource: ResourceTemplate, Action: ActionCreate, Kind: KindPerRow},
	nisv1connect.TemplateServiceGetTemplateProcedure:              {Resource: ResourceTemplate, Action: ActionRead, Kind: KindPerRow},
	nisv1connect.TemplateServiceGetTemplateByNameProcedure:        {Resource: ResourceTemplate, Action: ActionRead, Kind: KindPerRow},
	nisv1connect.TemplateServiceListTemplatesProcedure:            {Resource: ResourceTemplate, Action: ActionRead, Kind: KindScopedList},
	nisv1connect.TemplateServiceUpdateTemplateProcedure:           {Resource: ResourceTemplate, Action: ActionUpdate, Kind: KindPerRow},
	nisv1connect.TemplateServiceDeleteTemplateProcedure:           {Resource: ResourceTemplate, Action: ActionDelete, Kind: KindPerRow},
	nisv1connect.TemplateServiceListTemplateVersionsProcedure:     {Resource: ResourceTemplate, Action: ActionRead, Kind: KindPerRow},
	nisv1connect.TemplateServiceListTemplateDependentsProcedure:   {Resource: ResourceTemplate, Action: ActionRead, Kind: KindPerRow},
	nisv1connect.TemplateServiceApplyTemplateToScopedKeyProcedure: {Resource: ResourceTemplate, Action: ActionUpdate, Kind: KindPerRow},

	// UserService. revoke → delete (pre-A17 verb-prefix rule).
	nisv1connect.UserServiceCreateUserProcedure:                {Resource: ResourceUser, Action: ActionCreate, Kind: KindPerRow},
	nisv1connect.UserServiceGetUserProcedure:                   {Resource: ResourceUser, Action: ActionRead, Kind: KindPerRow},
	nisv1connect.UserServiceGetUserByNameProcedure:             {Resource: ResourceUser, Action: ActionRead, Kind: KindPerRow},
	nisv1connect.UserServiceListUsersProcedure:                 {Resource: ResourceUser, Action: ActionRead, Kind: KindScopedList},
	nisv1connect.UserServiceUpdateUserProcedure:                {Resource: ResourceUser, Action: ActionUpdate, Kind: KindPerRow},
	nisv1connect.UserServiceDeleteUserProcedure:                {Resource: ResourceUser, Action: ActionDelete, Kind: KindPerRow},
	nisv1connect.UserServiceGetUserCredentialsProcedure:        {Resource: ResourceUser, Action: ActionRead, Kind: KindPerRow},
	nisv1connect.UserServiceRevokeUserProcedure:                {Resource: ResourceUser, Action: ActionDelete, Kind: KindPerRow},
	nisv1connect.UserServiceRegenerateUserCredentialsProcedure: {Resource: ResourceUser, Action: ActionRead, Kind: KindPerRow},

	// WebhookService. The pre-A17 extractAction had no "test*" prefix so
	// TestWebhookSubscription routed to "read"; preserved.
	nisv1connect.WebhookServiceCreateWebhookSubscriptionProcedure: {Resource: ResourceWebhook, Action: ActionCreate, Kind: KindPerRow},
	nisv1connect.WebhookServiceGetWebhookSubscriptionProcedure:    {Resource: ResourceWebhook, Action: ActionRead, Kind: KindPerRow},
	nisv1connect.WebhookServiceListWebhookSubscriptionsProcedure:  {Resource: ResourceWebhook, Action: ActionRead, Kind: KindScopedList},
	nisv1connect.WebhookServiceUpdateWebhookSubscriptionProcedure: {Resource: ResourceWebhook, Action: ActionUpdate, Kind: KindPerRow},
	nisv1connect.WebhookServiceDeleteWebhookSubscriptionProcedure: {Resource: ResourceWebhook, Action: ActionDelete, Kind: KindPerRow},
	nisv1connect.WebhookServiceTestWebhookSubscriptionProcedure:   {Resource: ResourceWebhook, Action: ActionRead, Kind: KindPerRow},
	nisv1connect.WebhookServiceListWebhookDeliveriesProcedure:     {Resource: ResourceWebhook, Action: ActionRead, Kind: KindScopedList},
}

// RoleGrant is one row of the (role, resource, actions) policy. A grant lists
// the actions a single role is allowed on a single resource — purely additive.
// The absence of a (role, resource, action) triple in any grant is the
// default-deny.
type RoleGrant struct {
	Role     entities.APIUserRole
	Resource string
	Actions  []string
}

// RolePolicy is the canonical replacement for the pre-A17 casbin_policy.csv.
// The 88 rows below preserve the pre-A17 grants byte-for-byte EXCEPT:
//
//   - The dead `(operator-admin, cluster, sync)` row from the CSV is dropped.
//     It was never reached: extractAction routed SyncCluster to (cluster,
//     read), so the "sync" action was unreachable from any procedure. The
//     per-row narrowing in PermissionService.CanSyncCluster is unchanged.
//
// Adding a new role-resource-action triple is purely additive — list the
// actions on the corresponding RoleGrant.
var RolePolicy = []RoleGrant{
	// admin — full access to everything.
	{Role: entities.RoleAdmin, Resource: ResourceOperator, Actions: []string{ActionCreate, ActionRead, ActionUpdate, ActionDelete}},
	{Role: entities.RoleAdmin, Resource: ResourceAccount, Actions: []string{ActionCreate, ActionRead, ActionUpdate, ActionDelete}},
	{Role: entities.RoleAdmin, Resource: ResourceUser, Actions: []string{ActionCreate, ActionRead, ActionUpdate, ActionDelete}},
	{Role: entities.RoleAdmin, Resource: ResourceScopedKey, Actions: []string{ActionCreate, ActionRead, ActionUpdate, ActionDelete}},
	{Role: entities.RoleAdmin, Resource: ResourceCluster, Actions: []string{ActionCreate, ActionRead, ActionUpdate, ActionDelete}},
	{Role: entities.RoleAdmin, Resource: ResourceExport, Actions: []string{ActionCreate, ActionRead}},
	{Role: entities.RoleAdmin, Resource: ResourceAPIUser, Actions: []string{ActionCreate, ActionRead, ActionUpdate, ActionDelete}},
	{Role: entities.RoleAdmin, Resource: ResourceEvent, Actions: []string{ActionRead}},
	{Role: entities.RoleAdmin, Resource: ResourceWebhook, Actions: []string{ActionCreate, ActionRead, ActionUpdate, ActionDelete}},
	{Role: entities.RoleAdmin, Resource: ResourceAPIToken, Actions: []string{ActionCreate, ActionRead, ActionDelete}},
	{Role: entities.RoleAdmin, Resource: ResourceSearch, Actions: []string{ActionRead}},
	{Role: entities.RoleAdmin, Resource: ResourceTemplate, Actions: []string{ActionCreate, ActionRead, ActionUpdate, ActionDelete}},
	{Role: entities.RoleAdmin, Resource: ResourceJob, Actions: []string{ActionRead, ActionUpdate, ActionDelete}},
	{Role: entities.RoleAdmin, Resource: ResourceBackup, Actions: []string{ActionCreate, ActionRead, ActionUpdate, ActionDelete}},
	{Role: entities.RoleAdmin, Resource: ResourceConfig, Actions: []string{ActionRead}},

	// operator-admin — scoped to ONE operator. Can manage everything under
	// that operator EXCEPT the operator itself (no rename/delete). Cannot
	// delete accounts (data-loss guard, admin-only). Per-row narrowing is in
	// PermissionService.
	{Role: entities.RoleOperatorAdmin, Resource: ResourceOperator, Actions: []string{ActionRead}},
	{Role: entities.RoleOperatorAdmin, Resource: ResourceAccount, Actions: []string{ActionCreate, ActionRead, ActionUpdate}},
	{Role: entities.RoleOperatorAdmin, Resource: ResourceUser, Actions: []string{ActionCreate, ActionRead, ActionUpdate, ActionDelete}},
	{Role: entities.RoleOperatorAdmin, Resource: ResourceScopedKey, Actions: []string{ActionCreate, ActionRead, ActionUpdate}},
	{Role: entities.RoleOperatorAdmin, Resource: ResourceCluster, Actions: []string{ActionRead}},
	{Role: entities.RoleOperatorAdmin, Resource: ResourceExport, Actions: []string{ActionRead}},
	{Role: entities.RoleOperatorAdmin, Resource: ResourceWebhook, Actions: []string{ActionCreate, ActionRead, ActionUpdate, ActionDelete}},
	{Role: entities.RoleOperatorAdmin, Resource: ResourceAPIToken, Actions: []string{ActionCreate, ActionRead, ActionDelete}},
	{Role: entities.RoleOperatorAdmin, Resource: ResourceSearch, Actions: []string{ActionRead}},
	{Role: entities.RoleOperatorAdmin, Resource: ResourceTemplate, Actions: []string{ActionCreate, ActionRead, ActionUpdate, ActionDelete}},
	{Role: entities.RoleOperatorAdmin, Resource: ResourceBackup, Actions: []string{ActionCreate, ActionRead, ActionUpdate, ActionDelete}},

	// account-admin — scoped to ONE account. Read-only on the surrounding
	// operator and cluster; no scoped-key management; no account renames.
	{Role: entities.RoleAccountAdmin, Resource: ResourceOperator, Actions: []string{ActionRead}},
	{Role: entities.RoleAccountAdmin, Resource: ResourceAccount, Actions: []string{ActionRead}},
	{Role: entities.RoleAccountAdmin, Resource: ResourceUser, Actions: []string{ActionCreate, ActionRead, ActionUpdate}},
	{Role: entities.RoleAccountAdmin, Resource: ResourceCluster, Actions: []string{ActionRead}},
	{Role: entities.RoleAccountAdmin, Resource: ResourceAPIToken, Actions: []string{ActionCreate, ActionRead, ActionDelete}},
	{Role: entities.RoleAccountAdmin, Resource: ResourceSearch, Actions: []string{ActionRead}},
}

// permitSet is a pre-computed O(1) lookup for RolePermits. Key shape:
// "<role>|<resource>|<action>". Built at package-init time from RolePolicy.
var permitSet = buildPermitSet()

func buildPermitSet() map[string]struct{} {
	out := map[string]struct{}{}
	for _, g := range RolePolicy {
		for _, a := range g.Actions {
			out[permitKey(string(g.Role), g.Resource, a)] = struct{}{}
		}
	}
	return out
}

func permitKey(role, resource, action string) string {
	return role + "|" + resource + "|" + action
}

// ResolveProcedure returns the authz triple for a Connect procedure path. The
// boolean is false for procedures not in the registry — middleware MUST treat
// this as default-deny (the lint test refuses to ship a handler without a
// matching entry, so an unknown procedure at runtime indicates a hand-edit of
// the registry or an out-of-band proto file).
func ResolveProcedure(path string) (Procedure, bool) {
	p, ok := Procedures[path]
	return p, ok
}

// RolePermits reports whether the given role is allowed the (resource, action)
// pair under the policy. Equivalent to the pre-A17 casbin enforcer.Enforce
// call. Default-deny: any triple not present in RolePolicy returns false.
func RolePermits(role entities.APIUserRole, resource, action string) bool {
	_, ok := permitSet[permitKey(string(role), resource, action)]
	return ok
}
