package oauth

import (
	"errors"
	"fmt"

	"github.com/truewebber/eve-online-mcp/internal/adapter/sso"
)

func wrap(op string, err error) error {
	if err == nil {
		return nil
	}

	return fmt.Errorf("oauth: %s: %w", op, err)
}

func unavailable(err error) error {
	if err == nil {
		return nil
	}

	return fmt.Errorf("%w: %w", ErrUnavailable, err)
}

func classifySSO(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sso.ErrInvalidGrant) {
		return fmt.Errorf("%w: %w", ErrClientMismatch, err)
	}

	return fmt.Errorf("%w: %w", ErrLoginRefused, err)
}
