package httpsvc

import (
	"errors"
	"fmt"
	"html"
	"net/http"

	"github.com/truewebber/gopkg/log"

	"github.com/truewebber/eve-online-mcp/internal/usecase/oauth"
)

const githubRepoURL = "https://github.com/truewebber/eve-online-mcp"

type API struct {
	OAuth  *oauth.Server
	Host   oauth.Host
	Logger log.Logger
}

func New(oauthServer *oauth.Server, host oauth.Host, logger log.Logger) (*API, error) {
	if oauthServer == nil {
		return nil, errOAuthRequired
	}
	if host.PublicURL == "" || host.MCPPath == "" || host.CallbackURL == "" {
		return nil, errHostRequired
	}
	if logger == nil {
		return nil, errLoggerRequired
	}

	return &API{OAuth: oauthServer, Host: host, Logger: logger}, nil
}

func (h *API) Mount(mux ServeMux) {
	HandlerWithOptions(h, StdHTTPServerOptions{
		BaseRouter:       mux,
		ErrorHandlerFunc: h.bindError,
	})
}

func (h *API) GetIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)

		return
	}
	repo := html.EscapeString(githubRepoURL)
	body := fmt.Sprintf(`
			<h1>EVE MCP</h1>
			<p>Add this URL in Cursor or Claude Code. The client will show <b>Authentication required</b> and send you to the EVE login.</p>
			<p>MCP endpoint: <code>%s</code></p>
			<p class=dim><a href="%s">%s</a></p>
			<p class=dim>Writes: confirm (mail cap 5/hour)</p>`,
		html.EscapeString(h.OAuth.ResourceURL()), repo, repo)
	page(w, "EVE MCP", body)
}

func (h *API) GetAuthLogin(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, h.Host.URL("oauth", "authorize"), http.StatusFound)
}

func (h *API) GetAuthCallback(w http.ResponseWriter, r *http.Request, _ GetAuthCallbackParams) {
	if errS := r.URL.Query().Get("error"); errS != "" {
		desc := r.URL.Query().Get("error_description")
		h.logPage(pageRefused, errCCPRefused, "error", errS, "error_description", desc)
		writePage(w, pageRefused, "")

		return
	}
	code, state := r.URL.Query().Get("code"), r.URL.Query().Get("state")
	if code == "" || state == "" {
		writePage(w, pageBadCallback, "")

		return
	}
	cb, err := h.OAuth.CompleteCallback(r.Context(), code, state)
	if err != nil {
		h.writeErr(w, err)

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

func (h *API) bindError(w http.ResponseWriter, _ *http.Request, err error) {
	h.logPage(pageGeneric, err)
	http.Error(w, `{"error":"invalid_request"}`, http.StatusBadRequest)
}

func (h *API) writeErr(w http.ResponseWriter, err error) {
	if short, ok := errors.AsType[oauth.ShortGrantError](err); ok {
		h.logPage(pageShortGrant, err, "missing", short.Missing)
		shortGrant(w, short.Missing)

		return
	}
	entry := lookup(err)
	h.logPage(entry, err)
	writePage(w, entry, "")
}

func (h *API) logPage(entry pageErr, err error, extra ...any) {
	args := append([]any{"err", err, "title", entry.title}, extra...)
	if entry.status >= http.StatusInternalServerError {
		h.Logger.Error("http: callback", args...)

		return
	}
	h.Logger.Info("http: callback", args...)
}
