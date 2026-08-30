// Package mcp is the MCP protocol surface. It depends only on usecase.
package mcp

import (
	"eve-mcp/internal/usecase/eve"
	"eve-mcp/internal/usecase/session"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func Instructions(corpScopes bool) string {
	if corpScopes {
		return eve.Instructions + eve.CorpInstructions
	}
	return eve.Instructions
}

func Register(s *mcp.Server, runtime *session.Session) {
	eve.Register(s, runtime)
}
