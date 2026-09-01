package eve

import (
	"context"
	"errors"

	"github.com/truewebber/eve-online-mcp/internal/adapter/esi"
	"github.com/truewebber/eve-online-mcp/internal/domain/write"
	"github.com/truewebber/eve-online-mcp/internal/usecase/session"
)

type writeLog struct {
	tool, capability string
	args             map[string]any
	err              error
}

func recordWrite(ctx context.Context, a *session.Session, rec writeLog) {
	out := write.Record{Tool: rec.tool, Capability: rec.capability, Args: rec.args, Outcome: write.OutcomeOK}
	if rec.err != nil {
		out.Outcome = write.OutcomeError
		fail := esiWriteOutcome(rec.err)
		out.ESIStatus, out.Error = fail.status, fail.msg
	}
	if recErr := a.Guard.Record(ctx, out); recErr != nil && a.Logger != nil {
		a.Logger.Error("eve: record mutation", "tool", rec.tool, "err", recErr)
	}
}

type writeOutcome struct {
	status int
	msg    string
}

func esiWriteOutcome(err error) writeOutcome {
	if ee, ok := errors.AsType[esi.Error](err); ok {
		return writeOutcome{status: ee.Status, msg: ee.Error()}
	}
	if rl, ok := errors.AsType[esi.RateLimitedError](err); ok {
		return writeOutcome{status: rl.Status, msg: rl.Error()}
	}

	return writeOutcome{msg: err.Error()}
}
