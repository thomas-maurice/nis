// Package client — styles.go is the single source of truth for nisctl's
// visual presentation. Color choices, status badges, entity coloring, and
// the key-value detail renderer all live here so command files don't grow
// their own one-off ANSI codes (see apply.go pre-2026-05-21 for what that
// looks like — local `\033[3Xm` constants that drifted from each other).
//
// Design notes:
//
//   - Colors are lipgloss AdaptiveColor (hex). The Light variant is used
//     when the terminal reports a light background, Dark on dark. Picking
//     hex over ANSI numeric codes ("9", "10") gets us identical output
//     across terminals that have differently-themed 16-color palettes,
//     and keeps the choices documentable.
//   - DisableColor(true) flips both renderers to termenv.Ascii. That
//     strips colors at render time even if Foreground() was set on a
//     style — we deliberately don't try to "unset" foregrounds on the
//     style itself because callers chain .Foreground(...) AFTER we
//     hand them the style, which would silently overwrite our unset.
//   - Two renderers (stdout + stderr) because their TTY state can
//     differ ("nisctl ... > file" leaves stderr a TTY).
package client

import (
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	nisv1 "github.com/thomas-maurice/nis/gen/nis/v1"
)

// ─────────────────────────────────────────────────────────────────────────────
// Palette
// ─────────────────────────────────────────────────────────────────────────────
//
// Status colors are tuned for readability on both light and dark terminals.
// Entity colors are picked to be mutually distinguishable while staying on
// the "informational" side of the spectrum — we don't want a user UUID
// looking like an error.

var (
	// Status palette.
	colorSuccess    = lipgloss.AdaptiveColor{Light: "#0E7C3A", Dark: "#7CE38B"} // green
	colorError      = lipgloss.AdaptiveColor{Light: "#B91C1C", Dark: "#FF6B6B"} // red
	colorWarning    = lipgloss.AdaptiveColor{Light: "#B45309", Dark: "#FFB454"} // amber
	colorInfo       = lipgloss.AdaptiveColor{Light: "#0369A1", Dark: "#7DC8FF"} // light blue
	colorMuted      = lipgloss.AdaptiveColor{Light: "#6B7280", Dark: "#9CA3AF"} // gray
	colorAccent     = lipgloss.AdaptiveColor{Light: "#1E40AF", Dark: "#82AAFF"} // header blue
	colorDeadLetter = lipgloss.AdaptiveColor{Light: "#86198F", Dark: "#F472B6"} // magenta
	colorRetry      = lipgloss.AdaptiveColor{Light: "#9333EA", Dark: "#C792EA"} // violet

	// Entity palette. Each NIS resource kind gets its own hue so a glance
	// at a row says "this column is an account, that one's an operator."
	// The IDs and the matching nkey public keys for a given resource use
	// the same color — they identify the same thing from two angles.
	colorOperator  = lipgloss.AdaptiveColor{Light: "#6D28D9", Dark: "#B388FF"} // purple
	colorAccount   = lipgloss.AdaptiveColor{Light: "#1D4ED8", Dark: "#82AAFF"} // blue
	colorUser      = lipgloss.AdaptiveColor{Light: "#15803D", Dark: "#C3E88D"} // green
	colorCluster   = lipgloss.AdaptiveColor{Light: "#C2410C", Dark: "#FFCB6B"} // amber
	colorScopedKey = lipgloss.AdaptiveColor{Light: "#BE185D", Dark: "#F78C6C"} // coral
	colorTemplate  = lipgloss.AdaptiveColor{Light: "#0E7490", Dark: "#89DDFF"} // cyan
	colorAPIUser   = lipgloss.AdaptiveColor{Light: "#0F766E", Dark: "#5EEAD4"} // teal
	colorAPIToken  = lipgloss.AdaptiveColor{Light: "#7E22CE", Dark: "#D8B4FE"} // light purple
	colorWebhook   = lipgloss.AdaptiveColor{Light: "#A16207", Dark: "#FCD34D"} // yellow
	colorBackup    = lipgloss.AdaptiveColor{Light: "#4D7C0F", Dark: "#BEF264"} // lime
	colorJob       = lipgloss.AdaptiveColor{Light: "#4338CA", Dark: "#A5B4FC"} // indigo
)

