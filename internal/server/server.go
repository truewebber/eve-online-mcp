package server

import (
	"fmt"
	"html"
	"log"
	"net/http"
	"strings"

	"eve-mcp/internal/app"
	"eve-mcp/internal/config"
	"eve-mcp/internal/mcpoauth"
	"eve-mcp/internal/tenant"
	"eve-mcp/internal/tools"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func ListenAndServe(settings *config.Settings, a *app.App) error {
	tenants, err := tenant.Open(settings.DataDir)
	if err != nil {
		return err
	}
	oauth, err := mcpoauth.Open(settings, a, tenants)
	if err != nil {
		return err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", health(settings))
	mux.HandleFunc("/", index(settings, oauth))
	mux.HandleFunc("/auth/login", login(settings))
	mux.HandleFunc("/auth/callback", callback(a, oauth))
	mux.HandleFunc("/setup", setup(settings))
	oauth.Mount(mux)

	instr := tools.Instructions
	if settings.CorpScopes {
		instr += tools.CorpInstructions
	}
	mcpServer := mcp.NewServer(&mcp.Implementation{
		Name: "eve-online", Title: "EVE Online", Version: config.Version,
	}, &mcp.ServerOptions{Instructions: instr})
	tools.Register(mcpServer, a)
	stream := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return mcpServer
	}, &mcp.StreamableHTTPOptions{
		Stateless:                  true,
		DisableLocalhostProtection: true,
	})
	protected := oauth.ProtectMCP(stream)
	mux.Handle(settings.MCPPath, protected)
	if !strings.HasSuffix(settings.MCPPath, "/") {
		mux.Handle(settings.MCPPath+"/", protected)
	}

	base := settings.BaseURL()
	log.Printf("write mode: %s", settings.WriteMode)
	log.Printf("MCP endpoint: %s%s (OAuth — clients show Authentication required)", base, settings.MCPPath)
	log.Printf("EVE callback must be exactly: %s", settings.CallbackURL)

	return http.ListenAndServe(settings.Listen, mux)
}

