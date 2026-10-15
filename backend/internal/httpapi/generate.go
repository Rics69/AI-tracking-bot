// Package httpapi is the REST transport: handlers generated from api/openapi.yaml
// plus their implementation on top of the domain services.
package httpapi

//go:generate go tool oapi-codegen -config ../../api/oapi-codegen.yaml ../../api/openapi.yaml
