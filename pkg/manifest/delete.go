package manifest

import (
	"context"
	"fmt"

	"connectrpc.com/connect"
	nisv1 "github.com/thomas-maurice/nis/gen/nis/v1"
)

// DeleteResult is the full output of DeleteAll.
type DeleteResult struct {
	Items []DeleteItem
}

// DeleteItem carries the per-object outcome of one delete dispatch.
type DeleteItem struct {
	Object  Object
	Outcome Outcome
	Note    string
}

// topoOrder maps each Kind to a sort rank for deletion (lower = deleted first).
// Templates sort BEFORE Operator (5) but AFTER ScopedSigningKey (1): a
// template can only be deleted once no SSK pins it, and the server
// enforces that via TemplateService.DeleteTemplate's dependents check.
var deleteOrder = map[string]int{
	KindUser:             0,
	KindScopedSigningKey: 1,
	KindAccount:          2,
	KindCluster:          3,
	KindTemplate:         4,
	KindOperator:         5,
}

// DeleteAll deletes every Object in batch in reverse topo order:
// User → ScopedSigningKey → Account → Cluster → Operator.
// Reserved names are refused. 404 on lookup → OutcomeNoop (already gone).
// First failure aborts the batch.
func DeleteAll(ctx context.Context, c PlannerClient, batch []Object) (*DeleteResult, error) {
	sorted := sortForDelete(batch)
	cache := newApplyCache()
	result := &DeleteResult{}

	for _, obj := range sorted {
		di, err := deleteOne(ctx, c, obj, cache)
		result.Items = append(result.Items, di)
		if err != nil {
			return result, err
		}
	}
	return result, nil
}

// sortForDelete returns a copy of batch ordered by deleteOrder (stable within each rank).
func sortForDelete(batch []Object) []Object {
	out := make([]Object, len(batch))
	copy(out, batch)
	// Stable insertion sort on the small N typical for manifests.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && deleteOrder[out[j].Kind] < deleteOrder[out[j-1].Kind]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func deleteOne(ctx context.Context, c PlannerClient, obj Object, cache *applyCache) (DeleteItem, error) {
	// Reserved-name check.
	switch obj.Kind {
	case KindAccount:
		if obj.Metadata.Name == "$SYS" {
			return DeleteItem{Object: obj, Outcome: OutcomeFailed, Note: "refusing to delete reserved entity"},
				fmt.Errorf("manifest delete: Account $SYS: refusing to delete reserved entity")
		}
	case KindUser:
		if obj.Metadata.Name == "system" {
			return DeleteItem{Object: obj, Outcome: OutcomeFailed, Note: "refusing to delete reserved entity"},
				fmt.Errorf("manifest delete: User system: refusing to delete reserved entity")
		}
	case KindScopedSigningKey:
		if obj.Metadata.Name == "default" {
			return DeleteItem{Object: obj, Outcome: OutcomeFailed, Note: "refusing to delete reserved entity"},
				fmt.Errorf("manifest delete: ScopedSigningKey default: refusing to delete reserved entity")
		}
	case KindTemplate:
		if obj.Metadata.Name == "default" || obj.Metadata.Name == "system" {
			return DeleteItem{Object: obj, Outcome: OutcomeFailed, Note: "refusing to delete reserved entity"},
				fmt.Errorf("manifest delete: Template %s: refusing to delete reserved entity", obj.Metadata.Name)
		}
	}

	switch obj.Kind {
	case KindOperator:
		return deleteOperator(ctx, c, obj, cache)
	case KindCluster:
		return deleteCluster(ctx, c, obj, cache)
	case KindAccount:
		return deleteAccount(ctx, c, obj, cache)
	case KindScopedSigningKey:
		return deleteScopedSigningKey(ctx, c, obj, cache)
	case KindUser:
		return deleteUser(ctx, c, obj, cache)
	case KindTemplate:
		return deleteTemplate(ctx, c, obj, cache)
	}
	return DeleteItem{Object: obj, Outcome: OutcomeFailed},
		fmt.Errorf("manifest delete: unknown kind %q", obj.Kind)
}

