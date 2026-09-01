package eve

import (
	"errors"

	"github.com/truewebber/eve-online-mcp/internal/adapter/esi"
	"github.com/truewebber/eve-online-mcp/internal/adapter/sso"
	"github.com/truewebber/eve-online-mcp/internal/domain/write"
	"github.com/truewebber/eve-online-mcp/internal/usecase/session"
)

const (
	sentenceAuth         = "This connection is not authorized. Re-authenticate the MCP server (Authentication required) and try again."
	sentenceScope        = "This character is missing a required ESI scope. Re-authenticate the MCP server (Authentication required) and approve the full scope set."
	sentenceGrant        = "This connection's EVE grant was revoked or expired. Re-authenticate the MCP server (Authentication required) and try again."
	sentenceNPC          = "This character is in an NPC corporation. ESI corporation hangars, wallets and jobs only exist for player-created corporations. There is nothing for eve_corp_* to read. Call eve_corp_overview."
	sentenceRole         = "This character does not hold the corporation role ESI requires. Location-specific roles do not unlock these endpoints. Call eve_corp_overview."
	sentenceCorp         = "ESI did not return a corporation for this character. Try again shortly."
	sentenceMail         = "Mail budget exhausted: 5 mails in the last hour. Wait until an earlier send drops out of the rolling hour, then try again."
	sentenceConfirm      = "That confirm_token is unknown or has expired. Call the tool again without a token to get a fresh preview."
	sentenceConfirmArgs  = "The arguments changed since the preview was generated, so the token was discarded. Request a new preview and confirm that one."
	sentenceConfirmTool  = "This confirm_token was issued for a different tool. Call the intended tool again without a token to get a fresh preview."
	sentenceCapability   = "Unknown write capability."
	sentenceAllowance    = "This character's ESI request allowance is spent. Wait until retry_at, then call the same tool once. Do not retry in a loop."
	sentenceBudget       = "This character's ESI error budget is spent. Wait until retry_at, then call the same tool once. Do not retry in a loop."
	sentenceESILimit     = "CCP's ESI error limit is shared for this server's public IP. Wait until retry_at, then call the same tool once. Do not retry in a loop."
	sentenceESI          = "ESI rejected this request. Call eve_auth_status if this keeps happening."
	sentenceUnresolved   = "That name could not be resolved. Names must be exact — call eve_universe_search."
	sentenceWrite        = "This write was blocked. Call the same tool again without a confirm_token to get a fresh preview."
	sentenceCSPAUnpriced = "The CSPA charge could not be priced, so nothing was attempted. Call eve_mail_send again shortly."
	sentenceCSPAExceeds  = "The priced CSPA charge exceeds approved_cost. Raise approved_cost or drop recipients."
	sentenceGeneric      = "The request failed. Call eve_auth_status, then retry the same tool."
	noteSectionFailed    = "This section could not be loaded. The rest of the result is still valid."
	kindError            = "Error"
	invariantRequired    = "is required"
)

var ErrCSPAUnpriced = errors.New("eve: cspa unpriced")

type UnresolvedError struct {
	Names []string
}

func (e UnresolvedError) Error() string { return "eve: name unresolved" }

type ValidationError struct {
	Field     string
	Invariant string
}

func (e ValidationError) Error() string { return e.Field + " " + e.Invariant }

type CSPAExceedsError struct {
	Cost     float64
	Approved int
}

func (e CSPAExceedsError) Error() string { return "eve: cspa exceeds approved_cost" }

type PreviewReadError struct {
	Read string
	err  error
}

func (e PreviewReadError) Error() string { return "eve: preview read failed" }

func (e PreviewReadError) Unwrap() error { return e.err }

func toolSentence(err error) string {
	if v, ok := errors.AsType[ValidationError](err); ok {
		return v.Field + " " + v.Invariant
	}
	if s := sentenceFromIs(err); s != "" {
		return s
	}

	return sentenceFromAs(err)
}

func sentenceFromIs(err error) string {
	for _, row := range []struct {
		err error
		s   string
	}{
		{session.ErrNoSession, sentenceAuth},
		{session.ErrDeadSession, sentenceAuth},
		{session.ErrMissingScope, sentenceScope},
		{write.ErrMissingWriteScope, sentenceScope},
		{sso.ErrInvalidGrant, sentenceGrant},
		{session.ErrNPCCorp, sentenceNPC},
		{session.ErrMissingCorpRole, sentenceRole},
		{session.ErrNoCorporation, sentenceCorp},
		{write.ErrMailCap, sentenceMail},
		{write.ErrConfirmUnknown, sentenceConfirm},
		{write.ErrConfirmArgs, sentenceConfirmArgs},
		{write.ErrConfirmTool, sentenceConfirmTool},
		{write.ErrUnknownCapability, sentenceCapability},
		{esi.ErrAllowanceSpent, sentenceAllowance},
		{esi.ErrBudgetSpent, sentenceBudget},
		{ErrCSPAUnpriced, sentenceCSPAUnpriced},
	} {
		if errors.Is(err, row.err) {
			return row.s
		}
	}

	return ""
}

func sentenceFromAs(err error) string {
	switch {
	case errors.As(err, new(esi.RateLimitedError)):
		return sentenceESILimit
	case errors.As(err, new(esi.Error)):
		return sentenceESI
	case errors.As(err, new(PreviewReadError)):
		return sentenceCSPAUnpriced
	case errors.As(err, new(CSPAExceedsError)):
		return sentenceCSPAExceeds
	case errors.As(err, new(UnresolvedError)):
		return sentenceUnresolved
	case errors.As(err, new(sso.Error)):
		return sentenceAuth
	case errors.As(err, new(write.BlockedError)):
		return sentenceWrite
	default:
		return sentenceGeneric
	}
}

func unresolvedNames(err error) []string {
	if u, ok := errors.AsType[UnresolvedError](err); ok {
		return u.Names
	}

	return nil
}

func unresolvedResult(names ...string) map[string]any {
	return map[string]any{
		fError:  sentenceUnresolved,
		fKind:   kindError,
		"names": names,
	}
}

func sectionNote(a *session.Session, section string, err error) string {
	a.Logger.Error("eve: "+section, "err", err, "character", a.CharacterID)

	return noteSectionFailed
}

func ambiguousResult(matches []string) map[string]any {
	return map[string]any{
		fError:    "More than one match; confirm which one is meant with eve_universe_search.",
		fKind:     kindError,
		"matches": matches,
	}
}
