//go:build e2e

// events_test.go — end-to-end tests for the EventService (audit log).
// All tests are admin-only or verify the admin-only RBAC gate.
// No NATS is needed; the event substrate is DB-only.
package e2e

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	nisv1 "github.com/thomas-maurice/nis/gen/nis/v1"
)

// TestEventsLog_EmittedOnEntityCreate verifies that creating an operator emits
// the expected cascade of events: operator.created, account.created ($SYS),
// user.created (system user), and scoped_key.created (signing key).
func TestEventsLog_EmittedOnEntityCreate(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	opID := h.createOperator(t, "events-op")

	resp, err := h.eventCli.ListEvents(ctx, connect.NewRequest(&nisv1.ListEventsRequest{
		Filter: &nisv1.EventFilter{
			OperatorId: opID,
			Limit:      100,
		},
	}))
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}

	byType := make(map[string][]*nisv1.Event)
	for _, e := range resp.Msg.Events {
		byType[e.Type] = append(byType[e.Type], e)
	}

	for _, want := range []string{"operator.created", "account.created", "user.created", "scoped_key.created"} {
		if len(byType[want]) == 0 {
			t.Errorf("expected at least one %s event, got none", want)
		}
	}

	// Every event must carry the correct operator_id and actor_type.
	for _, e := range resp.Msg.Events {
		if e.OperatorId != opID {
			t.Errorf("event %s: want operator_id=%s, got %s", e.Type, opID, e.OperatorId)
		}
		if e.ActorType != "user" {
			t.Errorf("event %s: want actor_type=user, got %s", e.Type, e.ActorType)
		}
	}
}

// TestEventsLog_FilterByType verifies that filtering by a single event type
// returns only events of that type.
func TestEventsLog_FilterByType(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	opID := h.createOperator(t, "filter-type-op")

	resp, err := h.eventCli.ListEvents(ctx, connect.NewRequest(&nisv1.ListEventsRequest{
		Filter: &nisv1.EventFilter{
			OperatorId: opID,
			Types:      []string{"operator.created"},
			Limit:      10,
		},
	}))
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}

	if len(resp.Msg.Events) != 1 {
		t.Fatalf("expected exactly 1 operator.created event, got %d", len(resp.Msg.Events))
	}
	if got := resp.Msg.Events[0].Type; got != "operator.created" {
		t.Errorf("expected operator.created, got %s", got)
	}
}

// TestEventsLog_FilterByOperator verifies that filtering by operator_id returns
// only events for that operator and none from others.
func TestEventsLog_FilterByOperator(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	opAID := h.createOperator(t, "filter-op-a")
	opBID := h.createOperator(t, "filter-op-b")

	resp, err := h.eventCli.ListEvents(ctx, connect.NewRequest(&nisv1.ListEventsRequest{
		Filter: &nisv1.EventFilter{
			OperatorId: opAID,
			Limit:      100,
		},
	}))
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}

	if len(resp.Msg.Events) == 0 {
		t.Fatal("expected events for operator A, got none")
	}
	for _, e := range resp.Msg.Events {
		if e.OperatorId != opAID {
			t.Errorf("event %s: want operator_id=%s, got %s", e.Type, opAID, e.OperatorId)
		}
		if e.OperatorId == opBID {
			t.Errorf("event %s: unexpectedly belongs to operator B", e.Type)
		}
	}
}

// TestEventsLog_CursorPagination verifies that cursor-based pagination works:
// a first page with limit=5 returns a non-empty next_cursor, and a second page
// returns additional events with no overlap.
func TestEventsLog_CursorPagination(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	// Three operators each emit ~4 events = ~12 total, enough for two pages of 5.
	h.createOperator(t, "page-op-1")
	h.createOperator(t, "page-op-2")
	h.createOperator(t, "page-op-3")

	resp1, err := h.eventCli.ListEvents(ctx, connect.NewRequest(&nisv1.ListEventsRequest{
		Filter: &nisv1.EventFilter{Limit: 5},
	}))
	if err != nil {
		t.Fatalf("ListEvents (page 1): %v", err)
	}
	if len(resp1.Msg.Events) != 5 {
		t.Fatalf("page 1: expected 5 events, got %d", len(resp1.Msg.Events))
	}
	if resp1.Msg.NextCursor == "" {
		t.Fatal("page 1 must return a non-empty next_cursor")
	}

	resp2, err := h.eventCli.ListEvents(ctx, connect.NewRequest(&nisv1.ListEventsRequest{
		Filter: &nisv1.EventFilter{
			Limit:  5,
			Cursor: resp1.Msg.NextCursor,
		},
	}))
	if err != nil {
		t.Fatalf("ListEvents (page 2): %v", err)
	}
	if len(resp2.Msg.Events) == 0 {
		t.Fatal("page 2 must have at least 1 event")
	}

	// No ID duplicates across pages.
	seen := make(map[string]bool, len(resp1.Msg.Events))
	for _, e := range resp1.Msg.Events {
		seen[e.Id] = true
	}
	for _, e := range resp2.Msg.Events {
		if seen[e.Id] {
			t.Errorf("duplicate event ID across pages: %s", e.Id)
		}
	}
}

