package mcp

import (
	"github.com/truewebber/eve-online-mcp/internal/usecase/eve"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func Instructions() string {
	return eve.Instructions + eve.CorpInstructions
}

func Register(s *mcp.Server) {
	eve.Register(s)
}
