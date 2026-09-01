package http

import (
	"container/list"
	"sync"
)

const maxNameEntries = 50_000

type nameItem struct {
	id   int
	name string
}

type nameCache struct {
	mu    sync.Mutex
	ll    *list.List
	items map[int]*list.Element
}

func newNameCache() *nameCache {
	return &nameCache{ll: list.New(), items: map[int]*list.Element{}}
}

type nameHit struct {
	names   map[int]string
	missing []int
}

func (c *nameCache) get(ids []int) nameHit {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := nameHit{names: map[int]string{}}
	for _, id := range ids {
		el, ok := c.items[id]
		if !ok {
			out.missing = append(out.missing, id)

			continue
		}
		item, ok := el.Value.(*nameItem)
		if !ok {
			out.missing = append(out.missing, id)

			continue
		}
		c.ll.MoveToFront(el)
		out.names[id] = item.name
	}

	return out
}

func (c *nameCache) put(id int, name string) {
	if id == 0 || name == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[id]; ok {
		item, ok := el.Value.(*nameItem)
		if !ok {
			return
		}
		item.name = name
		c.ll.MoveToFront(el)

		return
	}
	c.items[id] = c.ll.PushFront(&nameItem{id: id, name: name})
	for c.ll.Len() > maxNameEntries {
		el := c.ll.Back()
		if el == nil {
			break
		}
		item, ok := el.Value.(*nameItem)
		if !ok {
			c.ll.Remove(el)

			continue
		}
		c.ll.Remove(el)
		delete(c.items, item.id)
	}
}