func deleteTemplate(ctx context.Context, c PlannerClient, obj Object, cache *applyCache) (DeleteItem, error) {
	opID, err := resolveDeleteOperatorID(ctx, c, obj.Metadata.Operator, cache)
	if err != nil {
		if connect.CodeOf(err) == connect.CodeNotFound {
			return DeleteItem{Object: obj, Outcome: OutcomeNoop, Note: "already gone (operator not found)"}, nil
		}
		return DeleteItem{Object: obj, Outcome: OutcomeFailed},
			fmt.Errorf("manifest delete: Template %s/%s: %w", obj.Metadata.Operator, obj.Metadata.Name, err)
	}
	lookupResp, err := c.TemplateClient().GetTemplateByName(ctx, connect.NewRequest(&nisv1.GetTemplateByNameRequest{
		OperatorId: opID,
		Name:       obj.Metadata.Name,
	}))
	if err != nil {
		if connect.CodeOf(err) == connect.CodeNotFound {
			return DeleteItem{Object: obj, Outcome: OutcomeNoop, Note: "already gone"}, nil
		}
		return DeleteItem{Object: obj, Outcome: OutcomeFailed},
			fmt.Errorf("manifest delete: Template %s/%s: GetTemplateByName: %w", obj.Metadata.Operator, obj.Metadata.Name, err)
	}
	tplID := lookupResp.Msg.GetTemplate().GetId()
	if _, err := c.TemplateClient().DeleteTemplate(ctx, connect.NewRequest(&nisv1.DeleteTemplateRequest{
		Id: tplID,
	})); err != nil {
		if connect.CodeOf(err) == connect.CodeNotFound {
			return DeleteItem{Object: obj, Outcome: OutcomeNoop, Note: "already gone"}, nil
		}
		// FailedPrecondition (template has dependents) surfaces here as
		// the apply RPC error; let the caller see the message verbatim.
		return DeleteItem{Object: obj, Outcome: OutcomeFailed},
			fmt.Errorf("manifest delete: Template %s/%s: DeleteTemplate: %w", obj.Metadata.Operator, obj.Metadata.Name, err)
	}
	return DeleteItem{Object: obj, Outcome: OutcomeApplied}, nil
}

func deleteOperator(ctx context.Context, c PlannerClient, obj Object, cache *applyCache) (DeleteItem, error) {
	lookupResp, err := c.OperatorClient().GetOperatorByName(ctx, connect.NewRequest(&nisv1.GetOperatorByNameRequest{
		Name:           obj.Metadata.Name,
		OrganizationId: c.TargetOrgID(),
	}))
	if err != nil {
		if connect.CodeOf(err) == connect.CodeNotFound {
			return DeleteItem{Object: obj, Outcome: OutcomeNoop, Note: "already gone"}, nil
		}
		return DeleteItem{Object: obj, Outcome: OutcomeFailed},
			fmt.Errorf("manifest delete: Operator %s: GetOperatorByName: %w", obj.Metadata.Name, err)
	}
	opID := lookupResp.Msg.GetOperator().GetId()
	cache.operatorByName[obj.Metadata.Name] = opID

	if _, err := c.OperatorClient().DeleteOperator(ctx, connect.NewRequest(&nisv1.DeleteOperatorRequest{
		Id: opID,
	})); err != nil {
		if connect.CodeOf(err) == connect.CodeNotFound {
			return DeleteItem{Object: obj, Outcome: OutcomeNoop, Note: "already gone"}, nil
		}
		return DeleteItem{Object: obj, Outcome: OutcomeFailed},
			fmt.Errorf("manifest delete: Operator %s: DeleteOperator: %w", obj.Metadata.Name, err)
	}
	return DeleteItem{Object: obj, Outcome: OutcomeApplied}, nil
}

func deleteCluster(ctx context.Context, c PlannerClient, obj Object, cache *applyCache) (DeleteItem, error) {
	opID, err := resolveDeleteOperatorID(ctx, c, obj.Metadata.Operator, cache)
	if err != nil {
		if connect.CodeOf(err) == connect.CodeNotFound {
			return DeleteItem{Object: obj, Outcome: OutcomeNoop, Note: "already gone (operator not found)"}, nil
		}
		return DeleteItem{Object: obj, Outcome: OutcomeFailed},
			fmt.Errorf("manifest delete: Cluster %s: %w", obj.Metadata.Name, err)
	}

	lookupResp, err := c.ClusterClient().GetClusterByName(ctx, connect.NewRequest(&nisv1.GetClusterByNameRequest{
		OperatorId: opID,
		Name:       obj.Metadata.Name,
	}))
	if err != nil {
		if connect.CodeOf(err) == connect.CodeNotFound {
			return DeleteItem{Object: obj, Outcome: OutcomeNoop, Note: "already gone"}, nil
		}
		return DeleteItem{Object: obj, Outcome: OutcomeFailed},
			fmt.Errorf("manifest delete: Cluster %s: GetClusterByName: %w", obj.Metadata.Name, err)
	}
	clusterID := lookupResp.Msg.GetCluster().GetId()

	if _, err := c.ClusterClient().DeleteCluster(ctx, connect.NewRequest(&nisv1.DeleteClusterRequest{
		Id: clusterID,
	})); err != nil {
		if connect.CodeOf(err) == connect.CodeNotFound {
			return DeleteItem{Object: obj, Outcome: OutcomeNoop, Note: "already gone"}, nil
		}
		return DeleteItem{Object: obj, Outcome: OutcomeFailed},
			fmt.Errorf("manifest delete: Cluster %s: DeleteCluster: %w", obj.Metadata.Name, err)
	}
	return DeleteItem{Object: obj, Outcome: OutcomeApplied}, nil
}

