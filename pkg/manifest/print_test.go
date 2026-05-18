package manifest

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func makePrintPlan() *PlanResult {
	ttl := 24 * time.Hour
	return &PlanResult{
		Summary: Summary{Create: 2, Update: 1, Noop: 2},
		Items: []PlanItem{
			{
				Object: Object{
					TypeMeta: TypeMeta{Kind: KindOperator},
					Metadata: ObjectMeta{Name: "acme"},
					Operator: &OperatorSpec{Description: "new desc"},
				},
				Action: ActionCreate,
			},
			{
				Object: Object{
					TypeMeta: TypeMeta{Kind: KindAccount},
					Metadata: ObjectMeta{Name: "payments", Operator: "acme"},
					Account:  &AccountSpec{Description: "payments account"},
				},
				Action: ActionCreate,
				Note:   "parent Operator is new",
			},
			{
				Object: Object{
					TypeMeta: TypeMeta{Kind: KindAccount},
					Metadata: ObjectMeta{Name: "billing", Operator: "acme"},
					Account: &AccountSpec{
						Description: "billing account",
						JetStream:   &JetStreamSpec{Enabled: true, MaxMemory: 2 << 30},
					},
				},
				Action:     ActionUpdate,
				ExistingID: uuid.MustParse("22222222-2222-2222-2222-222222222222"),
				Updates: []UpdateOp{
					{RPC: "UpdateAccount", Fields: []string{"description"}},
					{RPC: "UpdateJetStreamLimits", Fields: []string{"jetStream.maxMemory"}},
				},
			},
			{
				Object: Object{
					TypeMeta: TypeMeta{Kind: KindOperator},
					Metadata: ObjectMeta{Name: "legacy"},
					Operator: &OperatorSpec{},
				},
				Action:     ActionNoop,
				ExistingID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
			},
			{
				Object: Object{
					TypeMeta: TypeMeta{Kind: KindCluster},
					Metadata: ObjectMeta{Name: "prod", Operator: "acme"},
					Cluster:  &ClusterSpec{ServerURLs: []string{"nats://new:4222"}},
				},
				Action:     ActionNoop,
				ExistingID: uuid.MustParse("55555555-5555-5555-5555-555555555555"),
				Note:       "cluster updates via manifest are not supported in v1; use 'nisctl cluster update'",
			},
			{
				Object: Object{
					TypeMeta: TypeMeta{Kind: KindUser},
					Metadata: ObjectMeta{Name: "svc", Operator: "acme", Account: "billing"},
					User:     &UserSpec{Description: "new desc", JWTTTL: &ttl},
				},
				Action:     ActionUpdate,
				ExistingID: uuid.MustParse("44444444-4444-4444-4444-444444444444"),
				Updates: []UpdateOp{
					{RPC: "UpdateUser", Fields: []string{"description", "jwtTTL"}},
				},
			},
		},
	}
}

func TestPrint_NoColor(t *testing.T) {
	plan := makePrintPlan()
	var sb strings.Builder
	err := Print(plan, PrintOptions{Color: false, W: &sb})
	if err != nil {
		t.Fatalf("Print: %v", err)
	}
	out := sb.String()

	// Summary line
	if !strings.Contains(out, "Plan: 2 to add, 1 to change, 2 to leave unchanged.") {
		t.Errorf("summary line missing, got:\n%s", out)
	}
	// Creates
	if !strings.Contains(out, "+ Operator/acme") {
		t.Errorf("expected '+ Operator/acme', got:\n%s", out)
	}
	if !strings.Contains(out, "+ Account/payments") {
		t.Errorf("expected '+ Account/payments', got:\n%s", out)
	}
	// Update
	if !strings.Contains(out, "~ Account/billing") {
		t.Errorf("expected '~ Account/billing', got:\n%s", out)
	}
	if !strings.Contains(out, "description") {
		t.Errorf("expected field 'description' in update, got:\n%s", out)
	}
	if !strings.Contains(out, "UpdateAccount") {
		t.Errorf("expected 'UpdateAccount' RPC label, got:\n%s", out)
	}
	if !strings.Contains(out, "UpdateJetStreamLimits") {
		t.Errorf("expected 'UpdateJetStreamLimits' RPC label, got:\n%s", out)
	}
	// Noop
	if !strings.Contains(out, "= Operator/legacy") {
		t.Errorf("expected '= Operator/legacy', got:\n%s", out)
	}
	// Cluster noop with note
	if !strings.Contains(out, "= Cluster/prod") {
		t.Errorf("expected '= Cluster/prod', got:\n%s", out)
	}
	if !strings.Contains(out, "cluster updates via manifest are not supported") {
		t.Errorf("expected cluster noop note, got:\n%s", out)
	}
	// No ANSI codes when color=false
	if strings.Contains(out, "\033[") {
		t.Errorf("found ANSI escape in no-color output:\n%s", out)
	}
}

func TestPrint_Color(t *testing.T) {
	plan := makePrintPlan()
	var sb strings.Builder
	err := Print(plan, PrintOptions{Color: true, W: &sb})
	if err != nil {
		t.Fatalf("Print: %v", err)
	}
	out := sb.String()

	// ANSI codes must be present when color=true
	if !strings.Contains(out, "\033[") {
		t.Errorf("expected ANSI escapes in colored output, got none")
	}
	// Green + for creates
	if !strings.Contains(out, ansiGreen+"+"+ansiReset) {
		t.Errorf("expected green '+' glyph, got:\n%s", out)
	}
	// Yellow ~ for updates
	if !strings.Contains(out, ansiYellow+"~"+ansiReset) {
		t.Errorf("expected yellow '~' glyph, got:\n%s", out)
	}
	// Gray = for noops
	if !strings.Contains(out, ansiGray+"="+ansiReset) {
		t.Errorf("expected gray '=' glyph, got:\n%s", out)
	}
}

func TestPrint_ParentAnnotations(t *testing.T) {
	plan := &PlanResult{
		Summary: Summary{Create: 1},
		Items: []PlanItem{
			{
				Object: Object{
					TypeMeta: TypeMeta{Kind: KindUser},
					Metadata: ObjectMeta{Name: "alice", Operator: "acme", Account: "billing"},
					User:     &UserSpec{},
				},
				Action: ActionCreate,
			},
		},
	}
	var sb strings.Builder
	if err := Print(plan, PrintOptions{W: &sb}); err != nil {
		t.Fatal(err)
	}
	out := sb.String()
	if !strings.Contains(out, "operator: acme") {
		t.Errorf("expected 'operator: acme', got:\n%s", out)
	}
	if !strings.Contains(out, "account: billing") {
		t.Errorf("expected 'account: billing', got:\n%s", out)
	}
}

func TestPrint_NilWriter(t *testing.T) {
	err := Print(&PlanResult{}, PrintOptions{W: nil})
	if err == nil {
		t.Fatal("expected error for nil writer")
	}
}
