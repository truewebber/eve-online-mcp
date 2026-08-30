package session

import (
	"errors"
	"time"

	"github.com/truewebber/eve-online-mcp/internal/adapter/esi"
	"github.com/truewebber/eve-online-mcp/internal/adapter/sso"
	"github.com/truewebber/eve-online-mcp/internal/domain/character"
	"github.com/truewebber/eve-online-mcp/internal/domain/write"
)

// MapError turns adapter/domain errors into the JSON the model already knows.
func MapError(err error) map[string]any {
	var ae sso.Error
	var nf character.NotFound
	var wb write.Blocked
	var ul esi.UserLimited
	var rl esi.RateLimited
	var ee esi.Error
	switch {
	case errors.As(err, &ae):
		return map[string]any{"error": ae.Error(), "kind": "AuthError"}
	case errors.As(err, &nf):
		return map[string]any{"error": nf.Error(), "kind": "CharacterNotFound"}
	case errors.As(err, &wb):
		return map[string]any{"error": wb.Error(), "kind": "WriteBlocked"}
	case errors.As(err, &ul):
		return map[string]any{
			"error":               ul.Error(),
			"kind":                "UserRateLimited",
			"retry_after_seconds": ul.RetrySec,
			"retry_at":            ul.RetryAt.UTC().Format(time.RFC3339),
			"hint":                "Wait until retry_at, then call the same tool once. Do not retry in a loop.",
		}
	case errors.As(err, &rl):
		out := map[string]any{
			"error":               rl.Error(),
			"kind":                "EsiRateLimited",
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
		return map[string]any{"error": ee.Error(), "kind": "EsiError", "status": ee.Status}
	default:
		return map[string]any{"error": err.Error(), "kind": "Error"}
	}
}