func health(settings *config.Settings) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"ok","write_mode":%q,"mcp_endpoint":%q}`, settings.WriteMode, settings.MCPPath)
	}
}

func index(settings *config.Settings, oauth *mcpoauth.Server) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		body := fmt.Sprintf(`
			<h1>EVE MCP</h1>
			<p>Add this URL in Cursor or Claude Code. The client will show <b>Authentication required</b> — that opens a page where you paste your own EVE application Client ID and log in.</p>
			<p>MCP endpoint: <code>%s</code></p>
			<p class=dim>EVE callback must be exactly <code>%s</code>.</p>
			<p class=dim>Writes: <code>%s</code> · config: <code>%s</code></p>`,
			html.EscapeString(oauth.ResourceURL()),
			html.EscapeString(settings.CallbackURL),
			html.EscapeString(settings.WriteMode),
			html.EscapeString(settings.ConfigPath))
		page(w, "EVE MCP", body)
	}
}

func login(settings *config.Settings) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, settings.BaseURL()+"/oauth/authorize", http.StatusFound)
	}
}

func callback(host *app.App, oauth *mcpoauth.Server) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if host == nil {
			pageStatus(w, 503, "Not configured", `<p class=warn>Server is still in setup.</p>`)
			return
		}
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
		sso := host.SSO
		if p, ok := oauth.PeekPending(state); ok {
			if ta, err := oauth.AppByID(p.TenantID); err == nil {
				sso = ta.SSO
			}
		}
		token, err := sso.CompleteLogin(code, state)
		if err != nil {
			pageStatus(w, 400, "Login failed", `<p class=warn>`+html.EscapeString(err.Error())+`</p>`)
			return
		}
		if loc, err := oauth.FinishEVE(state, token.CharacterName); err == nil && loc != "" {
			http.Redirect(w, r, loc, http.StatusFound)
			return
		}
		page(w, "EVE MCP", fmt.Sprintf(`
			<h1>%s is authorized</h1>
			<p class=dim>%d scopes · character #%d</p>
			<p>You can close this tab. Cursor and Claude Code can now read this character.</p>
			<p><a class=btn href="/">Back to status</a></p>`,
			html.EscapeString(token.CharacterName), len(token.Scopes), token.CharacterID))
	}
}

func setup(settings *config.Settings) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			if err := r.ParseForm(); err != nil {
				pageStatus(w, 400, "Bad form", `<p class=warn>`+html.EscapeString(err.Error())+`</p>`)
				return
			}
			settings.ClientID = strings.TrimSpace(r.FormValue("client_id"))
			settings.ClientSecret = strings.TrimSpace(r.FormValue("client_secret"))
			contact := strings.TrimSpace(r.FormValue("contact"))
			if contact != "" {
				settings.UserAgent = "eve-mcp/" + config.Version + " " + contact
			}
			if v := r.FormValue("callback_url"); strings.TrimSpace(v) != "" {
				settings.CallbackURL = strings.TrimSpace(v)
			}
			if err := config.SaveSettings(settings); err != nil {
				pageStatus(w, 500, "Could not save", `<p class=warn>`+html.EscapeString(err.Error())+`</p>`)
				return
			}
			page(w, "EVE MCP", `
				<h1>Saved</h1>
				<p>Restart <code>eve-mcp</code>, then open <a href="/auth/login">Authorize a character</a>.</p>
				<p class=dim>The process does not reload config in place — stop it and start it again.</p>`)
			return
		}
		page(w, "EVE MCP setup", fmt.Sprintf(`
			<h1>First-run setup</h1>
			<p>Register an application at
			<a href="https://developers.eveonline.com/applications">developers.eveonline.com</a>
			with callback <code>%s</code>. PKCE is enough — Client Secret is optional.</p>
			<form method=post>
			  <label>Client ID <input name=client_id required value="%s"></label>
			  <label>Contact email <input name=contact type=email value="%s" placeholder="you@example.com"></label>
			  <label>Client Secret <input name=client_secret value="%s" placeholder="only if confidential"></label>
			  <label>Callback URL <input name=callback_url value="%s"></label>
			  <button type=submit>Save and continue</button>
			</form>
			<p class=dim>Written to <code>%s</code>. Clients never see this.</p>`,
			html.EscapeString(settings.CallbackURL),
			html.EscapeString(settings.ClientID),
			html.EscapeString(extractContact(settings.UserAgent)),
			html.EscapeString(settings.ClientSecret),
			html.EscapeString(settings.CallbackURL),
			html.EscapeString(settings.ConfigPath)))
	}
}

func extractContact(ua string) string {
	parts := strings.Fields(ua)
	if len(parts) >= 2 && strings.Contains(parts[len(parts)-1], "@") {
		return parts[len(parts)-1]
	}
	return ""
}

func page(w http.ResponseWriter, title, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, pageTmpl, html.EscapeString(title), body)
}

func pageStatus(w http.ResponseWriter, status int, title, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	fmt.Fprintf(w, pageTmpl, html.EscapeString(title), body)
}

const pageTmpl = `<!doctype html><meta charset=utf-8><title>%s</title>
<style>
  :root { color-scheme: light dark; }
  body { font: 15px/1.6 system-ui, sans-serif; max-width: 44rem; margin: 4rem auto; padding: 0 1.5rem; }
  h1 { font-size: 1.5rem; margin-bottom: .25rem; }
  h2 { font-size: 1rem; text-transform: uppercase; letter-spacing: .06em; opacity: .6; margin-top: 2rem; }
  ul { padding-left: 1.1rem; }
  code { background: rgba(127,127,127,.18); padding: .1em .35em; border-radius: 3px; }
  .dim { opacity: .65; }
  .warn { color: #c0392b; }
  .btn, button { display: inline-block; padding: .55rem 1rem; border-radius: 6px;
          background: #2b6cb0; color: #fff; text-decoration: none; border: 0; font: inherit; cursor: pointer; }
  label { display: block; margin: .8rem 0; }
  input { display: block; width: 100%%; margin-top: .25rem; padding: .45rem .55rem; font: inherit; box-sizing: border-box; }
</style>
%s
`
