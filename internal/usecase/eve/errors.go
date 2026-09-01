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
	switch {
	case errors.Is(err, session.ErrNoSession), errors.Is(err, session.ErrDeadSession):
		return sentenceAuth
	case errors.Is(err, session.ErrMissingScope), errors.Is(err, write.ErrMissingWriteScope):
		return sentenceScope
	case errors.Is(err, sso.ErrInvalidGrant):
		return sentenceGrant
	case errors.Is(err, session.ErrNPCCorp):
		return sentenceNPC
	case errors.Is(err, session.ErrMissingCorpRole):
		return sentenceRole
	case errors.Is(err, session.ErrNoCorporation):
		return sentenceCorp
	case errors.Is(err, write.ErrMailCap):
		return sentenceMail
	case errors.Is(err, write.ErrConfirmUnknown):
		return sentenceConfirm
	case errors.Is(err, write.ErrConfirmArgs):
		return sentenceConfirmArgs
	case errors.Is(err, write.ErrConfirmTool):
		return sentenceConfirmTool
	case errors.Is(err, write.ErrUnknownCapability):
		return sentenceCapability
	case errors.Is(err, esi.ErrAllowanceSpent):
		return sentenceAllowance
	case errors.Is(err, esi.ErrBudgetSpent):
		return sentenceBudget
	case errors.As(err, new(esi.RateLimitedError)):
		return sentenceESILimit
	case errors.As(err, new(esi.Error)):
		return sentenceESI
	case errors.Is(err, ErrCSPAUnpriced), errors.As(err, new(PreviewReadError)):
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
	if a != nil && a.Logger != nil {
		a.Logger.Error("eve: "+section, "err", err, "character", a.CharacterID)
	}

	return noteSectionFailed
}

func ambiguousResult(matches []string) map[string]any {
	return map[string]any{
		fError:    "More than one match; confirm which one is meant with eve_universe_search.",
		fKind:     kindError,
		"matches": matches,
	}
}
