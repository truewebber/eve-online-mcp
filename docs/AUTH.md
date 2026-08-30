# eve-mcp — Authorization Map

**This document is normative.** Every channel a credential may travel
and every place it may rest is listed here; the implementation must not
move a secret any other way. Companion to [SPEC.md](SPEC.md) §3 and
[DB.md](DB.md).

Two OAuth layers, deliberately separate:

1. **MCP OAuth** — we are the authorization server; Cursor / Claude are
   dynamically registered clients. Produces our JWTs (`sub` = character
   id, `sid` = session id).
2. **EVE SSO** — CCP is the authorization server; this process is the
   one registered application (`CLIENT_ID`). Produces the EVE grant
   (refresh + access tokens) that lives on the session row.

The player's EVE password is typed at CCP only — no party below ever
sees it.

## Flow map

```yaml
actors:
  browser:      "player's browser (also carries redirects between parties)"
  mcp_client:   "Cursor / Claude app (OAuth client; holds its PKCE verifier + MCP tokens)"
  eve_mcp:      "our server, any replica count, public listener (LISTEN / PUBLIC_URL, TLS via CF tunnel)"
  postgres:     "durable store (DATABASE_URL, cluster-internal)"
  eve_sso:      "login.eveonline.com (CCP)"
  esi:          "esi.evetech.net (CCP)"

secrets_inventory:
  CLIENT_SECRET:        {holder: eve_mcp env,        transit: "eve_mcp -> eve_sso only (TLS)",            ttl: static,   leak: "attacker still needs a login flow; rotate at CCP"}
  HMAC_KEY:             {holder: "eve_mcp env (k8s Secret)", transit: never,                              ttl: static,   leak: "forge any MCP JWT -> rotate secret + restart = global re-auth"}
  login_states.state:   {holder: postgres,           transit: "browser URL (to CCP and back)",            ttl: 15m,      leak: "useless alone: consumed once, binds no tokens"}
  pkce_verifier_ours:   {holder: postgres,           transit: never,                                      ttl: 15m,      leak: "needs matching EVE code too"}
  eve_auth_code:        {holder: nobody,             transit: "eve_sso -> browser URL -> eve_mcp",        ttl: "5m, single use", leak: "unusable without pkce_verifier_ours (in PG)"}
  eve_refresh_token:    {holder: "postgres (auth_codes -> sessions)", transit: "eve_sso -> eve_mcp (TLS)", ttl: "until revoked / session end", leak: "CROWN JEWELS: full account read. Never sent to client or browser"}
  eve_access_token:     {holder: "eve_mcp process memory", transit: "eve_mcp -> esi header (TLS)",        ttl: 20m,      leak: "20 min of access; not persisted anywhere"}
  client_pkce_verifier: {holder: mcp_client,         transit: "mcp_client -> eve_mcp at token exchange",  ttl: minutes,  leak: "useless without our auth code"}
  our_auth_code:        {holder: postgres,           transit: "eve_mcp -> browser URL -> client callback",ttl: "2m, single use", leak: "unusable without client_pkce_verifier (in the app)"}
  mcp_access_jwt:       {holder: mcp_client,         transit: "Authorization header on /mcp (TLS)",       ttl: 1h,       leak: "one character, until sid check fails"}
  mcp_refresh_jwt:      {holder: mcp_client,         transit: "POST /oauth/token body (TLS)",             ttl: "session valid_til (30d)", leak: "same scope; dies with session revoke"}
  confirm_token:        {holder: "postgres + chat transcript", transit: "tool result / tool args",        ttl: "300s, single use", leak: "harmless without a live bearer of the SAME session"}

flows:

  discovery_and_registration:            # once per client install
    - {from: mcp_client, to: eve_mcp,  via: "GET /mcp",                    carries: [nothing],                       result: "401 + WWW-Authenticate (PRM URL)"}
    - {from: mcp_client, to: eve_mcp,  via: "GET /.well-known/*",          carries: [nothing],                       result: "AS metadata (endpoints)"}
    - {from: mcp_client, to: eve_mcp,  via: "POST /oauth/register",        carries: [client_name, redirect_uris],   result: "client_id; uris filtered by allowlist -> postgres.oauth_clients"}

  sign_in:                                # per connection; any pod serves any step
    - step: authorize
      from: browser
      to: eve_mcp
      via: "GET /oauth/authorize?client_id&redirect_uri&state&code_challenge"
      carries: [client_pkce_challenge]    # verifier stays inside the app
      server_does: "validate client+redirect vs allowlist; write postgres.login_states
                    (our state + our pkce_verifier + frozen client params)"
      result: "302 -> eve_sso (nothing secret kept in process memory)"
    - step: eve_login
      from: browser
      to: eve_sso
      via: "GET /v2/oauth/authorize?client_id=CLIENT_ID&state&code_challenge(ours)"
      carries: [login_states.state]
      note: "player types EVE credentials AT CCP ONLY — we never see them"
    - step: callback
      from: browser
      to: eve_mcp
      via: "GET /auth/callback?code=EVE_CODE&state"
      carries: [eve_auth_code, login_states.state]
      server_does: "consume login_states; POST eve_sso /v2/oauth/token
                    (EVE_CODE + our pkce_verifier [+ CLIENT_SECRET]) -> eve tokens;
                    upsert postgres.characters; park grant in postgres.auth_codes; mint OUR_CODE"
      result: "302 -> client redirect_uri?code=OUR_CODE&state=mcp_state"
      invariant: "EVE tokens NEVER enter this redirect — only our one-time code"
    - step: token_exchange
      from: mcp_client
      to: eve_mcp
      via: "POST /oauth/token (code=OUR_CODE, code_verifier)"
      carries: [our_auth_code, client_pkce_verifier]
      server_does: "verify PKCE + redirect_uri; ONE TRANSACTION:
                    delete code -> revoke EVERY session of this character with revoked_at IS NULL
                    -> insert sessions row (grant moves in) -> commit -> issue JWTs (sub=character, sid)"
      after_commit: "POST eve_sso /v2/oauth/revoke for the predecessor's refresh token,
                     best effort, failure logged only — never inside the transaction"
      result: "access + refresh JWT -> client storage"

  tool_call:
    - {from: mcp_client, to: eve_mcp, via: "POST /mcp (Authorization: Bearer access_jwt)",
       server_does: "verify signature + sid live (valid_til, revoked_at) + session.scopes still sufficient
                     -> per-character runtime",
       then: "eve_mcp -> esi with eve_access_token (refreshed from session.refresh_token under FOR UPDATE)"}

  token_refresh:
    - {from: mcp_client, to: eve_mcp, via: "POST /oauth/token (grant_type=refresh_token)",
       carries: [mcp_refresh_jwt], check: "sid still live, else invalid_grant -> re-login"}

  re_authentication:                      # the only way back in (SPEC §3.5)
    - trigger: "eve_sso returns invalid_grant on refresh (player revoked us at CCP, or CCP dropped the stream)"
    - trigger: "owner_hash at login differs -> character was sold"
    - trigger: "session.scopes no longer cover the build's required set"
    - server_does: "revoke the session; next /mcp call answers 401 + WWW-Authenticate"
    - result: "client restarts the OAuth flow -> sign_in above. No tool can mint a login URL."

  mutation_confirm:
    - {from: mcp_client, to: eve_mcp, via: "tools/call without token",
       result: "preview + confirm_token (bound to sid) -> chat"}
    - {from: mcp_client, to: eve_mcp, via: "tools/call with token + same args",
       check: "same tool + same args_digest + same sid, single use -> execute -> append postgres.mutations"}

  logout:
    - {from: mcp_client, to: eve_mcp, via: "eve_auth_logout",
       server_does: "revoke session; soft-delete character; after commit revoke session.refresh_token at CCP"}
```

