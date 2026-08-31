package esitest

import (
	"errors"
	"fmt"
	nhttp "net/http"
)

var ErrNoFixture = errors.New("esitest: no fixture")

type Transport struct {
	fixtures map[string]Fixture
}

func Load() (*Transport, error) {
	return LoadDir(Testdata())
}

func LoadDir(dir string) (*Transport, error) {
	fixtures, err := ReadDir(dir)
	if err != nil {
		return nil, err
	}

	return &Transport{fixtures: fixtures}, nil
}

func (t *Transport) RoundTrip(req *nhttp.Request) (*nhttp.Response, error) {
	key := Key(req.Method, req.URL.Path, req.URL.Query())
	f, ok := t.fixtures[key]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNoFixture, key)
	}

	return f.Response(), nil
}

func (t *Transport) Fixture(key string) (Fixture, bool) {
	f, ok := t.fixtures[key]

	return f, ok
}
