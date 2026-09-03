package eve

import (
	"testing"

	"github.com/truewebber/eve-online-mcp/internal/j"
)

func TestSkillSubscriptionAlphaWhenAnySkillCapped(t *testing.T) {
	t.Parallel()
	kind, capped, ok := skillSubscription([]map[string]any{
		{esiTrainedLevel: 5, esiActiveLevel: 5},
		{esiTrainedLevel: 4, esiActiveLevel: 2},
	})
	if !ok || kind != vAlpha || capped != 1 {
		t.Fatalf("kind %q capped %d ok %v", kind, capped, ok)
	}
}

func TestSkillSubscriptionOmegaWhenLevelsMatch(t *testing.T) {
	t.Parallel()
	kind, capped, ok := skillSubscription([]map[string]any{
		{esiTrainedLevel: 5, esiActiveLevel: 5},
		{esiTrainedLevel: 1},
	})
	if !ok || kind != vOmega || capped != 0 {
		t.Fatalf("kind %q capped %d ok %v", kind, capped, ok)
	}
}

func TestSkillSubscriptionEmptyIsUnknown(t *testing.T) {
	t.Parallel()
	kind, capped, ok := skillSubscription(nil)
	if ok || kind != "" || capped != 0 {
		t.Fatalf("kind %q capped %d ok %v", kind, capped, ok)
	}
}

func TestSkillQueueCarriesOmegaSubscription(t *testing.T) {
	t.Parallel()
	body := asMap(t, mustCall(t, func() (any, error) {
		return eveCharacterSkillQueue(t.Context(), toolSession(t, newOverviewESI(sampleOverviewData()), false), empty{})
	}))
	if body[fSubscription] != vOmega {
		t.Fatalf("subscription %v", body[fSubscription])
	}
	if body["training_now"] != overviewTrainingNow {
		t.Fatalf("training_now %v", body["training_now"])
	}
}

func TestSkillQueueCarriesAlphaSubscription(t *testing.T) {
	t.Parallel()
	data := sampleOverviewData()
	data.skills = overviewSkillsPayload(5, 1)
	body := asMap(t, mustCall(t, func() (any, error) {
		return eveCharacterSkillQueue(t.Context(), toolSession(t, newOverviewESI(data), false), empty{})
	}))
	if body[fSubscription] != vAlpha {
		t.Fatalf("subscription %v", body[fSubscription])
	}
}

func TestSkillQueueKeepsQueueWhenSkillsFail(t *testing.T) {
	t.Parallel()
	client := newOverviewESI(sampleOverviewData())
	paths := fixtureOverviewPaths()
	client.byPath[paths.skills] = overviewBox{err: errInner}
	body := asMap(t, mustCall(t, func() (any, error) {
		return eveCharacterSkillQueue(t.Context(), toolSession(t, client, false), empty{})
	}))
	if body[fSubscription] != nil {
		t.Fatalf("subscription after skills error: %+v", body)
	}
	if j.Int(body["queued_skills"]) != 2 {
		t.Fatalf("queue lost after skills error: %+v", body)
	}
}

func TestCharacterSkillsReportsSubscription(t *testing.T) {
	t.Parallel()
	body := asMap(t, mustCall(t, func() (any, error) {
		return eveCharacterSkills(t.Context(), toolSession(t, newOverviewESI(sampleOverviewData()), false), characterSkillsIn{})
	}))
	if body[fSubscription] != vOmega {
		t.Fatalf("subscription %v", body[fSubscription])
	}
	if j.Int(body["skills_known"]) != 1 {
		t.Fatalf("skills_known %v", body["skills_known"])
	}
}
