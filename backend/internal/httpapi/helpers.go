package httpapi

import (
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"
)

func deref[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}

// optional maps the domain's "empty string means absent" to an omitted JSON field.
func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func dateTime(d *openapi_types.Date) *time.Time {
	if d == nil {
		return nil
	}
	return &d.Time
}
