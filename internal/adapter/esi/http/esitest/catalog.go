package esitest

import nhttp "net/http"

const (
	CompatDate = "2026-08-18"

	SourceRecorded = "recorded"
	SourceOpenAPI  = "openapi"

	PublicCharacterID  = 196379789
	FixtureCharacterID = 2112000001
	JitaSystem         = 30000142
	AmarrSystem        = 30002187
	TritaniumType      = 34

	errorCharacterID = 1

	statusForbidden    = 403
	statusErrorLimited = 420
)

type Spec struct {
	Method string
	Path   string
	Query  map[string]string
	Body   any
	Auth   bool
	Status int
}

func Catalog() []Spec {
	wallet := Path("characters", id(FixtureCharacterID), "wallet")
	assets := Path("characters", id(FixtureCharacterID), "assets")
	mail := Path("characters", id(FixtureCharacterID), "mail")

	return []Spec{
		{Method: nhttp.MethodGet, Path: "/status"},
		{Method: nhttp.MethodGet, Path: Path("characters", id(PublicCharacterID))},
		{Method: nhttp.MethodGet, Path: wallet, Auth: true},
		{Method: nhttp.MethodGet, Path: assets, Query: map[string]string{"page": "1"}, Auth: true},
		{Method: nhttp.MethodGet, Path: assets, Query: map[string]string{"page": "2"}, Auth: true},
		{Method: nhttp.MethodGet, Path: mail, Auth: true},
		{Method: nhttp.MethodPost, Path: "/universe/names", Body: []int{TritaniumType, JitaSystem}},
		{Method: nhttp.MethodPost, Path: "/universe/ids", Body: []string{"Jita", "Tritanium"}},
		{Method: nhttp.MethodGet, Path: "/markets/prices"},
		{Method: nhttp.MethodPost, Path: Path("route", id(JitaSystem), id(AmarrSystem)), Body: map[string]any{"preference": "Shorter"}},
		{Method: nhttp.MethodGet, Path: Path("characters", id(errorCharacterID), "wallet"), Status: statusForbidden},
		{Method: nhttp.MethodGet, Path: Path("characters", id(errorCharacterID), "assets"), Status: statusErrorLimited},
	}
}
