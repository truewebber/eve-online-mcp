package esitest

import (
	"path/filepath"
	"runtime"
)

const (
	testdataDirPerm  = 0o750
	testdataFilePerm = 0o600
)

func Testdata() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return filepath.Join("..", "testdata")
	}

	return filepath.Join(filepath.Dir(file), "..", "testdata")
}
