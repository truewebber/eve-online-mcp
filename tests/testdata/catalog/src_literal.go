package sample

var ctx, unused any

func run(c client) {
	c.Get(ctx, "/status", unused, unused, unused)
	c.Get(ctx, esi.Path("/universe/system_kills"), unused, unused, unused)
	c.Post(ctx, esiPath("characters", esiID(1), "cspa"), unused, unused, unused)
	c.Put(ctx, esi.Path("characters", esi.ID(1), "mail", esi.ID(2)), unused, unused, unused)
}

func esiPath(elem ...string) string { return "" }
func esiID(int) string              { return "" }

type esi struct{}

func (esi) Path(elem ...string) string { return "" }
func (esi) ID(int) string              { return "" }

type client struct{}

func (client) Get(ctx any, path string, a, b, c any)  {}
func (client) Post(ctx any, path string, a, b, c any) {}
func (client) Put(ctx any, path string, a, b, c any)  {}
