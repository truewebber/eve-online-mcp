package eve

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/truewebber/eve-online-mcp/internal/adapter/esi"
	"github.com/truewebber/eve-online-mcp/internal/adapter/esi/http/esitest"
	"github.com/truewebber/eve-online-mcp/internal/j"
	"github.com/truewebber/eve-online-mcp/internal/usecase/session"
)

const (
	overviewCorpID       = 98_667_722
	overviewAllianceID   = 99_009_901
	overviewSystemID     = 30_000_142
	overviewStationID    = 60_003_760
	overviewShipTypeID   = 33_474
	overviewSkillID      = 11_584
	overviewWalletISK    = 174_760_000.0
	overviewRemaps       = 2
	overviewAgePublic    = 1.0
	overviewAgeWallet    = 2.0
	overviewAgeLoc       = 3.0
	overviewAgeShip      = 4.0
	overviewAgeOnline    = 5.0
	overviewAgeQueue     = 6.0
	overviewAgeAttrs     = 7.0
	overviewAgeSkills    = 8.0
	overviewJitterRuns   = 40
	overviewKeySystem    = "solar_system_id"
	overviewKeyLevel     = "finished_level"
	overviewKeyPosition  = "queue_position"
	overviewKeySkill     = "skill_id"
	overviewTrainingNow  = "Anchoring IV"
	overviewQueueEnd     = "2026-10-17T01:34:56Z"
	overviewLastLogin    = "2026-08-30T20:31:29Z"
	overviewSystemName   = "Jita"
	overviewStationName  = "Jita IV - Moon 4 - Caldari Navy Assembly Plant"
	overviewShipTypeName = "Venture"
	overviewShipName     = "Pioneer-S-2"
	overviewCorpName     = "Distinct Test Corp"
	overviewAllianceName = "Distinct Test Alliance"
	overviewWalletFmt    = "174.76m"
	overviewJitterMod    = 15
)

var errUnexpectedESI = errors.New("unexpected esi call")

func TestApplyOverviewWalletKeepsNumericBalance(t *testing.T) {
	t.Parallel()
	out := map[string]any{}
	applyOverviewWallet(overviewBox{r: esi.Result{Data: overviewWalletISK}}, out)
	if out["wallet_isk"] != overviewWalletISK {
		t.Fatalf("wallet_isk %v", out["wallet_isk"])
	}
	if out[fWallet] != overviewWalletFmt {
		t.Fatalf("wallet %q", out[fWallet])
	}
}

func TestApplyOverviewWalletOmitsFailedFetch(t *testing.T) {
	t.Parallel()
	out := map[string]any{"keep": true}
	applyOverviewWallet(overviewBox{err: errInner}, out)
	if _, ok := out["wallet_isk"]; ok {
		t.Fatalf("wallet_isk on error: %+v", out)
	}
	if out["keep"] != true {
		t.Fatal("existing fields must stay")
	}
}

func TestApplyOverviewWalletDoesNotInventZeroFromObject(t *testing.T) {
	t.Parallel()
	out := map[string]any{}
	applyOverviewWallet(overviewBox{r: esi.Result{Data: map[string]any{
		"bonus_remaps": overviewRemaps, "intelligence": 20,
	}}}, out)
	if out[fWallet] == "0.00" {
		t.Fatal("non-numeric wallet payload formatted as 0.00 ISK")
	}
	if _, ok := out["wallet_isk"].(map[string]any); ok {
		t.Fatal("attributes object leaked into wallet_isk")
	}
}

func TestApplyOverviewQueueReportsTraining(t *testing.T) {
	t.Parallel()
	out := map[string]any{}
	a := toolSession(t, newOverviewESI(sampleOverviewData()), false)
	applyOverviewQueue(t.Context(), a, overviewBox{r: esi.Result{Data: sampleOverviewData().queue}}, out)
	if out["training_now"] != overviewTrainingNow {
		t.Fatalf("training_now %v", out["training_now"])
	}
	if out["queue_length"] != 2 {
		t.Fatalf("queue_length %v", out["queue_length"])
	}
	if out["queue_ends"] != overviewQueueEnd {
		t.Fatalf("queue_ends %v", out["queue_ends"])
	}
	if out["warning"] != nil {
		t.Fatalf("warning on a live queue: %v", out["warning"])
	}
}

