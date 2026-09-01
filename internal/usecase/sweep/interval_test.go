package sweep

import (
	"context"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/truewebber/eve-online-mcp/internal/domain/authcode"
	"github.com/truewebber/eve-online-mcp/internal/domain/session"
	"github.com/truewebber/eve-online-mcp/internal/mocks"
)

func TestIntervalOneRunPerTick(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctrl := gomock.NewController(t)
		var n atomic.Int64
		opts := idleOpts(ctrl, func() { n.Add(1) })
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		go New(opts).Start(ctx)
		synctest.Wait()
		if got := n.Load(); got != 1 {
			t.Fatalf("after start %d", got)
		}
		time.Sleep(Interval)
		synctest.Wait()
		if got := n.Load(); got != 2 {
			t.Fatalf("after tick %d", got)
		}
		cancel()
		synctest.Wait()
	})
}

func TestIntervalSkipsWhenLockHeld(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctrl := gomock.NewController(t)
		lock := mocks.NewMockSweepLock(ctrl)
		lock.EXPECT().Try(gomock.Any()).Return(false, nil).AnyTimes()
		opts := idleOpts(ctrl, func() { t.Fatal("ran while lock held") })
		opts.Lock = lock
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		go New(opts).Start(ctx)
		synctest.Wait()
		time.Sleep(Interval)
		synctest.Wait()
		cancel()
		synctest.Wait()
	})
}

func idleOpts(ctrl *gomock.Controller, onRun func()) Options {
	lock := mocks.NewMockSweepLock(ctrl)
	lock.EXPECT().Try(gomock.Any()).Return(true, nil).AnyTimes()
	lock.EXPECT().Release(gomock.Any()).Return(nil).AnyTimes()
	logins := mocks.NewMockLoginstateRepository(ctrl)
	logins.EXPECT().DeleteExpired(gomock.Any()).DoAndReturn(func(context.Context) (int64, error) {
		onRun()

		return 0, nil
	}).AnyTimes()
	codes := mocks.NewMockAuthcodeRepository(ctrl)
	codes.EXPECT().SweepExpired(gomock.Any()).Return(authcode.Swept{}, nil).AnyTimes()
	confirms := mocks.NewMockConfirmRepository(ctrl)
	confirms.EXPECT().DeleteExpired(gomock.Any()).Return(int64(0), nil).AnyTimes()
	sessions := mocks.NewMockSessionRepository(ctrl)
	sessions.EXPECT().ExpireValidTil(gomock.Any()).Return(session.Revoked{}, nil).AnyTimes()
	sessions.EXPECT().PurgeRevoked(gomock.Any()).Return(int64(0), nil).AnyTimes()
	mutations := mocks.NewMockMutationRepository(ctrl)
	mutations.EXPECT().DeleteOld(gomock.Any()).Return(int64(0), nil).AnyTimes()
	clients := mocks.NewMockOauthclientRepository(ctrl)
	clients.EXPECT().SoftDeleteAbandoned(gomock.Any()).Return(int64(0), nil).AnyTimes()
	clients.EXPECT().DeleteLongSoftDeleted(gomock.Any()).Return(int64(0), nil).AnyTimes()

	return Options{
		Lock:      lock,
		Logins:    logins,
		Codes:     codes,
		Confirms:  confirms,
		Sessions:  sessions,
		Mutations: mutations,
		Clients:   clients,
		SSO:       mocks.NewMockSSOClient(ctrl),
		Logger:    mocks.QuietLogger(ctrl),
	}
}
