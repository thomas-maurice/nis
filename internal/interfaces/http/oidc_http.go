package http

import (
	"errors"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/thomas-maurice/nis/internal/application/services"
)

// OIDCHandler holds the two HTTP handlers for the OIDC login flow.
// It lives in the http package (outside internal/interfaces/grpc/handlers/)
// so the AST authz lint does not misidentify these as Connect-RPC handlers
// and require Procedures registry entries.
type OIDCHandler struct {
	ssoService *services.SSOService
	publicURL  string
	verifierFn services.VerifierFactory
}

// NewOIDCHandler creates an OIDCHandler using the production VerifierFactory.
func NewOIDCHandler(ssoService *services.SSOService, publicURL string) *OIDCHandler {
	return &OIDCHandler{
		ssoService: ssoService,
		publicURL:  publicURL,
		verifierFn: services.RealVerifierFactory,
	}
}

// ServeStart handles GET /auth/oidc/start?org=<slug>&redirect=<optional>.
//
// On success it issues a 302 to the IdP authorization URL.
// On any error it adds a jittered delay (150–450 ms) before returning a
// generic 400. The jitter normalizes the timing difference between a valid
// slug (discovery round-trip) and an invalid one (fast DB miss) to blunt
// timing-based org-slug enumeration (see DESIGN.md §4.4/§5.5).
func (h *OIDCHandler) ServeStart(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimSpace(r.URL.Query().Get("org"))
	if slug == "" {
		h.errorWithJitter(w)
		return
	}

	var redirectAfter *string
	if ra := r.URL.Query().Get("redirect"); ra != "" {
		redirectAfter = &ra
	}

	authURL, err := h.ssoService.StartLogin(r.Context(), slug, redirectAfter)
	if err != nil {
		h.errorWithJitter(w)
		return
	}

	http.Redirect(w, r, authURL, http.StatusFound)
}

// ServeCallback handles GET /auth/oidc/callback?code=<code>&state=<state>.
//
// On success it issues a 302 to the SPA /login/callback route with the NIS
// session JWT in the URL fragment (#token=...). The SPA must call
// history.replaceState to strip the fragment immediately after reading it
// (see DESIGN.md §4.6 / security consideration S1).
// On any error it returns a generic 400 with no detail.
func (h *OIDCHandler) ServeCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	// The IdP signals a failed authorization by redirecting here with an
	// OAuth2 error response (RFC 6749 §4.1.2.1): ?error=<code>[&error_description=...]
	// and no authorization code. Surface it to the SPA login screen instead of
	// returning an opaque 400 — the description is the IdP's own explanation
	// (e.g. "invalid_request: The request is otherwise malformed") and is what
	// the operator needs to debug their IdP-side provider config.
	if idpErr := q.Get("error"); idpErr != "" {
		h.redirectToSPAError(w, r, idpErr, q.Get("error_description"))
		return
	}

	code := q.Get("code")
	state := q.Get("state")
	if code == "" || state == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	sessionJWT, redirectAfter, err := h.ssoService.CompleteLogin(r.Context(), code, state, h.verifierFn)
	if err != nil {
		if errors.Is(err, services.ErrSSODenied) || errors.Is(err, services.ErrSSOUnavailable) {
			http.Error(w, "authentication failed", http.StatusBadRequest)
			return
		}
		http.Error(w, "authentication failed", http.StatusBadRequest)
		return
	}

	// JWT lives in the URL fragment; it is never sent to the server in Referer
	// headers on subsequent navigations.
	fragment := "token=" + url.QueryEscape(sessionJWT)
	if redirectAfter != nil && *redirectAfter != "" {
		fragment += "&redirect=" + url.QueryEscape(*redirectAfter)
	}
	dest := h.publicURL + "/login/callback#" + fragment
	http.Redirect(w, r, dest, http.StatusFound)
}

// redirectToSPAError sends the browser to the SPA login-callback route with the
// error carried in the URL fragment (#error=...&error_description=...). The SPA
// reads it and renders the reason. Values are URL-encoded; the SPA renders them
// as text (Vue escapes interpolation), so reflecting the IdP-supplied
// description is safe against XSS.
func (h *OIDCHandler) redirectToSPAError(w http.ResponseWriter, r *http.Request, code, desc string) {
	frag := "error=" + url.QueryEscape(code)
	if desc != "" {
		frag += "&error_description=" + url.QueryEscape(desc)
	}
	http.Redirect(w, r, h.publicURL+"/login/callback#"+frag, http.StatusFound)
}

// errorWithJitter writes a generic 400 after a random 150–450 ms sleep.
func (h *OIDCHandler) errorWithJitter(w http.ResponseWriter) {
	jitter := time.Duration(150+rand.IntN(301)) * time.Millisecond
	time.Sleep(jitter)
	http.Error(w, "sso unavailable", http.StatusBadRequest)
}
