package sample

var ctx, unused any

func helper(c client, path string) {
	c.Get(ctx, path, unused, unused, unused)
}

func run(c client) {
	helper(c, esiPath("characters", esiID(1), "killmails", "recent"))
}

func esiPath(elem ...string) string { return "" }
func esiID(int) string              { return "" }

type client struct{}

func (client) Get(ctx any, path string, a, b, c any) {}
