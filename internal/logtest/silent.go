package logtest

type Silent struct{}

func (Silent) Info(string, ...any)  {}
func (Silent) Error(string, ...any) {}
