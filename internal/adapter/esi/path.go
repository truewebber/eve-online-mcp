package esi

import (
	"path"
	"strconv"
)

func Path(elem ...string) string {
	return path.Join("/", path.Join(elem...))
}

func ID(id int) string {
	return strconv.Itoa(id)
}
