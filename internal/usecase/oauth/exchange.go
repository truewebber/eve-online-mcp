package oauth

import (
	"errors"
	"net/http"

	"github.com/truewebber/eve-online-mcp/internal/domain/authcode"
	"github.com/truewebber/eve-online-mcp/internal/usecase/session"
)

func (s *Server) exchangeCode(w http.ResponseWriter, r *http.Request) {
	code := r.Form.Get(paramCode)
	verifier := r.Form.Get(paramCodeVerifier)
	redirect := r.Form.Get(paramRedirectURI)
	ac, err := s.codes.Get(r.Context(), code)
	if errors.Is(err, authcode.ErrNotFound) {
		http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)

		return
	}
	if err != nil {
		http.Error(w, `{"error":"server_error"}`, http.StatusInternalServerError)

		return
	}
	if redirect != "" && redirect != ac.RedirectURI {
		http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)

		return
	}
	if !pkceOK(ac.CodeChallenge, verifier) {
		http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)

		return
	}
	exchanged, err := s.runtime.Redeem(r.Context(), session.ParkedGrant{
		Code:         ac.Value,
		CharacterID:  ac.CharacterID,
		RefreshToken: ac.RefreshToken,
		Scopes:       ac.Scopes,
		MCPClientID:  ac.MCPClientID,
		ClientName:   s.clientName(r.Context(), ac.MCPClientID),
		IP:           requestIP(r),
	})
	if errors.Is(err, authcode.ErrNotFound) {
		http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)

		return
	}
	if err != nil {
		http.Error(w, `{"error":"server_error"}`, http.StatusInternalServerError)

		return
	}
	s.writeTokens(w, int(exchanged.Session.CharacterID), exchanged.Session.ID, exchanged.Session.ValidTil)
	s.runtime.RevokeAtCCP(r.Context(), exchanged.Revoked.Tokens)
}