// ─────────────────────────────────────────────────────────────────────────────
// Renderers (TTY-aware, color toggling)
// ─────────────────────────────────────────────────────────────────────────────

var (
	stdoutRenderer *lipgloss.Renderer
	stderrRenderer *lipgloss.Renderer
	renderersOnce  sync.Once
)

func renderers() (*lipgloss.Renderer, *lipgloss.Renderer) {
	renderersOnce.Do(func() {
		stdoutRenderer = lipgloss.NewRenderer(os.Stdout)
		stderrRenderer = lipgloss.NewRenderer(os.Stderr)
	})
	return stdoutRenderer, stderrRenderer
}

// StdoutRenderer returns the lipgloss renderer bound to os.Stdout.
func StdoutRenderer() *lipgloss.Renderer {
	r, _ := renderers()
	return r
}

// StderrRenderer returns the lipgloss renderer bound to os.Stderr.
func StderrRenderer() *lipgloss.Renderer {
	_, r := renderers()
	return r
}

// DisableColor forces both renderers to ASCII (no ANSI). Wired to the
// --no-color flag in cmd/nisctl/commands/root.go. Also called when the
// output format is json/yaml/quiet so those streams stay byte-clean even
// on a TTY (e.g. `nisctl ... -o json | jq`).
//
// We set the color profile rather than trying to unset Foreground on
// every style because callers chain .Foreground(...) onto the returned
// style — an "unset" we apply first would be silently overwritten.
// Profile-level disabling strips at Render time regardless.
func DisableColor(disabled bool) {
	r1, r2 := renderers()
	if disabled {
		r1.SetColorProfile(termenv.Ascii)
		r2.SetColorProfile(termenv.Ascii)
	}
	// We never "re-enable" — the precondition is that DisableColor is
	// called exactly once during command startup, before any output is
	// produced. Toggling back would require remembering the auto-
	// detected profile, which isn't worth the complexity.
}

func sNew() lipgloss.Style { return StdoutRenderer().NewStyle() }
func eNew() lipgloss.Style { return StderrRenderer().NewStyle() }

// ─────────────────────────────────────────────────────────────────────────────
// Generic text styles
// ─────────────────────────────────────────────────────────────────────────────

// HeaderStyle is the table-header style: bold, accent-colored.
func HeaderStyle() lipgloss.Style { return sNew().Bold(true).Foreground(colorAccent) }

// DimStyle renders muted secondary text (timestamps in detail views, key
// columns in KV blocks, "this field is unset" hints).
func DimStyle() lipgloss.Style { return sNew().Faint(true) }

// SuccessStyle, ErrorStyle, WarningStyle, InfoStyle, MutedStyle render
// short pieces of free text in the matching status color. Bold is applied
// to error/success since they're the strongest signals.
func SuccessStyle() lipgloss.Style { return sNew().Foreground(colorSuccess).Bold(true) }
func ErrorStyle() lipgloss.Style   { return eNew().Foreground(colorError).Bold(true) }
func WarningStyle() lipgloss.Style { return sNew().Foreground(colorWarning) }
func InfoStyle() lipgloss.Style    { return sNew().Foreground(colorInfo) }
func MutedStyle() lipgloss.Style   { return sNew().Foreground(colorMuted) }

// Inline color shorthands. Kept for legacy callers in apply.go /
// delete_manifest.go that want a one-shot "color this fixed string."
// Prefer the named status / entity helpers for new code.
func Green(s string) string  { return sNew().Foreground(colorSuccess).Render(s) }
func Red(s string) string    { return sNew().Foreground(colorError).Render(s) }
func Yellow(s string) string { return sNew().Foreground(colorWarning).Render(s) }
func Gray(s string) string   { return sNew().Foreground(colorMuted).Render(s) }
func Cyan(s string) string   { return sNew().Foreground(colorInfo).Render(s) }

// ─────────────────────────────────────────────────────────────────────────────
// Named status styles
// ─────────────────────────────────────────────────────────────────────────────
//
// One style per status label. Use these when you need the style itself
// (e.g. to render multiple pieces of text in the same color, or set
// additional attributes). When you just want "color this status word,"
// use the *Badge helpers below — they're status-style-aware and bold-on
// for emphasis.