func TestApplyOverviewQueueEmptySliceWarns(t *testing.T) {
	t.Parallel()
	out := map[string]any{}
	applyOverviewQueue(t.Context(), &session.Session{}, overviewBox{r: esi.Result{Data: []any{}}}, out)
	if out["warning"] == nil {
		t.Fatal("empty skillqueue must warn")
	}
}

func TestApplyOverviewQueueDoesNotTreatObjectAsEmpty(t *testing.T) {
	t.Parallel()
	out := map[string]any{}
	applyOverviewQueue(t.Context(), &session.Session{}, overviewBox{r: esi.Result{Data: map[string]any{
		overviewKeySystem: overviewSystemID,
	}}}, out)
	if out["warning"] != nil {
		t.Fatalf("non-list payload treated as empty queue: %v", out["warning"])
	}
}

func TestApplyOverviewQueueSkipsPausedEntries(t *testing.T) {
	t.Parallel()
	out := map[string]any{}
	applyOverviewQueue(t.Context(), &session.Session{}, overviewBox{r: esi.Result{Data: []any{
		map[string]any{overviewKeySkill: overviewSkillID, overviewKeyLevel: 4, overviewKeyPosition: 0},
	}}}, out)
	if out["warning"] == nil {
		t.Fatal("paused-only queue should warn")
	}
	if out["training_now"] != nil {
		t.Fatalf("paused entry became training_now: %v", out["training_now"])
	}
}

func TestApplyOverviewSubscriptionReportsAlpha(t *testing.T) {
	t.Parallel()
	out := map[string]any{}
	applyOverviewSubscription(overviewBox{r: esi.Result{Data: overviewSkillsPayload(5, 4)}}, out)
	if out[fSubscription] != vAlpha {
		t.Fatalf("subscription %v", out[fSubscription])
	}
}

func TestApplyOverviewSubscriptionReportsOmega(t *testing.T) {
	t.Parallel()
	out := map[string]any{}
	applyOverviewSubscription(overviewBox{r: esi.Result{Data: overviewSkillsPayload(4, 4)}}, out)
	if out[fSubscription] != vOmega {
		t.Fatalf("subscription %v", out[fSubscription])
	}
}

func TestApplyOverviewSubscriptionOmitsFailedFetch(t *testing.T) {
	t.Parallel()
	out := map[string]any{"keep": true}
	applyOverviewSubscription(overviewBox{err: errInner}, out)
	if out[fSubscription] != nil {
		t.Fatalf("subscription on error: %+v", out)
	}
	if out["keep"] != true {
		t.Fatal("existing fields must stay")
	}
}

func TestApplyOverviewSubscriptionOmitsEmptySkills(t *testing.T) {
	t.Parallel()
	out := map[string]any{}
	applyOverviewSubscription(overviewBox{r: esi.Result{Data: map[string]any{esiSkills: []any{}, "total_sp": 0}}}, out)
	if out[fSubscription] != nil {
		t.Fatalf("subscription from empty skills: %+v", out)
	}
}

func TestCharacterOverviewReportsAlphaSubscription(t *testing.T) {
	t.Parallel()
	data := sampleOverviewData()
	data.skills = overviewSkillsPayload(5, 2)
	body := asMap(t, mustCall(t, func() (any, error) {
		return eveCharacterOverview(t.Context(), toolSession(t, newOverviewESI(data), false), empty{})
	}))
	if body[fSubscription] != vAlpha {
		t.Fatalf("subscription %v", body[fSubscription])
	}
}

func TestApplyOverviewOnlineAndShipAndLocation(t *testing.T) {
	t.Parallel()
	a := toolSession(t, newOverviewESI(sampleOverviewData()), false)
	out := map[string]any{}
	data := sampleOverviewData()
	applyOverviewOnline(overviewBox{r: esi.Result{Data: data.online}}, out)
	applyOverviewLocation(t.Context(), a, esitest.FixtureCharacterID, overviewBox{r: esi.Result{Data: data.location}}, out)
	applyOverviewShip(t.Context(), a, overviewBox{r: esi.Result{Data: data.ship}}, out)
	if out["online"] != false {
		t.Fatalf("online %v", out["online"])
	}
	if out["last_login"] != overviewLastLogin {
		t.Fatalf("last_login %v", out["last_login"])
	}
	if out["solar_system"] != overviewSystemName {
		t.Fatalf("solar_system %v", out["solar_system"])
	}
	if out["docked_at"] != overviewStationName {
		t.Fatalf("docked_at %v", out["docked_at"])
	}
	if out["ship_type"] != overviewShipTypeName {
		t.Fatalf("ship_type %v", out["ship_type"])
	}
	if out["ship_name"] != overviewShipName {
		t.Fatalf("ship_name %v", out["ship_name"])
	}
}

