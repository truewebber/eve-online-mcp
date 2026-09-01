package eve

import (
	"context"
	"testing"
	"time"

	nhttp "net/http"

	"go.uber.org/mock/gomock"

	"github.com/truewebber/eve-online-mcp/internal/adapter/esi"
	esihttp "github.com/truewebber/eve-online-mcp/internal/adapter/esi/http"
	"github.com/truewebber/eve-online-mcp/internal/adapter/esi/http/esitest"
	"github.com/truewebber/eve-online-mcp/internal/adapter/sso"
	"github.com/truewebber/eve-online-mcp/internal/domain/character"
	dbsession "github.com/truewebber/eve-online-mcp/internal/domain/session"
	"github.com/truewebber/eve-online-mcp/internal/domain/write"
	"github.com/truewebber/eve-online-mcp/internal/mocks"
	"github.com/truewebber/eve-online-mcp/internal/usecase/session"
)

func fixtureSession(t *testing.T) *session.Session {
	t.Helper()
	tr, err := esitest.Load()
	if err != nil {
		t.Fatal(err)
	}
	client := esihttp.New(esi.Options{
		BaseURL:    esi.DefaultBaseURL,
		CompatDate: esitest.CompatDate,
		UserAgent:  "eve-mcp-test",
	}, &nhttp.Client{Transport: &esitest.Fallback{Inner: tr}}, mocks.QuietLogger(gomock.NewController(t)))

	return toolSession(t, client, true)
}

func toolSession(t *testing.T, esiClient esi.Client, refresh bool) *session.Session {
	t.Helper()
	ctrl := gomock.NewController(t)
	logger := mocks.QuietLogger(ctrl)
	chars := mocks.NewMockCharacterRepository(ctrl)
	sess := mocks.NewMockSessionRepository(ctrl)
	muts := mocks.NewMockMutationRepository(ctrl)
	confs := mocks.NewMockConfirmRepository(ctrl)
	ssoC := mocks.NewMockSSOClient(ctrl)
	id := esitest.FixtureCharacterID
	sid := int64(1)
	scopes := write.RequestedScopes()
	sess.EXPECT().LiveByID(gomock.Any(), sid).Return(&dbsession.Session{
		ID: sid, CharacterID: int64(id), Scopes: scopes, RefreshToken: "refresh",
	}, nil).AnyTimes()
	chars.EXPECT().Get(gomock.Any(), int64(id)).Return(&character.Character{
		ID: int64(id), Name: "Fixture Pilot",
	}, nil).AnyTimes()
	muts.EXPECT().CountMailCap(gomock.Any(), gomock.Any()).Return(0, nil).AnyTimes()
	if refresh {
		sess.EXPECT().LockForRefresh(gomock.Any(), sid, gomock.Any()).DoAndReturn(
			func(_ context.Context, _ int64, fn func(string) (string, error)) error {
				_, err := fn("refresh")

				return err
			},
		).AnyTimes()
		ssoC.EXPECT().AccessToken(gomock.Any(), "refresh").Return(&sso.CharacterToken{
			AccessToken: "at", RefreshToken: "refresh",
			AccessExpiresAt: time.Now().Add(time.Hour),
			CharacterID:     id, CharacterName: "Fixture Pilot", Scopes: scopes,
		}, nil).AnyTimes()
	}
	if mock, ok := esiClient.(*mocks.MockESIClient); ok {
		mock.EXPECT().ForUser(gomock.Any()).Return(mock).AnyTimes()
	}
	runtime, err := session.Open(session.Options{
		ESI: esiClient, SSO: ssoC, Characters: chars, Sessions: sess,
		Mutations: muts, Confirms: confs, Logger: logger,
	})
	if err != nil {
		t.Fatal(err)
	}

	return runtime.ForCharacter(id, sid)
}