func StylePending() lipgloss.Style    { return sNew().Foreground(colorInfo).Bold(true) }
func StyleRunning() lipgloss.Style    { return sNew().Foreground(colorWarning).Bold(true) }
func StyleSucceeded() lipgloss.Style  { return sNew().Foreground(colorSuccess).Bold(true) }
func StyleFailed() lipgloss.Style     { return sNew().Foreground(colorError).Bold(true) }
func StyleDeadLetter() lipgloss.Style { return sNew().Foreground(colorDeadLetter).Bold(true) }
func StyleCancelled() lipgloss.Style  { return sNew().Foreground(colorMuted).Bold(true) }
func StyleRetried() lipgloss.Style    { return sNew().Foreground(colorRetry).Bold(true) }
func StyleActive() lipgloss.Style     { return sNew().Foreground(colorSuccess).Bold(true) }
func StyleRevoked() lipgloss.Style    { return sNew().Foreground(colorError).Bold(true) }
func StyleExpired() lipgloss.Style    { return sNew().Foreground(colorWarning).Bold(true) }
func StyleEnabled() lipgloss.Style    { return sNew().Foreground(colorSuccess).Bold(true) }
func StyleDisabled() lipgloss.Style   { return sNew().Foreground(colorMuted).Bold(true) }
func StyleHealthy() lipgloss.Style    { return sNew().Foreground(colorSuccess).Bold(true) }
func StyleUnhealthy() lipgloss.Style  { return sNew().Foreground(colorError).Bold(true) }

// Cluster-drift styles. These describe the relationship between NIS DB
// state and what's actually on a NATS resolver — color choices follow
// "is this actionable from NIS?" (good = green, fixable = yellow, drift
// outside our control = red, can't tell = gray).
func StyleInSync() lipgloss.Style              { return sNew().Foreground(colorSuccess).Bold(true) }
func StyleDBAhead() lipgloss.Style             { return sNew().Foreground(colorWarning).Bold(true) }
func StyleOutOfBand() lipgloss.Style           { return sNew().Foreground(colorError).Bold(true) }
func StyleMissingOnResolver() lipgloss.Style   { return sNew().Foreground(colorWarning).Bold(true) }
func StyleUnreachable() lipgloss.Style         { return sNew().Foreground(colorMuted).Bold(true) }

// ─────────────────────────────────────────────────────────────────────────────
// Entity styles
// ─────────────────────────────────────────────────────────────────────────────
//
// Each NIS resource kind gets a distinct color. ID and Key (nkey public
// key) for the same kind share the color — they're two views of the
// same identity.

func StyleOperator() lipgloss.Style  { return sNew().Foreground(colorOperator) }
func StyleAccount() lipgloss.Style   { return sNew().Foreground(colorAccount) }
func StyleUser() lipgloss.Style      { return sNew().Foreground(colorUser) }
func StyleCluster() lipgloss.Style   { return sNew().Foreground(colorCluster) }
func StyleScopedKey() lipgloss.Style { return sNew().Foreground(colorScopedKey) }
func StyleTemplate() lipgloss.Style  { return sNew().Foreground(colorTemplate) }
func StyleAPIUser() lipgloss.Style   { return sNew().Foreground(colorAPIUser) }
func StyleAPIToken() lipgloss.Style  { return sNew().Foreground(colorAPIToken) }
func StyleWebhook() lipgloss.Style   { return sNew().Foreground(colorWebhook) }
func StyleBackup() lipgloss.Style    { return sNew().Foreground(colorBackup) }
func StyleJob() lipgloss.Style       { return sNew().Foreground(colorJob) }

// Entity ID / key one-shot renderers. Empty input returns the empty
// string (not "-") so caller can decide on a placeholder.
//
// The Key variants are intended for nkey public keys (56-char base32
// blobs starting with the entity prefix: O for operator, A for account,
// U for user). They share the color with their matching ID renderer
// because color signals "this is the same kind of thing," not the
// specific value.

