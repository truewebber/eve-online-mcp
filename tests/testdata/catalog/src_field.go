package sample

var ctx, unused any

type view struct {
	path string
}

func helper(c client, in view) {
	c.Get(ctx, in.path, unused, unused, unused)
}

func cursor(c client, in view) {
	c.GetCursorPages(ctx, in.path, unused, unused, unused)
}

func run(c client) {
	helper(c, view{path: esiPath("characters", esiID(1), "killmails", "recent")})
	cursor(c, view{path: esiPath("characters", esiID(1), "wallet", "transactions")})
}

func apply(c client) {
	path := esiPath("characters", esiID(1), "contacts")
	do(c, view{path: path})
}

func do(c client, in view) {
	var call func(any, string, any, any, any)
	call = c.Put
	call = c.Post
	call(ctx, in.path, unused, unused, unused)
}

func esiPath(elem ...string) string { return "" }
func esiID(int) string              { return "" }

type client struct{}

func (client) Get(ctx any, path string, a, b, c any)            {}
func (client) GetCursorPages(ctx any, path string, a, b, c any) {}
func (client) Post(ctx any, path string, a, b, c any)           {}
func (client) Put(ctx any, path string, a, b, c any)            {}