func TestApplyOverviewLocationInSpace(t *testing.T) {
	t.Parallel()
	a := toolSession(t, newOverviewESI(sampleOverviewData()), false)
	out := map[string]any{}
	applyOverviewLocation(t.Context(), a, esitest.FixtureCharacterID, overviewBox{r: esi.Result{Data: map[string]any{
		overviewKeySystem: overviewSystemID,
	}}}, out)
	if out["docked_at"] != "in space" {
		t.Fatalf("docked_at %v", out["docked_at"])
	}
}

func TestApplyOverviewPublicResolvesCorp(t *testing.T) {
	t.Parallel()
	a := toolSession(t, newOverviewESI(sampleOverviewData()), false)
	out := map[string]any{}
	applyOverviewPublic(t.Context(), a, overviewBox{r: esi.Result{Data: sampleOverviewData().public}}, out)
	if out[fCorporation] != overviewCorpName {
		t.Fatalf("corporation %v", out[fCorporation])
	}
	if out[fAlliance] != overviewAllianceName {
		t.Fatalf("alliance %v", out[fAlliance])
	}
	if out["security_status"] != 1.23 {
		t.Fatalf("security_status %v", out["security_status"])
	}
}

func TestFetchOverviewAssignsSlotsInLaunchOrder(t *testing.T) {
	t.Parallel()
	got := runGatedOverview(t, overviewLaunchOrder)
	assertOverviewSlots(t, got)
}

func TestFetchOverviewAssignsSlotsWhenResponsesArriveReversed(t *testing.T) {
	t.Parallel()
	got := runGatedOverview(t, overviewReverseOrder)
	assertOverviewSlots(t, got)
}

func TestFetchOverviewAssignsSlotsUnderRandomJitter(t *testing.T) {
	t.Parallel()
	for i := range overviewJitterRuns {
		got := fetchOverview(t.Context(), &session.Session{ESI: newJitterESI(t, i)}, esitest.FixtureCharacterID)
		assertOverviewSlots(t, got)
	}
}

func TestFetchOverviewRequestsEachEndpointOnce(t *testing.T) {
	t.Parallel()
	client := newOverviewESI(sampleOverviewData())
	_ = fetchOverview(t.Context(), &session.Session{ESI: client}, esitest.FixtureCharacterID)
	paths := fixtureOverviewPaths()
	want := []string{paths.public, paths.wallet, paths.location, paths.ship, paths.online, paths.queue, paths.attributes, paths.skills}
	got := client.callsSnapshot()
	if len(got) != overviewFetches {
		t.Fatalf("calls %d: %v", len(got), got)
	}
	seen := map[string]int{}
	for _, p := range got {
		seen[p]++
	}
	for _, p := range want {
		if seen[p] != 1 {
			t.Fatalf("path %s count %d in %v", p, seen[p], got)
		}
	}
}

func TestFetchOverviewKeepsPerEndpointErrors(t *testing.T) {
	t.Parallel()
	data := sampleOverviewData()
	client := newOverviewESI(data)
	paths := fixtureOverviewPaths()
	client.byPath[paths.wallet] = overviewBox{err: errInner}
	got := fetchOverview(t.Context(), &session.Session{ESI: client}, esitest.FixtureCharacterID)
	if !errors.Is(got.wallet.err, errInner) {
		t.Fatalf("wallet err %v", got.wallet.err)
	}
	if !reflect.DeepEqual(got.queue.r.Data, data.queue) {
		t.Fatalf("queue slot polluted after wallet error: %v", got.queue.r.Data)
	}
	if !reflect.DeepEqual(got.attributes.r.Data, data.attributes) {
		t.Fatalf("attributes slot polluted after wallet error: %v", got.attributes.r.Data)
	}
	if !reflect.DeepEqual(got.skills.r.Data, data.skills) {
		t.Fatalf("skills slot polluted after wallet error: %v", got.skills.r.Data)
	}
}