func OperatorID(id string) string  { return renderEntity(StyleOperator(), id) }
func OperatorKey(k string) string  { return renderEntity(StyleOperator(), k) }
func AccountID(id string) string   { return renderEntity(StyleAccount(), id) }
func AccountKey(k string) string   { return renderEntity(StyleAccount(), k) }
func UserID(id string) string      { return renderEntity(StyleUser(), id) }
func UserKey(k string) string      { return renderEntity(StyleUser(), k) }
func ClusterID(id string) string   { return renderEntity(StyleCluster(), id) }
func ScopedKeyID(id string) string { return renderEntity(StyleScopedKey(), id) }
func ScopedKeyKey(k string) string { return renderEntity(StyleScopedKey(), k) }
func TemplateID(id string) string  { return renderEntity(StyleTemplate(), id) }
func APIUserID(id string) string   { return renderEntity(StyleAPIUser(), id) }
func APITokenID(id string) string  { return renderEntity(StyleAPIToken(), id) }
func WebhookID(id string) string   { return renderEntity(StyleWebhook(), id) }
func BackupID(id string) string    { return renderEntity(StyleBackup(), id) }
func JobID(id string) string       { return renderEntity(StyleJob(), id) }

func renderEntity(st lipgloss.Style, v string) string {
	if v == "" {
		return ""
	}
	return st.Render(v)
}

// ─────────────────────────────────────────────────────────────────────────────
// Badges
// ─────────────────────────────────────────────────────────────────────────────

// Severity is a fallback for ad-hoc status labels that don't fit the
// named StyleXxx helpers below. Prefer the typed *Badge helpers (e.g.
// JobStatusBadge) over Severity-based Badge — they keep label text and
// color in lockstep at one place.
type Severity int

const (
	SeverityNeutral Severity = iota
	SeverityGood
	SeverityWarn
	SeverityBad
	SeverityInfo
	SeverityMuted
)

// Badge renders a label in the matching severity color, bold.
func Badge(label string, sev Severity) string {
	st := sNew().Bold(true)
	switch sev {
	case SeverityGood:
		st = st.Foreground(colorSuccess)
	case SeverityWarn:
		st = st.Foreground(colorWarning)
	case SeverityBad:
		st = st.Foreground(colorError)
	case SeverityInfo:
		st = st.Foreground(colorInfo)
	case SeverityMuted:
		st = st.Foreground(colorMuted)
	default:
		// SeverityNeutral: bold-only, no color.
	}
	return st.Render(label)
}

// JobStatusBadge colors a job status. Label strings match the lowercase
// forms used by the metrics layer so grep workflows survive ANSI strip.
func JobStatusBadge(s nisv1.JobStatus) string {
	switch s {
	case nisv1.JobStatus_JOB_STATUS_PENDING:
		return StylePending().Render("pending")
	case nisv1.JobStatus_JOB_STATUS_RUNNING:
		return StyleRunning().Render("running")
	case nisv1.JobStatus_JOB_STATUS_SUCCEEDED:
		return StyleSucceeded().Render("succeeded")
	case nisv1.JobStatus_JOB_STATUS_FAILED:
		return StyleFailed().Render("failed")
	case nisv1.JobStatus_JOB_STATUS_DEAD_LETTERED:
		return StyleDeadLetter().Render("dead_lettered")
	case nisv1.JobStatus_JOB_STATUS_CANCELLED:
		return StyleCancelled().Render("cancelled")
	default:
		return MutedStyle().Render("unknown")
	}
}

// DriftStatusBadge colors a cluster-drift status. Labels match
// driftStatusLabelFromProto's old output (kept for metric correlation).
func DriftStatusBadge(s nisv1.DriftStatus) string {
	switch s {
	case nisv1.DriftStatus_DRIFT_STATUS_IN_SYNC:
		return StyleInSync().Render("in_sync")
	case nisv1.DriftStatus_DRIFT_STATUS_DB_AHEAD:
		return StyleDBAhead().Render("db_ahead")
	case nisv1.DriftStatus_DRIFT_STATUS_OUT_OF_BAND:
		return StyleOutOfBand().Render("out_of_band")
	case nisv1.DriftStatus_DRIFT_STATUS_MISSING_ON_RESOLVER:
		return StyleMissingOnResolver().Render("missing_on_resolver")
	case nisv1.DriftStatus_DRIFT_STATUS_UNREACHABLE:
		return StyleUnreachable().Render("unreachable")
	}
	return MutedStyle().Render("unspecified")
}

