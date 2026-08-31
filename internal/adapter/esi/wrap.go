package esi

import "fmt"

func wrap(op string, err error) error {
	if err == nil {
		return nil
	}

	return fmt.Errorf("esi: %s: %w", op, err)
}
