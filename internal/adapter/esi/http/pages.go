package http

import (
	"context"

	"github.com/truewebber/eve-online-mcp/internal/adapter/esi"
	"github.com/truewebber/eve-online-mcp/internal/j"
)

func (c *Client) GetAllPages(ctx context.Context, path string, characterID *int, params map[string]any, maxPages int) (esi.Result, error) {
	if params == nil {
		params = map[string]any{}
	}
	first, err := c.cachedGet(ctx, path, characterID, withPage(params, 1), nil)
	if err != nil {
		return esi.Result{}, err
	}
	total := 1
	if first.Pages != nil {
		total = *first.Pages
	}
	if total <= 1 || !isSlice(first.Data) {
		return first, nil
	}
	capped := min(total, maxPages)
	if capped < total {
		c.logger.Info("esi: capping pages", "path", path, "total", total, "capped", capped)
	}
	type box struct {
		r   esi.Result
		err error
	}
	ch := make(chan box, capped-1)
	for page := 2; page <= capped; page++ {
		p := withPage(params, page)
		go func() {
			r, err := c.cachedGet(ctx, path, characterID, p, nil)
			ch <- box{r, err}
		}()
	}
	data := append([]any{}, j.Slice(first.Data)...)
	oldest := first.AgeSeconds
	allCached := first.FromCache
	for range capped - 1 {
		b := <-ch
		if b.err != nil {
			return esi.Result{}, b.err
		}
		if s := j.Slice(b.r.Data); s != nil {
			data = append(data, s...)
		}
		if b.r.AgeSeconds > oldest {
			oldest = b.r.AgeSeconds
		}
		allCached = allCached && b.r.FromCache
	}

	return esi.Result{
		Data: data, FromCache: allCached, AgeSeconds: oldest,
		ExpiresAt: first.ExpiresAt, Pages: &total, Truncated: capped < total,
	}, nil
}

type cursorWalk struct {
	path, cursorParam, cursorKey string
	characterID                  *int
	batchSize, maxPages          int
	cursor                       any
	base                         map[string]any
	data                         []any
	seen                         map[any]struct{}
	oldest, expiresAt            float64
	allCached                    bool
	fetched                      int
	truncated                    bool
}

func (c *Client) GetCursorPages(ctx context.Context, path string, q esi.CursorQuery) (esi.Result, error) {
	params := q.Params
	if params == nil {
		params = map[string]any{}
	}
	maxPages := max(q.MaxPages, 1)
	batchSize := max(q.BatchSize, 1)
	walk := cursorWalk{
		path: path, characterID: q.CharacterID,
		cursorParam: q.CursorParam, cursorKey: q.CursorKey,
		batchSize: batchSize, maxPages: maxPages,
		cursor: params[q.CursorParam], base: clone(params),
		seen: map[any]struct{}{}, allCached: true,
	}
	for index := range maxPages {
		cont, err := c.stepCursor(ctx, &walk, index)
		if err != nil {
			return esi.Result{}, err
		}
		if !cont {
			break
		}
	}
	pages := walk.fetched

	return esi.Result{
		Data: walk.data, FromCache: walk.allCached, AgeSeconds: walk.oldest,
		ExpiresAt: walk.expiresAt, Pages: &pages, Truncated: walk.truncated,
	}, nil
}

func (c *Client) stepCursor(ctx context.Context, w *cursorWalk, index int) (bool, error) {
	q := clone(w.base)
	q[w.cursorParam] = w.cursor
	result, err := c.cachedGet(ctx, w.path, w.characterID, q, nil)
	if err != nil {
		return false, err
	}
	w.fetched++
	if w.fetched == 1 {
		w.expiresAt = result.ExpiresAt
	}
	w.allCached = w.allCached && result.FromCache
	if result.AgeSeconds > w.oldest {
		w.oldest = result.AgeSeconds
	}
	rows := j.Maps(result.Data)
	if len(rows) == 0 {
		return false, nil
	}
	if len(rows) > w.batchSize {
		w.batchSize = len(rows)
	}
	nextCursor := mergeCursorRows(w, rows)
	if len(rows) < w.batchSize {
		return false, nil
	}
	if index == w.maxPages-1 {
		w.truncated = true

		return false, nil
	}
	if nextCursor == nil || (w.cursor != nil && !lessAny(nextCursor, w.cursor)) {
		c.logger.Info("esi: cursor did not advance", "path", w.path, "cursor", w.cursorParam, "at", w.cursor)

		return false, nil
	}
	w.cursor = nextCursor

	return true, nil
}

func mergeCursorRows(w *cursorWalk, rows []map[string]any) any {
	var nextCursor any
	for _, row := range rows {
		marker := row[w.cursorKey]
		if marker != nil {
			if _, ok := w.seen[marker]; ok {
				continue
			}
			w.seen[marker] = struct{}{}
			if nextCursor == nil || lessAny(marker, nextCursor) {
				nextCursor = marker
			}
		}
		w.data = append(w.data, row)
	}

	return nextCursor
}
