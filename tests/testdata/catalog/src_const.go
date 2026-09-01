package sample

const fFittings = "fittings"

var ctx, unused any

func run(c client) {
	c.Get(ctx, esiPath("characters", esiID(1), fFittings), unused, unused, unused)
	c.Post(ctx, esiPath("characters", esiID(1), fFittings), unused, unused, unused)
	c.Delete(ctx, esiPath("characters", esiID(1), fFittings, esiID(2)), unused, unused, unused)
}

func esiPath(elem ...string) string { return "" }
func esiID(int) string              { return "" }

type client struct{}

func (client) Get(ctx any, path string, a, b, c any)    {}
func (client) Post(ctx any, path string, a, b, c any)   {}
func (client) Delete(ctx any, path string, a, b, c any) {}
