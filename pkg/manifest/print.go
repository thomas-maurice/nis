package manifest

import (
	"fmt"
	"io"
	"strings"
)

// PrintOptions controls the output of Print.
type PrintOptions struct {
	Color bool
	W     io.Writer
}

const (
	ansiGreen  = "\033[32m"
	ansiYellow = "\033[33m"
	ansiGray   = "\033[90m"
	ansiRed    = "\033[31m"
	ansiReset  = "\033[0m"
)

// errWriter collects the first write error so callers can check once at the end.
type errWriter struct {
	w   io.Writer
	err error
}

func (e *errWriter) printf(format string, args ...any) {
	if e.err != nil {
		return
	}
	_, e.err = fmt.Fprintf(e.w, format, args...)
}

// Print renders a terraform-style plan to opts.W.
func Print(plan *PlanResult, opts PrintOptions) error {
	if opts.W == nil {
		return fmt.Errorf("manifest print: W must not be nil")
	}

	ew := &errWriter{w: opts.W}

	color := func(code, s string) string {
		if !opts.Color {
			return s
		}
		return code + s + ansiReset
	}

	s := plan.Summary
	ew.printf("Plan: %d to add, %d to change, %d to leave unchanged.\n\n",
		s.Create, s.Update, s.Noop)

	// Creates first, then updates, then noops — matching terraform convention.
	printGroup(ew, plan.Items, ActionCreate, color)
	printGroup(ew, plan.Items, ActionUpdate, color)
	printGroup(ew, plan.Items, ActionNoop, color)

	return ew.err
}

func printGroup(ew *errWriter, items []PlanItem, action Action, color func(string, string) string) {
	for _, item := range items {
		if item.Action != action {
			continue
		}
		switch action {
		case ActionCreate:
			printCreate(ew, item, color)
		case ActionUpdate:
			printUpdate(ew, item, color)
		case ActionNoop:
			printNoop(ew, item, color)
		}
	}
}

func printCreate(ew *errWriter, item PlanItem, color func(string, string) string) {
	glyph := color(ansiGreen, "+")
	ew.printf("  %s %s/%s%s\n", glyph, item.Object.Kind, item.Object.Metadata.Name, parentSuffix(item.Object))
	if item.Note != "" {
		ew.printf("      %s\n", color(ansiGray, item.Note))
	}
}

func printUpdate(ew *errWriter, item PlanItem, color func(string, string) string) {
	glyph := color(ansiYellow, "~")
	ew.printf("  %s %s/%s%s\n", glyph, item.Object.Kind, item.Object.Metadata.Name, parentSuffix(item.Object))
	for _, op := range item.Updates {
		for _, f := range op.Fields {
			oldVal, newVal := fieldValues(item.Object, f)
			if oldVal != "" || newVal != "" {
				ew.printf("      %s %s: %s -> %s\n",
					color(ansiYellow, "~"), f,
					color(ansiRed, oldVal),
					color(ansiGreen, newVal))
			} else {
				ew.printf("      %s %s\n", color(ansiYellow, "~"), f)
			}
		}
		ew.printf("        (%s)\n", color(ansiGray, op.RPC))
	}
	if item.Note != "" {
		ew.printf("      %s\n", color(ansiYellow, item.Note))
	}
}

func printNoop(ew *errWriter, item PlanItem, color func(string, string) string) {
	glyph := color(ansiGray, "=")
	ew.printf("  %s %s/%s%s", glyph, item.Object.Kind, item.Object.Metadata.Name, parentSuffix(item.Object))
	if item.Note != "" {
		ew.printf("  %s", color(ansiYellow, "("+item.Note+")"))
	}
	ew.printf("\n")
}

// parentSuffix returns the "  (operator: X)" / "  (operator: X, account: Y)" annotation.
func parentSuffix(obj Object) string {
	var parts []string
	if obj.Metadata.Operator != "" {
		parts = append(parts, "operator: "+obj.Metadata.Operator)
	}
	if obj.Metadata.Account != "" {
		parts = append(parts, "account: "+obj.Metadata.Account)
	}
	if len(parts) == 0 {
		return ""
	}
	return "  (" + strings.Join(parts, ", ") + ")"
}

// fieldValues returns human-readable new values for a named field from a manifest
// Object. old is always empty — the planner doesn't hold server-side values
// at print time. Values are display hints only.
func fieldValues(obj Object, field string) (old, new string) {
	switch obj.Kind {
	case KindAccount:
		if obj.Account == nil {
			return "", ""
		}
		switch field {
		case "description":
			return "", obj.Account.Description
		case "jetStream.enabled":
			if obj.Account.JetStream != nil {
				return "", fmt.Sprintf("%v", obj.Account.JetStream.Enabled)
			}
		case "jetStream.maxMemory":
			if obj.Account.JetStream != nil {
				return "", formatSize(obj.Account.JetStream.MaxMemory)
			}
		case "jetStream.maxStorage":
			if obj.Account.JetStream != nil {
				return "", formatSize(obj.Account.JetStream.MaxStorage)
			}
		case "jetStream.maxStreams":
			if obj.Account.JetStream != nil {
				return "", fmt.Sprintf("%d", obj.Account.JetStream.MaxStreams)
			}
		case "jetStream.maxConsumers":
			if obj.Account.JetStream != nil {
				return "", fmt.Sprintf("%d", obj.Account.JetStream.MaxConsumers)
			}
		}
	case KindOperator:
		if obj.Operator == nil {
			return "", ""
		}
		switch field {
		case "description":
			return "", obj.Operator.Description
		case "jwtPolicy.userJWTTTL":
			if obj.Operator.JWTPolicy != nil {
				return "", obj.Operator.JWTPolicy.UserJWTTTL.String()
			}
		case "jwtPolicy.accountJWTTTL":
			if obj.Operator.JWTPolicy != nil {
				return "", obj.Operator.JWTPolicy.AccountJWTTTL.String()
			}
		case "jwtPolicy.warnWindow":
			if obj.Operator.JWTPolicy != nil {
				return "", obj.Operator.JWTPolicy.WarnWindow.String()
			}
		case "jwtPolicy.autoRenew":
			if obj.Operator.JWTPolicy != nil {
				return "", fmt.Sprintf("%v", obj.Operator.JWTPolicy.AutoRenew)
			}
		}
	case KindUser:
		if obj.User == nil {
			return "", ""
		}
		switch field {
		case "description":
			return "", obj.User.Description
		case "jwtTTL":
			if obj.User.JWTTTL != nil {
				return "", (*obj.User.JWTTTL).String()
			}
		}
	case KindScopedSigningKey:
		if obj.ScopedSigningKey == nil {
			return "", ""
		}
		switch field {
		case "description":
			return "", obj.ScopedSigningKey.Description
		case "responseTTL":
			return "", obj.ScopedSigningKey.ResponseTTL.String()
		case "responseMaxMsgs":
			return "", fmt.Sprintf("%d", obj.ScopedSigningKey.ResponseMaxMsgs)
		}
	}
	return "", ""
}
