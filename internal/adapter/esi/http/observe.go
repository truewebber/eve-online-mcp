package http

import (
	nhttp "net/http"
	"time"
)

func (c *Client) observeRequest(method string, status int, req *nhttp.Request, d time.Duration) {
	c.observe.Request(method, status, req.Pattern, d)
}
