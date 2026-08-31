package esitest

import (
	"context"
	"fmt"
)

func Update(ctx context.Context) error {
	dir := Testdata()
	token := AccessToken()
	for _, spec := range Catalog() {
		f, err := resolveFixture(ctx, spec, token)
		if err != nil {
			return fmt.Errorf("%s %s: %w", spec.Method, spec.Path, err)
		}
		if err := Write(dir, f); err != nil {
			return err
		}
	}

	return nil
}

func resolveFixture(ctx context.Context, spec Spec, token string) (Fixture, error) {
	if spec.Status == statusErrorLimited || (spec.Auth && token == "") {
		return SchemaFixture(spec)
	}
	if spec.Status == statusForbidden {
		got, err := Record(ctx, spec, "")
		if err != nil || got.Status != statusForbidden {
			return SchemaFixture(spec)
		}

		return got, nil
	}
	if spec.Auth {
		return Record(ctx, spec, token)
	}

	return Record(ctx, spec, "")
}
