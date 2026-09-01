package esi

import (
	"fmt"
	"strings"
)

type NameMatch struct {
	ID       int
	Name     string
	Category string
	Kind     string
}

type NameResolution struct {
	Query        string
	Chosen       *NameMatch
	Alternatives []NameMatch
}

func (r NameResolution) Ambiguous() bool { return len(r.Alternatives) > 0 }

func (r NameResolution) Describe() string {
	if r.Chosen == nil {
		return fmt.Sprintf("%q matched nothing", r.Query)
	}
	others := make([]string, 0, len(r.Alternatives))
	for _, m := range r.Alternatives {
		others = append(others, article(m.Kind)+fmt.Sprintf(" (#%d)", m.ID))
	}
	chosen := article(r.Chosen.Kind) + fmt.Sprintf(" (#%d)", r.Chosen.ID)

	return fmt.Sprintf("%q is %s and also %s", r.Query, chosen, strings.Join(others, ", "))
}

func article(kind string) string {
	if kind == "" {
		return "a thing"
	}
	switch strings.ToLower(kind[:1]) {
	case "a", "e", "i", "o", "u":
		return "an " + kind
	}

	return "a " + kind
}

func CategoryKind(key string) string {
	switch key {
	case "agents":
		return "agent"
	case "alliances":
		return "alliance"
	case "characters":
		return "character"
	case "constellations":
		return "constellation"
	case "corporations":
		return "corporation"
	case "factions":
		return "faction"
	case "inventory_types":
		return "item type"
	case "regions":
		return "region"
	case "stations":
		return "station"
	case "systems":
		return "solar system"
	default:
		return ""
	}
}