func TestCharacterOverviewMapsEveryESISection(t *testing.T) {
	t.Parallel()
	body := callGatedOverview(t, overviewLaunchOrder)
	assertOverviewBody(t, body)
}

func TestCharacterOverviewMapsEveryESISectionWhenReversed(t *testing.T) {
	t.Parallel()
	body := callGatedOverview(t, overviewReverseOrder)
	assertOverviewBody(t, body)
}

func TestCharacterOverviewPartialWalletErrorKeepsQueue(t *testing.T) {
	t.Parallel()
	client := newOverviewESI(sampleOverviewData())
	paths := fixtureOverviewPaths()
	client.byPath[paths.wallet] = overviewBox{err: errInner}
	body := asMap(t, mustCall(t, func() (any, error) {
		return eveCharacterOverview(t.Context(), toolSession(t, client, false), empty{})
	}))
	if body[fWallet] != nil || body["wallet_isk"] != nil {
		t.Fatalf("wallet fields after error: %+v", body)
	}
	if body["training_now"] != overviewTrainingNow {
		t.Fatalf("queue lost after wallet error: %+v", body)
	}
	if body["remaps_available"] != float64(overviewRemaps) {
		t.Fatalf("remaps %v", body["remaps_available"])
	}
	if body[fSubscription] != vOmega {
		t.Fatalf("subscription %v", body[fSubscription])
	}
}

func TestCharacterOverviewHTTPFixturesKeepWalletBalance(t *testing.T) {
	t.Parallel()
	var zeros int
	for range overviewJitterRuns {
		body := asMap(t, mustCall(t, func() (any, error) {
			return eveCharacterOverview(t.Context(), fixtureSession(t), empty{})
		}))
		if body[fWallet] == "0.00" {
			zeros++
		}
		if body[fWallet] != "1.00m" {
			t.Fatalf("wallet %v wallet_isk %T %v", body[fWallet], body["wallet_isk"], body["wallet_isk"])
		}
	}
	if zeros != 0 {
		t.Fatalf("wallet became 0.00 on %d/%d runs", zeros, overviewJitterRuns)
	}
}

type overviewOrder func(overviewPaths) []string

func overviewLaunchOrder(p overviewPaths) []string {
	return []string{p.public, p.wallet, p.location, p.ship, p.online, p.queue, p.attributes, p.skills}
}

func overviewReverseOrder(p overviewPaths) []string {
	return []string{p.skills, p.attributes, p.queue, p.online, p.ship, p.location, p.wallet, p.public}
}

func runGatedOverview(t *testing.T, order overviewOrder) overviewFetch {
	t.Helper()
	client := newGatedOverviewESI(sampleOverviewData())
	paths := fixtureOverviewPaths()
	done := make(chan overviewFetch, 1)
	go func() {
		done <- fetchOverview(t.Context(), &session.Session{ESI: client}, esitest.FixtureCharacterID)
	}()
	waitOverviewStarts(t, client.started, overviewLaunchOrder(paths))
	releaseOverviewInOrder(t, client, order(paths))
	select {
	case got := <-done:
		return got
	case <-time.After(2 * time.Second):
		t.Fatal("fetchOverview hung")
	}

	return overviewFetch{}
}

func callGatedOverview(t *testing.T, order overviewOrder) map[string]any {
	t.Helper()
	client := newGatedOverviewESI(sampleOverviewData())
	paths := fixtureOverviewPaths()
	a := toolSession(t, client, false)
	type result struct {
		body map[string]any
		err  error
	}
	done := make(chan result, 1)
	go func() {
		got, err := eveCharacterOverview(t.Context(), a, empty{})
		if err != nil {
			done <- result{err: err}

			return
		}
		done <- result{body: asMap(t, got)}
	}()
	waitOverviewStarts(t, client.started, overviewLaunchOrder(paths))
	releaseOverviewInOrder(t, client, order(paths))
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatal(got.err)
		}

		return got.body
	case <-time.After(2 * time.Second):
		t.Fatal("eveCharacterOverview hung")
	}

	return nil
}

