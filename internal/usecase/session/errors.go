package session

import (
	"errors"
	"time"

	"github.com/truewebber/eve-online-mcp/internal/adapter/esi"
	"github.com/truewebber/eve-online-mcp/internal/adapter/sso"
	"github.com/truewebber/eve-online-mcp/internal/domain/write"
)

const (
	fieldKind  = "kind"
	kindAuth   = "AuthError"
	kindWrite  = "WriteBlocked"
	kindUser   = "UserRateLimited"
	kindESILim = "EsiRateLimited"
	kindESI    = "EsiError"
	kindError  = "Error"
)

func MapError(err error) map[string]any {
	var ul esi.UserLimitedError
	var rl esi.RateLimitedError
	var ee esi.Error
	var ae sso.Error
	var wb write.BlockedError
	switch {
	case errors.Is(err, ErrNoSession), errors.Is(err, ErrDeadSession),
		errors.Is(err, ErrMissingScope), errors.Is(err, sso.ErrInvalidGrant),
		errors.Is(err, write.ErrMissingWriteScope):
		return map[string]any{fieldKind: kindAuth}
	case errors.Is(err, ErrNPCCorp), errors.Is(err, ErrMissingCorpRole),
		errors.Is(err, ErrNoCorporation):
		return map[string]any{fieldKind: kindError}
	case errors.As(err, &ae):
		return map[string]any{fieldKind: kindAuth}
	case errors.As(err, &wb) || writeBlocked(err):
		return map[string]any{fieldKind: kindWrite}
	case errors.As(err, &ul):
		return map[string]any{
			fieldKind:             kindUser,
			"retry_after_seconds": ul.RetrySec,
			"retry_at":            ul.RetryAt.UTC().Format(time.RFC3339),
			"hint":                "Wait until retry_at, then call the same tool once. Do not retry in a loop.",
		}
	case errors.As(err, &rl):
		out := map[string]any{
			fieldKind:             kindESILim,
			"status":              rl.Status,
			"retry_after_seconds": rl.RetrySec,
			"retry_at":            rl.RetryAt.UTC().Format(time.RFC3339),
			"hint":                "CCP's ESI error limit is shared for this server's public IP. Wait until retry_at, then call the same tool once. Do not retry in a loop.",
		}
		if rl.Remain != nil {
			out["error_limit_remain"] = *rl.Remain
		}
		if rl.ResetSec != nil {
			out["error_limit_reset_seconds"] = *rl.ResetSec
		}

		return out
	case errors.As(err, &ee):
		return map[string]any{fieldKind: kindESI, "status": ee.Status}
	default:
		return map[string]any{fieldKind: kindError}
	}
}

func writeBlocked(err error) bool {
	return errors.Is(err, write.ErrMailCap) ||
		errors.Is(err, write.ErrConfirmUnknown) ||
		errors.Is(err, write.ErrConfirmArgs) ||
		errors.Is(err, write.ErrConfirmTool) ||
		errors.Is(err, write.ErrUnknownCapability)
}
