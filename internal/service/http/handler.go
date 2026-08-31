// Package httpsvc is the OpenAPI HTTP surface. It depends only on usecase.
package httpsvc

import (
	"errors"
	"fmt"
	"html"
	"net/http"

	"github.com/truewebber/eve-online-mcp/internal/usecase/oauth"
)

// API implements the generated ServerInterface.
type API struct {
	OAuth *oauth.Server
	Host  oauth.Host
}

func New(oauthServer *oauth.Server, host oauth.Host) *API {
	return &API{OAuth: oauthServer, Host: host}
}

func (h *API) GetIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)

		return
	}
	body := fmt.Sprintf(`
			<h1>EVE MCP</h1>
			<p>Add this URL in Cursor or Claude Code. The client will show <b>Authentication required</b> and send you to the EVE login.</p>
			<p>MCP endpoint: <code>%s</code></p>
			<p class=dim>EVE callback must be exactly <code>%s</code>.</p>
			<p class=dim>Writes: confirm (mail cap 5/hour)</p>`,
		html.EscapeString(h.OAuth.ResourceURL()),
		html.EscapeString(h.Host.CallbackURL))
	page(w, "EVE MCP", body)
}

func (h *API) GetAuthLogin(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, h.Host.BaseURL()+"/oauth/authorize", http.StatusFound)
}

func (h *API) GetAuthCallback(w http.ResponseWriter, r *http.Request, _ GetAuthCallbackParams) {
	if errS := r.URL.Query().Get("error"); errS != "" {
		detail := r.URL.Query().Get("error_description")
		if detail == "" {
			detail = errS
		}
		pageStatus(w, 400, "Login refused", `<p class=warn>`+html.EscapeString(detail)+`</p>`)

		return
	}
	code, state := r.URL.Query().Get("code"), r.URL.Query().Get("state")
	if code == "" || state == "" {
		pageStatus(w, 400, "Bad callback", `<p class=warn>Missing code or state.</p>`)

		return
	}
	cb, err := h.OAuth.CompleteCallback(r.Context(), code, state)
	if err != nil {
		title := "Login failed"
		detail := err.Error()
		if errors.As(err, new(oauth.CharacterOwnedError)) {
			title = "Character already linked"
		}
		if errors.Is(err, oauth.ErrUnknownLogin) {
			detail = "Unknown or expired login state — start the login again."
		}
		pageStatus(w, 400, title, `<p class=warn>`+html.EscapeString(detail)+`</p>`)

		return
	}
	if cb.Redirect != "" {
		http.Redirect(w, r, cb.Redirect, http.StatusFound)

		return
	}
	page(w, "EVE MCP", fmt.Sprintf(`
			<h1>%s is authorized</h1>
			<p class=dim>%d scopes · character #%d</p>
			<p>You can close this tab. Cursor and Claude Code can now read this character.</p>
			<p><a class=btn href="/">Back to status</a></p>`,
		html.EscapeString(cb.Token.CharacterName), len(cb.Token.Scopes), cb.Token.CharacterID))
}

func (h *API) GetProtectedResourceMetadata(w http.ResponseWriter, r *http.Request) {
	h.OAuth.ProtectedResourceHandler().ServeHTTP(w, r)
}

func (h *API) GetAuthorizationServerMetadata(w http.ResponseWriter, r *http.Request) {
	h.OAuth.ServeASMeta(w, r)
}

func (h *API) PostOAuthRegister(w http.ResponseWriter, r *http.Request) {
	h.OAuth.ServeRegister(w, r)
}

func (h *API) GetOAuthAuthorize(w http.ResponseWriter, r *http.Request, _ GetOAuthAuthorizeParams) {
	h.OAuth.ServeAuthorize(w, r)
}

func (h *API) PostOAuthToken(w http.ResponseWriter, r *http.Request) {
	h.OAuth.ServeToken(w, r)
}