func assertOverviewSlots(t *testing.T, got overviewFetch) {
	t.Helper()
	data := sampleOverviewData()
	cases := []struct {
		name string
		box  overviewBox
		want any
		age  float64
	}{
		{"public", got.public, data.public, overviewAgePublic},
		{"wallet", got.wallet, data.wallet, overviewAgeWallet},
		{"location", got.location, data.location, overviewAgeLoc},
		{fShip, got.ship, data.ship, overviewAgeShip},
		{"online", got.online, data.online, overviewAgeOnline},
		{fQueue, got.queue, data.queue, overviewAgeQueue},
		{"attributes", got.attributes, data.attributes, overviewAgeAttrs},
		{esiSkills, got.skills, data.skills, overviewAgeSkills},
	}
	for _, tc := range cases {
		if tc.box.err != nil {
			t.Fatalf("%s err %v", tc.name, tc.box.err)
		}
		if !reflect.DeepEqual(tc.box.r.Data, tc.want) {
			t.Fatalf("%s data %T %v want %T %v", tc.name, tc.box.r.Data, tc.box.r.Data, tc.want, tc.want)
		}
		if tc.box.r.AgeSeconds != tc.age {
			t.Fatalf("%s age %v want %v (slot received a different endpoint)", tc.name, tc.box.r.AgeSeconds, tc.age)
		}
	}
}

func assertOverviewBody(t *testing.T, body map[string]any) {
	t.Helper()
	assertOverviewIdentity(t, body)
	assertOverviewWalletBody(t, body)
	assertOverviewWhere(t, body)
	assertOverviewTraining(t, body)
}

func assertOverviewIdentity(t *testing.T, body map[string]any) {
	t.Helper()
	if body[fName] != fixturePilotName {
		t.Fatalf("name %v", body[fName])
	}
	if body[fCorporation] != overviewCorpName {
		t.Fatalf("corporation %v", body[fCorporation])
	}
	if body[fAlliance] != overviewAllianceName {
		t.Fatalf("alliance %v", body[fAlliance])
	}
}

func assertOverviewWalletBody(t *testing.T, body map[string]any) {
	t.Helper()
	if body[fWallet] != overviewWalletFmt {
		t.Fatalf("wallet %v", body[fWallet])
	}
	if j.Float(body["wallet_isk"]) != overviewWalletISK {
		t.Fatalf("wallet_isk %T %v", body["wallet_isk"], body["wallet_isk"])
	}
	if _, ok := body["wallet_isk"].(map[string]any); ok {
		t.Fatalf("wallet_isk is an object: %v", body["wallet_isk"])
	}
}

func assertOverviewWhere(t *testing.T, body map[string]any) {
	t.Helper()
	if body["online"] != false {
		t.Fatalf("online %v", body["online"])
	}
	if body["last_login"] != overviewLastLogin {
		t.Fatalf("last_login %v", body["last_login"])
	}
	if body["solar_system"] != overviewSystemName {
		t.Fatalf("solar_system %v", body["solar_system"])
	}
	if body["docked_at"] != overviewStationName {
		t.Fatalf("docked_at %v", body["docked_at"])
	}
	if body["ship_type"] != overviewShipTypeName {
		t.Fatalf("ship_type %v", body["ship_type"])
	}
	if body["ship_name"] != overviewShipName {
		t.Fatalf("ship_name %v", body["ship_name"])
	}
}

func assertOverviewTraining(t *testing.T, body map[string]any) {
	t.Helper()
	if body["training_now"] != overviewTrainingNow {
		t.Fatalf("training_now %v warning %v", body["training_now"], body["warning"])
	}
	if body["queue_ends"] != overviewQueueEnd {
		t.Fatalf("queue_ends %v", body["queue_ends"])
	}
	if body["remaps_available"] != float64(overviewRemaps) {
		t.Fatalf("remaps_available %v", body["remaps_available"])
	}
	if body[fSubscription] != vOmega {
		t.Fatalf("subscription %v", body[fSubscription])
	}
	if body["warning"] != nil {
		t.Fatalf("invented empty queue: %v", body["warning"])
	}
	if !strings.Contains(j.Str(body[fDataAge]), "old") {
		t.Fatalf("data_age %v", body[fDataAge])
	}
}

type overviewPaths struct {
	public, wallet, location, ship, online, queue, attributes, skills string
}