// TestEventsLog_GetEvent verifies that GetEvent returns the exact same event
// found via ListEvents, and that a random UUID returns NotFound.
func TestEventsLog_GetEvent(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	opID := h.createOperator(t, "get-event-op")

	listResp, err := h.eventCli.ListEvents(ctx, connect.NewRequest(&nisv1.ListEventsRequest{
		Filter: &nisv1.EventFilter{
			OperatorId: opID,
			Types:      []string{"operator.created"},
			Limit:      1,
		},
	}))
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(listResp.Msg.Events) != 1 {
		t.Fatalf("expected 1 operator.created event, got %d", len(listResp.Msg.Events))
	}
	target := listResp.Msg.Events[0]

	getResp, err := h.eventCli.GetEvent(ctx, connect.NewRequest(&nisv1.GetEventRequest{
		Id: target.Id,
	}))
	if err != nil {
		t.Fatalf("GetEvent: %v", err)
	}
	if getResp.Msg.Event.Id != target.Id {
		t.Errorf("GetEvent ID mismatch: want %s, got %s", target.Id, getResp.Msg.Event.Id)
	}
	if getResp.Msg.Event.Type != target.Type {
		t.Errorf("GetEvent Type mismatch: want %s, got %s", target.Type, getResp.Msg.Event.Type)
	}
	if getResp.Msg.Event.OperatorId != target.OperatorId {
		t.Errorf("GetEvent OperatorId mismatch: want %s, got %s", target.OperatorId, getResp.Msg.Event.OperatorId)
	}

	// Non-existent ID must return NotFound.
	_, err = h.eventCli.GetEvent(ctx, connect.NewRequest(&nisv1.GetEventRequest{
		Id: uuid.New().String(),
	}))
	if err == nil {
		t.Fatal("expected NotFound for random UUID, got nil error")
	}
	if got := connect.CodeOf(err); got != connect.CodeNotFound {
		t.Errorf("expected CodeNotFound, got %v: %v", got, err)
	}
}

// TestEventsLog_AdminOnly verifies that operator-admin users cannot call
// ListEvents (the authz registry's RolePolicy gates event.read to admin only).
func TestEventsLog_AdminOnly(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	opID := h.createOperator(t, "events-rbac-op")

	const opAdminUser = "events-op-admin"
	const opAdminPass = "events-op-admin-password"
	if _, err := h.loginAs(t, adminUsername, adminPassword).authCli.CreateAPIUser(ctx, connect.NewRequest(&nisv1.CreateAPIUserRequest{
		Username:    opAdminUser,
		Password:    opAdminPass,
		Permissions: []string{"operator-admin"},
		OperatorId:  &opID,
	})); err != nil {
		t.Fatalf("CreateAPIUser(operator-admin): %v", err)
	}

	opAdmin := h.loginAs(t, opAdminUser, opAdminPass)

	_, err := opAdmin.eventCli.ListEvents(ctx, connect.NewRequest(&nisv1.ListEventsRequest{
		Filter: &nisv1.EventFilter{Limit: 10},
	}))
	if err == nil {
		t.Fatal("operator-admin should NOT be able to call ListEvents, but call succeeded")
	}
	if got := connect.CodeOf(err); got != connect.CodePermissionDenied {
		t.Errorf("expected CodePermissionDenied, got %v: %v", got, err)
	}
}

