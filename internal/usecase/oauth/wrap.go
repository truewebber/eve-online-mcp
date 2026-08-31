package oauth

import "fmt"

func wrap(op string, err error) error {
	if err == nil {
		return nil
	}

	return fmt.Errorf("oauth: %s: %w", op, err)
}
