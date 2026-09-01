package pgtest

import "fmt"

func wrap(op string, err error) error {
	if err == nil {
		return nil
	}

	return fmt.Errorf("pgtest: %s: %w", op, err)
}
