package http

import "fmt"

func wrap(op string, err error) error {
	if err == nil {
		return nil
	}

	return fmt.Errorf("sso: %s: %w", op, err)
}