func fixtureOverviewPaths() overviewPaths {
	cid := esitest.FixtureCharacterID

	return overviewPaths{
		public:     esiPath("characters", esiID(cid)).String(),
		wallet:     esiPath("characters", esiID(cid), fWallet).String(),
		location:   esiPath("characters", esiID(cid), fLocation).String(),
		ship:       esiPath("characters", esiID(cid), fShip).String(),
		online:     esiPath("characters", esiID(cid), "online").String(),
		queue:      esiPath("characters", esiID(cid), "skillqueue").String(),
		attributes: esiPath("characters", esiID(cid), "attributes").String(),
		skills:     esiPath("characters", esiID(cid), esiSkills).String(),
	}
}

type overviewESIData struct {
	public, wallet, location, ship, online, queue, attributes, skills any
}

func sampleOverviewData() overviewESIData {
	return overviewESIData{
		public: map[string]any{
			"name": fixturePilotName, "corporation_id": overviewCorpID,
			"alliance_id": overviewAllianceID, "security_status": 1.23,
			"birthday": "2010-01-01T00:00:00Z",
		},
		wallet: overviewWalletISK,
		location: map[string]any{
			overviewKeySystem: overviewSystemID, "station_id": overviewStationID,
		},
		ship: map[string]any{
			fShipTypeID: overviewShipTypeID, "ship_name": overviewShipName,
		},
		online: map[string]any{
			"online": false, "last_login": overviewLastLogin,
		},
		queue: []any{
			map[string]any{
				overviewKeyPosition: 0, overviewKeySkill: overviewSkillID,
				overviewKeyLevel: 4, "finish_date": "2026-09-02T23:04:43Z",
			},
			map[string]any{
				overviewKeyPosition: 1, overviewKeySkill: overviewSkillID,
				overviewKeyLevel: 5, "finish_date": overviewQueueEnd,
			},
		},
		attributes: map[string]any{
			"bonus_remaps": overviewRemaps, "intelligence": 20, "memory": 22,
		},
		skills: overviewSkillsPayload(4, 4),
	}
}

func overviewSkillsPayload(trained, active int) map[string]any {
	return map[string]any{
		"total_sp": 1000,
		esiSkills: []any{
			map[string]any{
				"skill_id": overviewSkillID, esiTrainedLevel: trained,
				esiActiveLevel: active,
			},
		},
	}
}

func overviewNames() map[int]string {
	return map[int]string{
		overviewCorpID:     overviewCorpName,
		overviewAllianceID: overviewAllianceName,
		overviewSystemID:   overviewSystemName,
		overviewStationID:  overviewStationName,
		overviewShipTypeID: overviewShipTypeName,
		overviewSkillID:    "Anchoring",
	}
}

type overviewESI struct {
	mu       sync.Mutex
	calls    []string
	byPath   map[string]overviewBox
	names    map[int]string
	started  chan string
	finished chan string
	release  map[string]chan struct{}
	jitter   func(string) time.Duration
}

func newOverviewESI(data overviewESIData) *overviewESI {
	return &overviewESI{byPath: overviewBoxes(data), names: overviewNames()}
}

func newGatedOverviewESI(data overviewESIData) *overviewESI {
	paths := fixtureOverviewPaths()
	release := map[string]chan struct{}{}
	for _, p := range overviewLaunchOrder(paths) {
		release[p] = make(chan struct{})
	}

	return &overviewESI{
		byPath:   overviewBoxes(data),
		names:    overviewNames(),
		started:  make(chan string, overviewFetches),
		finished: make(chan string, overviewFetches),
		release:  release,
	}
}

func newJitterESI(t *testing.T, seed int) *overviewESI {
	t.Helper()
	delays := map[string]time.Duration{}
	for i, path := range overviewLaunchOrder(fixtureOverviewPaths()) {
		delays[path] = time.Duration((seed*7+i)%overviewJitterMod) * time.Millisecond
	}

	return &overviewESI{
		byPath: overviewBoxes(sampleOverviewData()),
		names:  overviewNames(),
		jitter: func(path string) time.Duration { return delays[path] },
	}
}

func overviewBoxes(data overviewESIData) map[string]overviewBox {
	p := fixtureOverviewPaths()

	return map[string]overviewBox{
		p.public:     {r: esi.Result{Data: data.public, AgeSeconds: overviewAgePublic}},
		p.wallet:     {r: esi.Result{Data: data.wallet, AgeSeconds: overviewAgeWallet}},
		p.location:   {r: esi.Result{Data: data.location, AgeSeconds: overviewAgeLoc}},
		p.ship:       {r: esi.Result{Data: data.ship, AgeSeconds: overviewAgeShip}},
		p.online:     {r: esi.Result{Data: data.online, AgeSeconds: overviewAgeOnline}},
		p.queue:      {r: esi.Result{Data: data.queue, AgeSeconds: overviewAgeQueue}},
		p.attributes: {r: esi.Result{Data: data.attributes, AgeSeconds: overviewAgeAttrs}},
		p.skills:     {r: esi.Result{Data: data.skills, AgeSeconds: overviewAgeSkills}},
	}
}

