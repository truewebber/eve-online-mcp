package sweep

import "fmt"

func wrap(op string, err error) error {
	if err == nil {
		return nil
	}

	return fmt.Errorf("sweep: %s: %w", op, err)
}
