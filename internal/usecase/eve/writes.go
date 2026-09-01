package eve

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const clientCaveat = "Takes effect only while the EVE client is running and logged in on this character. With the client closed the call reports success and nothing visible happens."

func registerWrites(s *mcp.Server) {
	registerWaypoint(s)
	registerOpenWindow(s)
	registerWriteFittings(s)
	registerMailOrganize(s)
	registerMailSend(s)
	registerMailCompose(s)
	registerContacts(s)
	registerCalendar(s)
}