// TestEventsLog_DeleteEmitsOperatorEvent verifies that deleting an operator
// emits operator.deleted and that the original *.created events persist
// (events table is append-only).
//
// Cascade-delete events (account.deleted / user.deleted for the operator's
// $SYS account and system user) are NOT emitted today: the SQL FK CASCADE
// runs at the DB level and bypasses the service layer's emit. Audit-cascade
// is a v1.1 follow-up — `operator.deleted` carries enough info to reconstruct
// "everything under this operator was removed" for now.
func TestEventsLog_DeleteEmitsOperatorEvent(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	opID := h.createOperator(t, "delete-cascade-op")

	// Delete the operator (no clusters attached, so delete is allowed).
	if _, err := h.operatorCli.DeleteOperator(ctx, connect.NewRequest(&nisv1.DeleteOperatorRequest{
		Id: opID,
	})); err != nil {
		t.Fatalf("DeleteOperator: %v", err)
	}

	resp, err := h.eventCli.ListEvents(ctx, connect.NewRequest(&nisv1.ListEventsRequest{
		Filter: &nisv1.EventFilter{
			OperatorId: opID,
			Limit:      100,
		},
	}))
	if err != nil {
		t.Fatalf("ListEvents after delete: %v", err)
	}

	byType := make(map[string]int)
	for _, e := range resp.Msg.Events {
		byType[e.Type]++
	}

	for _, want := range []string{"operator.created", "operator.deleted"} {
		if byType[want] == 0 {
			t.Errorf("expected at least one %s event after operator delete, got none (all types: %v)", want, byType)
		}
	}
}

// TestEventsLog_DiffCapturedOnAccountUpdate (P1) verifies that updating an
// account emits an `account.updated` event whose diff_json carries the
// field-level before/after change set produced by events.DiffBuilder.
func TestEventsLog_DiffCapturedOnAccountUpdate(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	opID := h.createOperator(t, "p1-diff-op")
	accID := h.createAccount(t, opID, "p1-diff-acc")

	// Mutate the description (a benign user-mutable field).
	newDesc := "updated by P1 test"
	if _, err := h.accountCli.UpdateAccount(ctx, connect.NewRequest(&nisv1.UpdateAccountRequest{
		Id:          accID,
		Description: &newDesc,
	})); err != nil {
		t.Fatalf("UpdateAccount: %v", err)
	}

	resp, err := h.eventCli.ListEvents(ctx, connect.NewRequest(&nisv1.ListEventsRequest{
		Filter: &nisv1.EventFilter{
			OperatorId: opID,
			Types:      []string{"account.updated"},
			Limit:      10,
		},
	}))
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(resp.Msg.Events) != 1 {
		t.Fatalf("expected exactly 1 account.updated event, got %d", len(resp.Msg.Events))
	}
	evt := resp.Msg.Events[0]
	if evt.DiffJson == "" {
		t.Fatal("P1: expected non-empty diff_json on account.updated event, got empty")
	}

	var diff map[string][2]any
	if err := json.Unmarshal([]byte(evt.DiffJson), &diff); err != nil {
		t.Fatalf("unmarshal diff_json: %v\nraw: %s", err, evt.DiffJson)
	}
	pair, ok := diff["description"]
	if !ok {
		t.Fatalf("expected diff to contain 'description', got keys %v", keysOf(diff))
	}
	// Pre-mutation description was the empty string from createAccount.
	if pair[0] != "" {
		t.Errorf("description before: want \"\", got %v", pair[0])
	}
	if pair[1] != newDesc {
		t.Errorf("description after: want %q, got %v", newDesc, pair[1])
	}
}

// TestEventsLog_DiffEmptyOnNoOpUpdate (P1) verifies that an update RPC that
// changes nothing produces no audit-visible diff. The event itself may still
// be emitted with an empty diff_json, OR not emitted at all — the service
// short-circuits no-op updates. Either is fine; what matters is that diff_json
// is NOT populated with spurious entries when nothing actually changed.
func TestEventsLog_DiffEmptyOnNoOpUpdate(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	opID := h.createOperator(t, "p1-noop-op")
	accID := h.createAccount(t, opID, "p1-noop-acc")

	// Update with the same name → no change.
	sameName := "p1-noop-acc"
	if _, err := h.accountCli.UpdateAccount(ctx, connect.NewRequest(&nisv1.UpdateAccountRequest{
		Id:   accID,
		Name: &sameName,
	})); err != nil {
		t.Fatalf("UpdateAccount: %v", err)
	}

	resp, err := h.eventCli.ListEvents(ctx, connect.NewRequest(&nisv1.ListEventsRequest{
		Filter: &nisv1.EventFilter{
			OperatorId: opID,
			Types:      []string{"account.updated"},
			Limit:      10,
		},
	}))
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	for _, e := range resp.Msg.Events {
		if e.DiffJson == "" {
			continue
		}
		var diff map[string][2]any
		if err := json.Unmarshal([]byte(e.DiffJson), &diff); err != nil {
			t.Fatalf("unmarshal diff_json: %v", err)
		}
		if len(diff) > 0 {
			t.Errorf("P1 no-op update should not produce diff entries; got %v", diff)
		}
	}
}

