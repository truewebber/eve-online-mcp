package sample

var ctx, unused any

func apply(c client) {
	path := esiPath("characters", esiID(1), "contacts")
	var call func(any, string, any, any, any)
	call = c.Put
	call = c.Post
	call(ctx, path, unused, unused, unused)
}

func esiPath(elem ...string) string { return "" }
func esiID(int) string              { return "" }

type client struct{}

func (client) Post(ctx any, path string, a, b, c any) {}
func (client) Put(ctx any, path string, a, b, c any)  {}
