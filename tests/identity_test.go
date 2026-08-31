package tests

import (
	"strconv"
	"strings"
	"testing"

	"github.com/truewebber/eve-online-mcp/internal/adapter/esi/http/esitest"

	"github.com/golang-jwt/jwt/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestAccessTokenSubIsCharacterID(t *testing.T) {
	t.Parallel()
	e := openEnv(t)
	tok, err := jwt.Parse(e.token, func(*jwt.Token) (any, error) {
		return []byte(testHMACKey), nil
	})
	if err != nil || !tok.Valid {
		t.Fatalf("jwt %v", err)
	}
	claims, ok := tok.Claims.(jwt.MapClaims)
	if !ok {
		t.Fatal("claims")
	}
	if claims["sub"] != strconv.Itoa(esitest.FixtureCharacterID) {
		t.Fatalf("sub %v", claims["sub"])
	}
}

func TestExtraCharacterArgumentIsRefused(t *testing.T) {
	t.Parallel()
	e := openEnv(t)
	res, err := e.client.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "eve_character_overview",
		Arguments: map[string]any{
			"character": "Someone Else",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	text := toolText(res)
	if !strings.Contains(text, "character") {
		t.Fatalf("want a refusal that names the extra field, got %q", text)
	}
	if strings.Contains(text, fixtureName) {
		t.Fatalf("handler ran despite extra character argument: %s", text)
	}
}
