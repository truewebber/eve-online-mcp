package eve

import (
	"context"
	"errors"

	"github.com/truewebber/eve-online-mcp/internal/adapter/esi"
	"github.com/truewebber/eve-online-mcp/internal/domain/write"
	"github.com/truewebber/eve-online-mcp/internal/usecase/session"
)

func recordWrite(ctx context.Context, a *session.Session, tool, capability string, args map[string]any, err error) {
	rec := write.Record{Tool: tool, Capability: capability, Args: args, Outcome: write.OutcomeOK}
	if err != nil {
		rec.Outcome = write.OutcomeError
		rec.ESIStatus, rec.Error = esiWriteOutcome(err)
	}
	if recErr := a.Guard.Record(ctx, rec); recErr != nil && a.Logger != nil {
		a.Logger.Error("eve: record mutation", "tool", tool, "err", recErr)
	}
}

func esiWriteOutcome(err error) (int, string) {
	if ee, ok := errors.AsType[esi.Error](err); ok {
		return ee.Status, ee.Error()
	}
	if rl, ok := errors.AsType[esi.RateLimitedError](err); ok {
		return rl.Status, rl.Error()
	}

	return 0, err.Error()
}
