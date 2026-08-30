package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"
	"time"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL is unset; run `make postgres` then `make test-store`")
	}
	ctx := context.Background()
	s, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	release, err := s.HoldTestLock(ctx)
	if err != nil {
		s.Close()
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	t.Cleanup(release)
	if err := s.ResetTables(ctx); err != nil {
		t.Fatal(err)
	}

	return s
}

func TestCreateUserAndGet(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	u, err := s.CreateUser(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if u.ID == "" {
		t.Fatalf("user %+v", u)
	}
	got, err := s.GetUser(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != u.ID {
		t.Fatalf("got %s", got.ID)
	}
	ok, err := s.UserExists(ctx, u.ID)
	if err != nil || !ok {
		t.Fatalf("exists %v %v", ok, err)
	}
	if _, err := s.GetUser(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestCharacterOwnership(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	a, err := s.CreateUser(ctx)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateUser(ctx)
	if err != nil {
		t.Fatal(err)
	}
	row := CharacterRow{
		CharacterID:  2112625428,
		Name:         "Jane Doe",
		OwnerHash:    "hash",
		RefreshToken: "rt-1",
		Scopes:       []string{"esi-wallet.read_character_wallet.v1"},
	}
	if err := s.UpsertCharacter(ctx, a.ID, row); err != nil {
		t.Fatal(err)
	}
	owner, ok, err := s.OwnerOf(ctx, row.CharacterID)
	if err != nil || !ok || owner != a.ID {
		t.Fatalf("owner %s ok %v err %v", owner, ok, err)
	}
	row.RefreshToken = "rt-2"
	if err := s.UpsertCharacter(ctx, a.ID, row); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetCharacter(ctx, row.CharacterID)
	if err != nil {
		t.Fatal(err)
	}
	if got.RefreshToken != "rt-2" || got.UserID != a.ID {
		t.Fatalf("got %+v", got)
	}
	if err := s.UpsertCharacter(ctx, b.ID, row); !errors.Is(err, ErrOwned) {
		t.Fatalf("want ErrOwned, got %v", err)
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO characters (character_id, user_id, name, refresh_token)
		VALUES ($1, $2, 'x', 'y')`, row.CharacterID, b.ID)
	if err == nil {
		t.Fatal("duplicate character_id must fail at the database")
	}
	list, err := s.ListCharacters(ctx, a.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("list %v %v", list, err)
	}
}

func TestWithCharacterForUpdateSerializes(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	u, err := s.CreateUser(ctx)
	if err != nil {
		t.Fatal(err)
	}
	id := int64(99)
	if err := s.UpsertCharacter(ctx, u.ID, CharacterRow{
		CharacterID: id, Name: "Lock", RefreshToken: "old",
	}); err != nil {
		t.Fatal(err)
	}

	started := make(chan struct{})
	release := make(chan struct{})
	var mu sync.Mutex
	var order []string
	errc := make(chan error, 2)

	go func() {
		errc <- s.WithCharacterForUpdate(ctx, id, func(tok string) (string, error) {
			mu.Lock()
			order = append(order, "a-start")
			mu.Unlock()
			close(started)
			<-release
			mu.Lock()
			order = append(order, "a-end")
			mu.Unlock()

			return "from-a", nil
		})
	}()
	<-started
	doneB := make(chan struct{})
	go func() {
		errc <- s.WithCharacterForUpdate(ctx, id, func(tok string) (string, error) {
			mu.Lock()
			order = append(order, "b:"+tok)
			mu.Unlock()

			return "from-b", nil
		})
		close(doneB)
	}()
	time.Sleep(80 * time.Millisecond)
	mu.Lock()
	for _, step := range order {
		if len(step) > 0 && step[0] == 'b' {
			mu.Unlock()
			t.Fatalf("b ran before a released: %v", order)
		}
	}
	snapshot := append([]string(nil), order...)
	mu.Unlock()
	if len(snapshot) != 1 || snapshot[0] != "a-start" {
		t.Fatalf("during lock: %v", snapshot)
	}
	close(release)
	select {
	case <-doneB:
	case <-time.After(5 * time.Second):
		t.Fatal("b blocked forever")
	}
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	got, err := s.GetCharacter(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.RefreshToken != "from-b" {
		t.Fatalf("token %s", got.RefreshToken)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(order) != 3 || order[0] != "a-start" || order[1] != "a-end" || order[2] != "b:from-a" {
		t.Fatalf("order %v", order)
	}
}

func TestAuthCodeOneTime(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	u, err := s.CreateUser(ctx)
	if err != nil {
		t.Fatal(err)
	}
	code := AuthCode{
		Code: "abc", UserID: u.ID, MCPClientID: "c",
		RedirectURI: "http://localhost/cb", CodeChallenge: "ch",
		ExpiresAt: time.Now().Add(2 * time.Minute),
	}
	if err := s.PutAuthCode(ctx, code); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.TakeAuthCode(ctx, "abc")
	if err != nil || !ok || got.UserID != u.ID {
		t.Fatalf("take %v ok %v err %v", got, ok, err)
	}
	_, ok, err = s.TakeAuthCode(ctx, "abc")
	if err != nil || ok {
		t.Fatalf("second take ok=%v err=%v", ok, err)
	}
}

func TestConfirmTTL(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	tok := ConfirmToken{Token: "fresh", UserID: "u", Tool: "eve_mail_send", ArgsDigest: "deadbeef"}
	if err := s.PutConfirmToken(ctx, tok); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.TakeConfirmToken(ctx, "fresh")
	if err != nil || !ok || got.Tool != tok.Tool {
		t.Fatalf("fresh %v ok %v err %v", got, ok, err)
	}
	if err := s.PutConfirmToken(ctx, ConfirmToken{Token: "peek", UserID: "u", Tool: "eve_ui_set_waypoint", ArgsDigest: "ab"}); err != nil {
		t.Fatal(err)
	}
	peek, ok, err := s.GetConfirmToken(ctx, "peek")
	if err != nil || !ok || peek.Tool != "eve_ui_set_waypoint" {
		t.Fatalf("get %+v ok %v err %v", peek, ok, err)
	}
	if err := s.DeleteConfirmToken(ctx, "peek"); err != nil {
		t.Fatal(err)
	}
	_, ok, err = s.GetConfirmToken(ctx, "peek")
	if err != nil || ok {
		t.Fatalf("deleted still there ok=%v err=%v", ok, err)
	}
	expired := ConfirmToken{Token: "old", UserID: "u", Tool: "eve_mail_send", ArgsDigest: "x"}
	if err := s.PutConfirmToken(ctx, expired); err != nil {
		t.Fatal(err)
	}
	_, err = s.pool.Exec(ctx, `UPDATE confirm_tokens SET created_at = now() - interval '10 minutes' WHERE token = 'old'`)
	if err != nil {
		t.Fatal(err)
	}
	_, ok, err = s.TakeConfirmToken(ctx, "old")
	if err != nil || ok {
		t.Fatalf("expired token must not honour, ok=%v err=%v", ok, err)
	}
}

func TestCacheAndNamesAndBlobs(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	body := json.RawMessage(`{"players":10}`)
	if err := s.CachePut(ctx, "/status", CachedResponse{
		Body: body, ETag: `"abc"`, ExpiresAt: time.Now().Add(time.Hour), Pages: new(2),
	}); err != nil {
		t.Fatal(err)
	}
	got, err := s.CacheGet(ctx, "/status")
	if err != nil || got == nil || !got.Fresh() || !jsonEqual(got.Body, body) || got.ETag != `"abc"` {
		t.Fatalf("cache %+v err %v", got, err)
	}
	if got.Pages == nil || *got.Pages != 2 {
		t.Fatalf("pages %v", got.Pages)
	}
	if err := s.CachePut(ctx, "/stale", CachedResponse{
		Body: json.RawMessage(`{}`), ExpiresAt: time.Now().Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	n, err := s.CachePurgeExpired(ctx)
	if err != nil || n < 1 {
		t.Fatalf("purge %d %v", n, err)
	}
	miss, err := s.CacheGet(ctx, "/stale")
	if err != nil || miss != nil {
		t.Fatalf("stale still there %v %v", miss, err)
	}

	if err := s.NamePut(ctx, []NameRow{{ID: 34, Name: "Tritanium", Category: "inventory_type"}}); err != nil {
		t.Fatal(err)
	}
	names, err := s.NameGet(ctx, []int64{34, 99})
	if err != nil || names[34].Name != "Tritanium" {
		t.Fatalf("names %v %v", names, err)
	}
	if _, ok := names[99]; ok {
		t.Fatal("unexpected 99")
	}
	if err := s.BlobPut(ctx, "prices", json.RawMessage(`{"ok":true}`)); err != nil {
		t.Fatal(err)
	}
	raw, err := s.BlobGet(ctx, "prices", nil)
	if err != nil || !jsonEqual(raw, json.RawMessage(`{"ok":true}`)) {
		t.Fatalf("blob %s %v", raw, err)
	}
	maxAge := time.Nanosecond
	time.Sleep(2 * time.Millisecond)
	raw, err = s.BlobGet(ctx, "prices", &maxAge)
	if err != nil || raw != nil {
		t.Fatalf("maxAge %s %v", raw, err)
	}
}

func TestGetOrCreateSecretStable(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	a, err := s.GetOrCreateSecret(ctx, "mcp_jwt_hmac")
	if err != nil || len(a) != SecretBytes {
		t.Fatalf("a %v %v", a, err)
	}
	b, err := s.GetOrCreateSecret(ctx, "mcp_jwt_hmac")
	if err != nil || string(a) != string(b) {
		t.Fatalf("unstable in same Open")
	}
	s2, err := Open(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s2.Close)
	c, err := s2.GetOrCreateSecret(ctx, "mcp_jwt_hmac")
	if err != nil || string(a) != string(c) {
		t.Fatalf("unstable across Open")
	}
}

func TestLoginStateAndPurge(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	u, err := s.CreateUser(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PutLoginState(ctx, LoginState{
		State: "st", PKCEVerifier: "v", Kind: LoginAlt, UserID: u.ID, Scopes: []string{"s"},
	}); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.GetLoginState(ctx, "st")
	if err != nil || !ok || got.Kind != LoginAlt || got.UserID != u.ID {
		t.Fatalf("%+v ok %v err %v", got, ok, err)
	}
	_, err = s.pool.Exec(ctx, `UPDATE login_states SET created_at = now() - interval '20 minutes' WHERE state = 'st'`)
	if err != nil {
		t.Fatal(err)
	}
	_, ok, err = s.GetLoginState(ctx, "st")
	if err != nil || ok {
		t.Fatalf("expired login still visible")
	}
	if err := s.PutLoginState(ctx, LoginState{
		State: "once", PKCEVerifier: "v2", Kind: LoginMCP,
	}); err != nil {
		t.Fatal(err)
	}
	got, ok, err = s.TakeLoginState(ctx, "once")
	if err != nil || !ok || got.Kind != LoginMCP {
		t.Fatalf("take %+v ok %v err %v", got, ok, err)
	}
	_, ok, err = s.TakeLoginState(ctx, "once")
	if err != nil || ok {
		t.Fatalf("second take ok=%v err=%v", ok, err)
	}
	if err := s.PutAuthCode(ctx, AuthCode{
		Code: "oldc", UserID: u.ID, MCPClientID: "c", RedirectURI: "r", CodeChallenge: "h",
		ExpiresAt: time.Now().Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	n, err := s.PurgeExpired(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n < 1 {
		t.Fatalf("purge count %d", n)
	}
}

func TestOAuthClient(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if err := s.PutClient(ctx, Client{ID: "cid", RedirectURIs: []string{"http://localhost/cb"}}); err != nil {
		t.Fatal(err)
	}
	c, ok, err := s.GetClient(ctx, "cid")
	if err != nil || !ok || len(c.RedirectURIs) != 1 {
		t.Fatalf("%+v ok %v err %v", c, ok, err)
	}
}

func TestMailLog(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := s.InsertMail(ctx, "u1", now.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertMail(ctx, "u1", now); err != nil {
		t.Fatal(err)
	}
	n, err := s.CountMailSince(ctx, "u1", now.Add(-time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("count %d %v", n, err)
	}
}

func jsonEqual(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	aj, _ := json.Marshal(x)
	bj, _ := json.Marshal(y)

	return string(aj) == string(bj)
}