// TestEventsLog_FilterByActor (P1) verifies that filtering by actor_type
// narrows results to events with that exact actor type.
func TestEventsLog_FilterByActor(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	// All actions in this harness run as the admin user → ActorType="user".
	_ = h.createOperator(t, "p1-actor-op")

	resp, err := h.eventCli.ListEvents(ctx, connect.NewRequest(&nisv1.ListEventsRequest{
		Filter: &nisv1.EventFilter{
			ActorType: "user",
			Limit:     50,
		},
	}))
	if err != nil {
		t.Fatalf("ListEvents(actor_type=user): %v", err)
	}
	if len(resp.Msg.Events) == 0 {
		t.Fatal("expected user-actor events, got none")
	}
	for _, e := range resp.Msg.Events {
		if e.ActorType != "user" {
			t.Errorf("actor_type filter leaked: got %s on event %s", e.ActorType, e.Type)
		}
	}

	// system filter should NOT match the admin-driven create above.
	respSys, err := h.eventCli.ListEvents(ctx, connect.NewRequest(&nisv1.ListEventsRequest{
		Filter: &nisv1.EventFilter{
			ActorType: "system",
			Types:     []string{"operator.created"},
			Limit:     10,
		},
	}))
	if err != nil {
		t.Fatalf("ListEvents(actor_type=system): %v", err)
	}
	if len(respSys.Msg.Events) > 0 {
		t.Errorf("expected no system-actor operator.created events; got %d", len(respSys.Msg.Events))
	}
}

// TestEventsLog_FilterByResourceID (P1) verifies the resource_id filter
// returns only events for that exact resource.
func TestEventsLog_FilterByResourceID(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	opID := h.createOperator(t, "p1-resid-op")
	_ = h.createAccount(t, opID, "p1-resid-other-acc")
	target := h.createAccount(t, opID, "p1-resid-target")

	resp, err := h.eventCli.ListEvents(ctx, connect.NewRequest(&nisv1.ListEventsRequest{
		Filter: &nisv1.EventFilter{
			ResourceId: target,
			Limit:      10,
		},
	}))
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(resp.Msg.Events) == 0 {
		t.Fatal("expected at least one event for the target resource, got none")
	}
	for _, e := range resp.Msg.Events {
		if e.ResourceId != target {
			t.Errorf("resource_id filter leaked: got %s on event %s", e.ResourceId, e.Type)
		}
	}
}

// TestEventsLog_SearchQ (P1) verifies the case-insensitive substring filter
// over `type` and `resource_id` (NOT payload, per P1 design).
func TestEventsLog_SearchQ(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	opID := h.createOperator(t, "p1-search-op")
	_ = h.createAccount(t, opID, "p1-search-acc")

	// "account.created" matches via the type column.
	resp, err := h.eventCli.ListEvents(ctx, connect.NewRequest(&nisv1.ListEventsRequest{
		Filter: &nisv1.EventFilter{
			OperatorId: opID,
			SearchQ:    "ACCOUNT.CREA",
			Limit:      10,
		},
	}))
	if err != nil {
		t.Fatalf("ListEvents(search_q): %v", err)
	}
	if len(resp.Msg.Events) == 0 {
		t.Fatal("expected at least one event matching ACCOUNT.CREA in type, got none")
	}
	for _, e := range resp.Msg.Events {
		if !strings.Contains(strings.ToLower(e.Type), "account.crea") {
			t.Errorf("search_q match leaked: type=%s does not contain 'account.crea'", e.Type)
		}
	}
}

// keysOf is a small test helper to make diff-key assertion failures readable.
func keysOf(m map[string][2]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
