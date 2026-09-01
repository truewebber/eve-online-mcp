package sweep

import "context"

func (r *Runner) expireLoginStates(ctx context.Context) int64 {
	n, err := r.logins.DeleteExpired(ctx)
	if err != nil {
		r.logger.Error("sweep: login_states", "err", err)

		return 0
	}

	return n
}

func (r *Runner) expireAuthCodes(ctx context.Context) int64 {
	swept, err := r.codes.SweepExpired(ctx)
	if err != nil {
		r.logger.Error("sweep: auth_codes", "err", err)

		return 0
	}
	r.revokeAtCCP(ctx, swept.Tokens)

	return swept.Count
}

func (r *Runner) expireConfirmTokens(ctx context.Context) int64 {
	n, err := r.confirms.DeleteExpired(ctx)
	if err != nil {
		r.logger.Error("sweep: confirm_tokens", "err", err)

		return 0
	}

	return n
}

func (r *Runner) expireSessions(ctx context.Context) int64 {
	revoked, err := r.sessions.ExpireValidTil(ctx)
	if err != nil {
		r.logger.Error("sweep: expire sessions", "err", err)

		return 0
	}
	r.revokeAtCCP(ctx, revoked.Tokens)

	return int64(len(revoked.IDs))
}

func (r *Runner) purgeSessions(ctx context.Context) int64 {
	n, err := r.sessions.PurgeRevoked(ctx)
	if err != nil {
		r.logger.Error("sweep: purge sessions", "err", err)

		return 0
	}

	return n
}

func (r *Runner) purgeMutations(ctx context.Context) int64 {
	n, err := r.mutations.DeleteOld(ctx)
	if err != nil {
		r.logger.Error("sweep: mutations", "err", err)

		return 0
	}

	return n
}

type clientPurge struct {
	Soft int64
	Hard int64
}

func (r *Runner) purgeClients(ctx context.Context) clientPurge {
	soft, err := r.clients.SoftDeleteAbandoned(ctx)
	if err != nil {
		r.logger.Error("sweep: oauth_clients soft-delete", "err", err)
		soft = 0
	}
	hard, err := r.clients.DeleteLongSoftDeleted(ctx)
	if err != nil {
		r.logger.Error("sweep: oauth_clients delete", "err", err)
		hard = 0
	}

	return clientPurge{Soft: soft, Hard: hard}
}

func (r *Runner) revokeAtCCP(ctx context.Context, tokens []string) {
	for _, tok := range tokens {
		r.sso.Revoke(ctx, tok)
	}
}
