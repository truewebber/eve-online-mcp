package write

import "fmt"

func wrap(op string, err error) error {
	if err == nil {
		return nil
	}

	return fmt.Errorf("write: %s: %w", op, err)
}
