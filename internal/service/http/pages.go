package httpsvc

import (
	"fmt"
	"html"
	"net/http"
	"strings"
)

func page(w http.ResponseWriter, title, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, pageTmpl, html.EscapeString(title), body)
}

const pageTmpl = `<!doctype html><meta charset=utf-8><title>%s</title>
<style>
  :root { color-scheme: light dark; }
  body { font: 15px/1.6 system-ui, sans-serif; max-width: 44rem; margin: 4rem auto; padding: 0 1.5rem; }
  h1 { font-size: 1.5rem; margin-bottom: .25rem; }
  h2 { font-size: 1rem; text-transform: uppercase; letter-spacing: .06em; opacity: .6; margin-top: 2rem; }
  ul { padding-left: 1.1rem; }
  code { background: rgba(127,127,127,.18); padding: .1em .35em; border-radius: 3px; }
  .dim { opacity: .65; }
  .warn { color: #c0392b; }
  .btn, button { display: inline-block; padding: .55rem 1rem; border-radius: 6px;
          background: #2b6cb0; color: #fff; text-decoration: none; border: 0; font: inherit; cursor: pointer; }
  label { display: block; margin: .8rem 0; }
  input { display: block; width: 100%%; margin-top: .25rem; padding: .45rem .55rem; font: inherit; box-sizing: border-box; }
</style>
%s
`

func shortGrant(w http.ResponseWriter, missing []string) {
	var b strings.Builder
	b.WriteString("<ul>")
	for _, scope := range missing {
		b.WriteString("<li><code>")
		b.WriteString(html.EscapeString(scope))
		b.WriteString("</code></li>")
	}
	b.WriteString("</ul>")
	writePage(w, pageShortGrant, b.String())
}
