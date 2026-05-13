package services

import (
	_ "embed"
	"fmt"
	"strings"

	"github.com/casbin/casbin/v2"
	"github.com/casbin/casbin/v2/model"
)

//go:embed casbin_model.conf
var casbinModelString string

//go:embed casbin_policy.csv
var casbinPolicyString string

// NewCasbinEnforcer constructs a Casbin enforcer using the model and policy embedded
// in the binary. Bundling the RBAC config into the binary means the server doesn't have
// to be launched from the repo root and the rule files can't drift from the code that
// references them.
func NewCasbinEnforcer() (*casbin.Enforcer, error) {
	m, err := model.NewModelFromString(casbinModelString)
	if err != nil {
		return nil, fmt.Errorf("parse casbin model: %w", err)
	}

	enforcer, err := casbin.NewEnforcer(m)
	if err != nil {
		return nil, fmt.Errorf("create casbin enforcer: %w", err)
	}

	var policies [][]string
	for _, line := range strings.Split(casbinPolicyString, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, ",")
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		if len(parts) < 4 || parts[0] != "p" {
			continue
		}
		policies = append(policies, parts[1:4])
	}

	if len(policies) > 0 {
		if _, err := enforcer.AddPolicies(policies); err != nil {
			return nil, fmt.Errorf("load casbin policies: %w", err)
		}
	}

	return enforcer, nil
}