// JetStreamProbeBadge colors a per-cluster JetStream probe result.
func JetStreamProbeBadge(s nisv1.JetStreamProbeStatus) string {
	switch s {
	case nisv1.JetStreamProbeStatus_JET_STREAM_PROBE_STATUS_OK:
		return StyleSucceeded().Render("ok")
	case nisv1.JetStreamProbeStatus_JET_STREAM_PROBE_STATUS_UNREACHABLE:
		return StyleUnreachable().Render("unreachable")
	case nisv1.JetStreamProbeStatus_JET_STREAM_PROBE_STATUS_NO_JETSTREAM:
		return MutedStyle().Render("no-jetstream")
	case nisv1.JetStreamProbeStatus_JET_STREAM_PROBE_STATUS_ACCOUNT_NOT_FOUND:
		return StyleMissingOnResolver().Render("account-not-found")
	case nisv1.JetStreamProbeStatus_JET_STREAM_PROBE_STATUS_NOT_ACTIVATED:
		return InfoStyle().Render("not-activated")
	case nisv1.JetStreamProbeStatus_JET_STREAM_PROBE_STATUS_ERROR:
		return StyleFailed().Render("error")
	}
	return MutedStyle().Render("unknown")
}

// HealthBadge renders a cluster's healthy flag.
func HealthBadge(healthy bool) string {
	if healthy {
		return StyleHealthy().Render("healthy")
	}
	return StyleUnhealthy().Render("unhealthy")
}

// BoolBadge renders a generic yes/no flag. trueLabel is the text when
// v is true (e.g. "enabled"); falseLabel for false (e.g. "disabled").
// True maps to success color, false to muted — flip the labels rather
// than the colors if your domain wants the opposite (e.g. "drifted").
func BoolBadge(v bool, trueLabel, falseLabel string) string {
	if v {
		return StyleEnabled().Render(trueLabel)
	}
	return StyleDisabled().Render(falseLabel)
}

// APITokenStatusBadge colors the lifecycle state of an API token —
// active / expired / revoked. Centralized here so the four call sites
// (table, detail, RPC error mapping) agree on label text.
func APITokenStatusBadge(revoked, expired bool) string {
	switch {
	case revoked:
		return StyleRevoked().Render("revoked")
	case expired:
		return StyleExpired().Render("expired")
	default:
		return StyleActive().Render("active")
	}
}

// WebhookDeliveryStatusBadge colors a delivery status string. The
// webhook substrate uses string statuses (not an enum), so we accept
// the raw value and lowercase-match on the closed set.
func WebhookDeliveryStatusBadge(status string) string {
	switch strings.ToLower(status) {
	case "succeeded":
		return StyleSucceeded().Render(status)
	case "pending", "queued":
		return StylePending().Render(status)
	case "failed":
		return StyleFailed().Render(status)
	case "dead_letter", "dead-letter", "dead_lettered":
		return StyleDeadLetter().Render(status)
	default:
		return MutedStyle().Render(status)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// KV detail-view renderer
// ─────────────────────────────────────────────────────────────────────────────

// KVPair is one row in a detail view (e.g. `nisctl job get`).
// Value may already contain styled content (e.g. a Badge).
type KVPair struct {
	Key   string
	Value string
}

// RenderKV formats a list of key-value pairs with right-padded dim keys.
// The pad width is computed from the longest key so columns align.
//
//	ID:             abc-123
//	Status:         pending
//	Attempts:       0 / 3
//
// Empty values render as "-". Multi-line values are indented under the
// value column.
func RenderKV(pairs []KVPair) string {
	keyWidth := 0
	for _, p := range pairs {
		if l := len(p.Key); l > keyWidth {
			keyWidth = l
		}
	}
	keyStyle := DimStyle()
	var sb strings.Builder
	for _, p := range pairs {
		key := keyStyle.Render(fmt.Sprintf("%-*s", keyWidth+1, p.Key+":"))
		val := p.Value
		if val == "" {
			val = DimStyle().Render("-")
		}
		if strings.Contains(val, "\n") {
			lines := strings.Split(val, "\n")
			sb.WriteString(key)
			sb.WriteString(" ")
			sb.WriteString(lines[0])
			sb.WriteString("\n")
			pad := strings.Repeat(" ", keyWidth+2)
			for _, line := range lines[1:] {
				sb.WriteString(pad)
				sb.WriteString(line)
				sb.WriteString("\n")
			}
		} else {
			sb.WriteString(key)
			sb.WriteString(" ")
			sb.WriteString(val)
			sb.WriteString("\n")
		}
	}
	return sb.String()
}
