package eve

import (
	"testing"

	"github.com/truewebber/eve-online-mcp/internal/adapter/esi"
	"github.com/truewebber/eve-online-mcp/internal/j"
)

func TestPageByCursorLimitAndESIFull(t *testing.T) {
	t.Parallel()
	rows := []map[string]any{{fMailID: 30}, {fMailID: 20}, {fMailID: 10}}
	clipped := pageByCursor(cursorPageIn{Shown: rows, Limit: 2, Key: fMailID, Hint: "Pass next_cursor as last_mail_id.", ESI: rows})
	if len(clipped.Rows) != 2 || clipped.NextCursor != 20 {
		t.Fatalf("limit cursor %+v", clipped)
	}
	if !j.Bool(clipped.fields[fTruncated]) {
		t.Fatal("limit must label truncation")
	}
	full := make([]map[string]any, esiCursorPage)
	for i := range full {
		full[i] = map[string]any{fMailID: 1000 + i}
	}
	all := pageByCursor(cursorPageIn{Shown: full, Limit: esiCursorPage, Key: fMailID, ESI: full})
	if all.NextCursor != 1000+esiCursorPage-1 {
		t.Fatalf("full ESI page cursor %d", all.NextCursor)
	}
	short := pageByCursor(cursorPageIn{Shown: rows, Limit: 10, Key: fMailID, ESI: rows})
	if short.NextCursor != 0 {
		t.Fatalf("short page cursor %d", short.NextCursor)
	}
}

func TestPageByNumberCarriesPages(t *testing.T) {
	t.Parallel()
	rows := []map[string]any{{fName: "a"}, {fName: "b"}, {fName: "c"}}
	paged := pageByNumber(rows, 2, 4, 2)
	if paged.Page != 2 || paged.TotalPages != 4 || len(paged.Rows) != 2 {
		t.Fatalf("%+v", paged)
	}
	if j.Int(paged.fields[fPage]) != 2 || j.Int(paged.fields[fTotalPages]) != 4 {
		t.Fatalf("fields %+v", paged.fields)
	}
	if !j.Bool(paged.fields[fTruncated]) {
		t.Fatal("truncated")
	}
	zero := pageByNumber(nil, 0, 0, 5)
	if zero.Page != 1 || zero.TotalPages != 1 || j.Bool(zero.fields[fTruncated]) {
		t.Fatalf("empty %+v", zero)
	}
}

func TestPageByOffsetStableTotal(t *testing.T) {
	t.Parallel()
	rows := []map[string]any{{fName: "a"}, {fName: "b"}, {fName: "c"}}
	first := pageByOffset(rows, 0, 1, "")
	second := pageByOffset(rows, 1, 1, "")
	if first.Total != 3 || second.Total != 3 {
		t.Fatalf("totals %d %d", first.Total, second.Total)
	}
	if j.Str(first.Rows[0][fName]) != "a" || j.Str(second.Rows[0][fName]) != "b" {
		t.Fatalf("walk %+v %+v", first.Rows, second.Rows)
	}
	if !j.Bool(first.fields[fTruncated]) || j.Str(first.fields["how_to_see_more"]) == "" {
		t.Fatalf("truncation %+v", first.fields)
	}
	past := pageByOffset(rows, 10, 2, "")
	if past.Total != 3 || len(past.Rows) != 0 || j.Bool(past.fields[fTruncated]) {
		t.Fatalf("past end %+v", past)
	}
}

func TestPageCountFromXPages(t *testing.T) {
	t.Parallel()
	n := 7
	if got := (esi.Result{Pages: &n}).PageCount(); got != 7 {
		t.Fatalf("got %d", got)
	}
	if got := (esi.Result{}).PageCount(); got != 1 {
		t.Fatalf("missing header %d", got)
	}
}
