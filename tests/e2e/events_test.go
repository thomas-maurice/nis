//go:build e2e

// events_test.go — end-to-end tests for the EventService (audit log).
// All tests are admin-only or verify the admin-only RBAC gate.
// No NATS is needed; the event substrate is DB-only.
package e2e

import (
	"context"
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
