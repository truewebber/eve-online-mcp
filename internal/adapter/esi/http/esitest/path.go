package esitest

import (
	"path"
	"strconv"
)

func Path(elem ...string) string {
	return path.Join("/", path.Join(elem...))
}

func id(n int) string {
	return strconv.Itoa(n)
}