func deleteAccount(ctx context.Context, c PlannerClient, obj Object, cache *applyCache) (DeleteItem, error) {
	opID, err := resolveDeleteOperatorID(ctx, c, obj.Metadata.Operator, cache)
	if err != nil {
		if connect.CodeOf(err) == connect.CodeNotFound {
			return DeleteItem{Object: obj, Outcome: OutcomeNoop, Note: "already gone (operator not found)"}, nil
		}
		return DeleteItem{Object: obj, Outcome: OutcomeFailed},
			fmt.Errorf("manifest delete: Account %s/%s: %w", obj.Metadata.Operator, obj.Metadata.Name, err)
	}

	lookupResp, err := c.AccountClient().GetAccountByName(ctx, connect.NewRequest(&nisv1.GetAccountByNameRequest{
		OperatorId: opID,
		Name:       obj.Metadata.Name,
	}))
	if err != nil {
		if connect.CodeOf(err) == connect.CodeNotFound {
			return DeleteItem{Object: obj, Outcome: OutcomeNoop, Note: "already gone"}, nil
		}
		return DeleteItem{Object: obj, Outcome: OutcomeFailed},
			fmt.Errorf("manifest delete: Account %s/%s: GetAccountByName: %w", obj.Metadata.Operator, obj.Metadata.Name, err)
	}
	accID := lookupResp.Msg.GetAccount().GetId()
	cache.accountByPath[obj.Metadata.Operator+"/"+obj.Metadata.Name] = accID

	if _, err := c.AccountClient().DeleteAccount(ctx, connect.NewRequest(&nisv1.DeleteAccountRequest{
		Id: accID,
	})); err != nil {
		if connect.CodeOf(err) == connect.CodeNotFound {
			return DeleteItem{Object: obj, Outcome: OutcomeNoop, Note: "already gone"}, nil
		}
		return DeleteItem{Object: obj, Outcome: OutcomeFailed},
			fmt.Errorf("manifest delete: Account %s/%s: DeleteAccount: %w", obj.Metadata.Operator, obj.Metadata.Name, err)
	}
	return DeleteItem{Object: obj, Outcome: OutcomeApplied}, nil
}

func deleteScopedSigningKey(ctx context.Context, c PlannerClient, obj Object, cache *applyCache) (DeleteItem, error) {
	path := obj.Metadata.Operator + "/" + obj.Metadata.Account + "/" + obj.Metadata.Name

	accID, err := resolveDeleteAccountID(ctx, c, obj.Metadata.Operator, obj.Metadata.Account, cache)
	if err != nil {
		if connect.CodeOf(err) == connect.CodeNotFound {
			return DeleteItem{Object: obj, Outcome: OutcomeNoop, Note: "already gone (parent not found)"}, nil
		}
		return DeleteItem{Object: obj, Outcome: OutcomeFailed},
			fmt.Errorf("manifest delete: ScopedSigningKey %s: %w", path, err)
	}

	lookupResp, err := c.ScopedSigningKeyClient().GetScopedSigningKeyByName(ctx, connect.NewRequest(&nisv1.GetScopedSigningKeyByNameRequest{
		AccountId: accID,
		Name:      obj.Metadata.Name,
	}))
	if err != nil {
		if connect.CodeOf(err) == connect.CodeNotFound {
			return DeleteItem{Object: obj, Outcome: OutcomeNoop, Note: "already gone"}, nil
		}
		return DeleteItem{Object: obj, Outcome: OutcomeFailed},
			fmt.Errorf("manifest delete: ScopedSigningKey %s: GetScopedSigningKeyByName: %w", path, err)
	}
	sskID := lookupResp.Msg.GetKey().GetId()

	if _, err := c.ScopedSigningKeyClient().DeleteScopedSigningKey(ctx, connect.NewRequest(&nisv1.DeleteScopedSigningKeyRequest{
		Id: sskID,
	})); err != nil {
		if connect.CodeOf(err) == connect.CodeNotFound {
			return DeleteItem{Object: obj, Outcome: OutcomeNoop, Note: "already gone"}, nil
		}
		return DeleteItem{Object: obj, Outcome: OutcomeFailed},
			fmt.Errorf("manifest delete: ScopedSigningKey %s: DeleteScopedSigningKey: %w", path, err)
	}
	return DeleteItem{Object: obj, Outcome: OutcomeApplied}, nil
}

