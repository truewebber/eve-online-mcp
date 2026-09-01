package httpsvc

import (
	"errors"
	"fmt"
	"html"
	"net/http"

	"github.com/truewebber/eve-online-mcp/internal/usecase/oauth"
)

type pageErr struct {
	title    string
	sentence string
	status   int
}

var errCCPRefused = errors.New("http: ccp refused")

//nolint:gochecknoglobals // RULES §9: the human-page catalog is a closed set of values, not process state.
var (
	pageUnknownLogin = pageErr{
		title:    titleFailed,
		sentence: "Unknown or expired login state. Start the login again from the client.",
		status:   http.StatusBadRequest,
	}
	pageRefused = pageErr{
		title:    titleRefused,
		sentence: "The EVE login was refused. Start the login again from the client.",
		status:   http.StatusBadRequest,
	}
	pageMismatch = pageErr{
		title:    titleFailed,
		sentence: "This login did not match the client that started it. Start the login again from the client.",
		status:   http.StatusBadRequest,
	}
	pageShortGrant = pageErr{
		title:    titleRefused,
		sentence: "This server needs every scope listed on the instance application at developers.eveonline.com. Add the missing ones and sign in again from the client.",
		status:   http.StatusBadRequest,
	}
	pageUnavailable = pageErr{
		title:    titleFailed,
		sentence: "This server could not finish the login. Try again in a moment.",
		status:   http.StatusServiceUnavailable,
	}
	pageGeneric = pageErr{
		title:    titleFailed,
		sentence: "Login failed. Start the login again from the client.",
		status:   http.StatusInternalServerError,
	}
	pageBadCallback = pageErr{
		title:    "Bad callback",
		sentence: "Missing code or state.",
		status:   http.StatusBadRequest,
	}
)

const (
	titleFailed  = "Login failed"
	titleRefused = "Login refused"
)

func lookup(err error) pageErr {
	switch {
	case errors.Is(err, oauth.ErrUnknownLogin):
		return pageUnknownLogin
	case errors.Is(err, oauth.ErrClientMismatch):
		return pageMismatch
	case errors.Is(err, oauth.ErrLoginRefused):
		return pageRefused
	case errors.Is(err, oauth.ErrUnavailable):
		return pageUnavailable
	default:
		return pageGeneric
	}
}

func writePage(w http.ResponseWriter, entry pageErr, extra string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(entry.status)
	body := "<h1>" + html.EscapeString(entry.title) + "</h1><p class=warn>" +
		html.EscapeString(entry.sentence) + "</p>" + extra
	fmt.Fprintf(w, pageTmpl, html.EscapeString(entry.title), body)
}