## Leak audit — what cannot escape, by construction

1. **The EVE refresh token never leaves the pair "our server ↔ CCP".**
   It appears in no browser URL, is never issued to an MCP client, and
   rests only in Postgres (`auth_codes` briefly, then `sessions`). Its
   only transit is TLS calls to `login.eveonline.com`.
2. **The EVE password is invisible even to us** — typed on CCP's page.
3. **Browser URLs carry only single-use material**: `state` and the two
   authorization codes. Each is useless alone — the EVE code needs our
   PKCE verifier (in Postgres), our code needs the client's verifier
   (inside the app). Browser history and relay logs (cursor.com /
   claude.ai callbacks) yield nothing redeemable.
4. **The MCP client holds only our JWTs.** A compromised player machine
   exposes one character for at most the session lifetime (30 d), and
   dies with a session revoke.
5. **Postgres is where account access rests** (refresh tokens); the JWT
   signing key lives separately, in the `HMAC_KEY` env secret. A
   database backup alone still exposes the EVE grants — DB.md's rule
   stands: treat backups like the refresh tokens themselves — but it
   does not let an attacker mint valid MCP bearers.
6. **The audit log is not a second mailbox.** `mutations` stores a
   digest and a one-line summary, never message bodies (DB.md).

## Standing requirements

These are not implementation details; breaking one breaks the audit
above.

1. `PUBLIC_URL` is HTTPS (authorization codes travel in browser URLs).
   Validated at boot (SPEC §2), not left to the operator's memory.
2. The redirect allowlist stays exact. It is what stops our code from
   being sent to an attacker's callback. `EXTRA_REDIRECT_URIS` widens it
   only with absolute, wildcard-free URIs, and only the host can set it
   — it is env, never anything a client can influence.
3. The public listener is reachable only through the tunnel. Until that
   is true, `CF-Connecting-IP` is attacker-controlled and the socket
   address is used for `sessions.ip` instead (SPEC §10).
4. Anonymous OAuth routes are rate-limited per IP and their rows are
   swept (SPEC §5.5, DB.md) — registration is unauthenticated by
   protocol design, so the only defence is that it cannot accumulate.
5. Revoking at CCP never happens inside a database transaction, and
   never before the replacement session is committed.

Known deliberate exposure: `confirm_token` enters the chat transcript
(it is part of the model's context). Accepted: it is single-use,
expires in 300 s, and is worthless without a live bearer of the same
session.