func deleteUser(ctx context.Context, c PlannerClient, obj Object, cache *applyCache) (DeleteItem, error) {
	path := obj.Metadata.Operator + "/" + obj.Metadata.Account + "/" + obj.Metadata.Name

	accID, err := resolveDeleteAccountID(ctx, c, obj.Metadata.Operator, obj.Metadata.Account, cache)
	if err != nil {
		if connect.CodeOf(err) == connect.CodeNotFound {
			return DeleteItem{Object: obj, Outcome: OutcomeNoop, Note: "already gone (parent not found)"}, nil
		}
		return DeleteItem{Object: obj, Outcome: OutcomeFailed},
			fmt.Errorf("manifest delete: User %s: %w", path, err)
	}

	lookupResp, err := c.UserClient().GetUserByName(ctx, connect.NewRequest(&nisv1.GetUserByNameRequest{
		AccountId: accID,
		Name:      obj.Metadata.Name,
	}))
	if err != nil {
		if connect.CodeOf(err) == connect.CodeNotFound {
			return DeleteItem{Object: obj, Outcome: OutcomeNoop, Note: "already gone"}, nil
		}
		return DeleteItem{Object: obj, Outcome: OutcomeFailed},
			fmt.Errorf("manifest delete: User %s: GetUserByName: %w", path, err)
	}
	userID := lookupResp.Msg.GetUser().GetId()

	if _, err := c.UserClient().DeleteUser(ctx, connect.NewRequest(&nisv1.DeleteUserRequest{
		Id: userID,
	})); err != nil {
		if connect.CodeOf(err) == connect.CodeNotFound {
			return DeleteItem{Object: obj, Outcome: OutcomeNoop, Note: "already gone"}, nil
		}
		return DeleteItem{Object: obj, Outcome: OutcomeFailed},
			fmt.Errorf("manifest delete: User %s: DeleteUser: %w", path, err)
	}
	return DeleteItem{Object: obj, Outcome: OutcomeApplied}, nil
}

// resolveDeleteOperatorID resolves an operator UUID using cache or GetOperatorByName.
// Returns a connect.CodeNotFound error if the operator doesn't exist.
func resolveDeleteOperatorID(ctx context.Context, c PlannerClient, opName string, cache *applyCache) (string, error) {
	if id, ok := cache.operatorByName[opName]; ok {
		return id, nil
	}
	resp, err := c.OperatorClient().GetOperatorByName(ctx, connect.NewRequest(&nisv1.GetOperatorByNameRequest{
		Name:           opName,
		OrganizationId: c.TargetOrgID(),
	}))
	if err != nil {
		return "", err
	}
	id := resp.Msg.GetOperator().GetId()
	cache.operatorByName[opName] = id
	return id, nil
}

// resolveDeleteAccountID resolves an account UUID using cache or GetAccountByName.
// Returns a connect.CodeNotFound error if the operator or account doesn't exist.
func resolveDeleteAccountID(ctx context.Context, c PlannerClient, opName, accName string, cache *applyCache) (string, error) {
	path := opName + "/" + accName
	if id, ok := cache.accountByPath[path]; ok {
		return id, nil
	}
	opID, err := resolveDeleteOperatorID(ctx, c, opName, cache)
	if err != nil {
		return "", err
	}
	resp, err := c.AccountClient().GetAccountByName(ctx, connect.NewRequest(&nisv1.GetAccountByNameRequest{
		OperatorId: opID,
		Name:       accName,
	}))
	if err != nil {
		return "", err
	}
	id := resp.Msg.GetAccount().GetId()
	cache.accountByPath[path] = id
	return id, nil
}