func (e *overviewESI) Get(ctx context.Context, path esi.Route, _ *int, _ map[string]any, _ *float64) (esi.Result, error) {
	key := path.String()
	e.mu.Lock()
	e.calls = append(e.calls, key)
	e.mu.Unlock()
	if e.started != nil {
		select {
		case e.started <- key:
		case <-ctx.Done():
			return esi.Result{}, wrap("overviewESI.Get", ctx.Err())
		}
	}
	if ch := e.release[key]; ch != nil {
		select {
		case <-ch:
		case <-ctx.Done():
			return esi.Result{}, wrap("overviewESI.Get", ctx.Err())
		}
	}
	if e.jitter != nil {
		select {
		case <-time.After(e.jitter(key)):
		case <-ctx.Done():
			return esi.Result{}, wrap("overviewESI.Get", ctx.Err())
		}
	}
	box, ok := e.byPath[key]
	if !ok {
		return esi.Result{}, errUnexpectedESI
	}
	if e.finished != nil {
		select {
		case e.finished <- key:
		case <-ctx.Done():
			return esi.Result{}, wrap("overviewESI.Get", ctx.Err())
		}
	}

	return box.r, box.err
}

func (e *overviewESI) Post(_ context.Context, path esi.Route, _ *int, _ map[string]any, jsonBody any) (any, error) {
	if path.String() != "/universe/names" {
		return nil, errUnexpectedESI
	}
	var rows []any
	for _, id := range overviewNameIDs(jsonBody) {
		if name, ok := e.names[id]; ok {
			rows = append(rows, map[string]any{"id": id, "name": name})
		}
	}

	return rows, nil
}

func (e *overviewESI) ForUser(esi.TokenSource) esi.Client { return e }

func (e *overviewESI) GetAllPages(context.Context, esi.Route, *int, map[string]any, int) (esi.Result, error) {
	return esi.Result{}, errUnexpectedESI
}

func (e *overviewESI) GetCursorPages(context.Context, esi.Route, esi.CursorQuery) (esi.Result, error) {
	return esi.Result{}, errUnexpectedESI
}

func (e *overviewESI) Put(context.Context, esi.Route, *int, map[string]any, any) (any, error) {
	return nil, errUnexpectedESI
}

func (e *overviewESI) Delete(context.Context, esi.Route, *int, map[string]any, any) (any, error) {
	return nil, errUnexpectedESI
}

func (e *overviewESI) callsSnapshot() []string {
	e.mu.Lock()
	defer e.mu.Unlock()

	return append([]string{}, e.calls...)
}

func waitOverviewStarts(t *testing.T, started <-chan string, want []string) {
	t.Helper()
	seen := map[string]int{}
	deadline := time.After(2 * time.Second)
	for range want {
		select {
		case path := <-started:
			seen[path]++
		case <-deadline:
			t.Fatalf("started %v want %v", seen, want)
		}
	}
	for _, path := range want {
		if seen[path] != 1 {
			t.Fatalf("start count %s=%d in %v", path, seen[path], seen)
		}
	}
}

func releaseOverviewInOrder(t *testing.T, client *overviewESI, order []string) {
	t.Helper()
	for _, path := range order {
		close(client.release[path])
		select {
		case got := <-client.finished:
			if got != path {
				t.Fatalf("finished %s want %s", got, path)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("Get %s did not finish", path)
		}
		// Get returns into `ch <- box`. Give that send a turn before the next
		// endpoint is allowed to complete, otherwise the gate only orders HTTP
		// returns and the overview channel can still scramble them.
		time.Sleep(10 * time.Millisecond)
	}
}

func overviewNameIDs(body any) []int {
	switch typed := body.(type) {
	case []int:
		return typed
	case []any:
		out := make([]int, 0, len(typed))
		for _, v := range typed {
			out = append(out, j.Int(v))
		}

		return out
	default:
		return nil
	}
}
