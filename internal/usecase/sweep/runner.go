package sweep

import (
	"context"
	"errors"
	"time"

	"github.com/truewebber/gopkg/log"

	"github.com/truewebber/eve-online-mcp/internal/adapter/sso"
	"github.com/truewebber/eve-online-mcp/internal/domain/authcode"
	"github.com/truewebber/eve-online-mcp/internal/domain/confirm"
	"github.com/truewebber/eve-online-mcp/internal/domain/loginstate"
	"github.com/truewebber/eve-online-mcp/internal/domain/mutation"
	"github.com/truewebber/eve-online-mcp/internal/domain/oauthclient"
	"github.com/truewebber/eve-online-mcp/internal/domain/session"
)

const Interval = 5 * time.Minute

var (
	errLockRequired      = errors.New("sweep: lock is required")
	errLoginsRequired    = errors.New("sweep: login-state repository is required")
	errCodesRequired     = errors.New("sweep: auth-code repository is required")
	errConfirmsRequired  = errors.New("sweep: confirm repository is required")
	errSessionsRequired  = errors.New("sweep: session repository is required")
	errMutationsRequired = errors.New("sweep: mutation repository is required")
	errClientsRequired   = errors.New("sweep: oauth client repository is required")
	errSSORequired       = errors.New("sweep: SSO client is required")
	errLoggerRequired    = errors.New("sweep: logger is required")
	errPoolRequired      = errors.New("sweep: pool is required")
)

type Options struct {
	Lock      Lock
	Logins    loginstate.Repository
	Codes     authcode.Repository
	Confirms  confirm.Repository
	Sessions  session.Repository
	Mutations mutation.Repository
	Clients   oauthclient.Repository
	SSO       sso.Client
	Logger    log.Logger
}

type Runner struct {
	lock      Lock
	logins    loginstate.Repository
	codes     authcode.Repository
	confirms  confirm.Repository
	sessions  session.Repository
	mutations mutation.Repository
	clients   oauthclient.Repository
	sso       sso.Client
	logger    log.Logger
}

type counts struct {
	LoginStates     int64
	AuthCodes       int64
	ConfirmTokens   int64
	SessionsExpired int64
	SessionsPurged  int64
	Mutations       int64
	ClientsSoft     int64
	ClientsHard     int64
}

func New(opts Options) (*Runner, error) {
	switch {
	case opts.Lock == nil:
		return nil, errLockRequired
	case opts.Logins == nil:
		return nil, errLoginsRequired
	case opts.Codes == nil:
		return nil, errCodesRequired
	case opts.Confirms == nil:
		return nil, errConfirmsRequired
	case opts.Sessions == nil:
		return nil, errSessionsRequired
	case opts.Mutations == nil:
		return nil, errMutationsRequired
	case opts.Clients == nil:
		return nil, errClientsRequired
	case opts.SSO == nil:
		return nil, errSSORequired
	case opts.Logger == nil:
		return nil, errLoggerRequired
	}

	return &Runner{
		lock:      opts.Lock,
		logins:    opts.Logins,
		codes:     opts.Codes,
		confirms:  opts.Confirms,
		sessions:  opts.Sessions,
		mutations: opts.Mutations,
		clients:   opts.Clients,
		sso:       opts.SSO,
		logger:    opts.Logger,
	}, nil
}

func (r *Runner) Start(ctx context.Context) {
	ticker := time.NewTicker(Interval)
	defer ticker.Stop()
	for {
		r.Once(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (r *Runner) Once(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	held, err := r.lock.Try(ctx)
	if err != nil {
		r.logger.Error("sweep: lock", "err", err)

		return
	}
	if !held {
		return
	}
	defer func() {
		if err := r.lock.Release(ctx); err != nil {
			r.logger.Error("sweep: unlock", "err", err)
		}
	}()
	c := counts{
		LoginStates:     r.expireLoginStates(ctx),
		AuthCodes:       r.expireAuthCodes(ctx),
		ConfirmTokens:   r.expireConfirmTokens(ctx),
		SessionsExpired: r.expireSessions(ctx),
		SessionsPurged:  r.purgeSessions(ctx),
		Mutations:       r.purgeMutations(ctx),
	}
	purged := r.purgeClients(ctx)
	c.ClientsSoft = purged.Soft
	c.ClientsHard = purged.Hard
	r.logger.Info("sweep", "counts", c)
}
