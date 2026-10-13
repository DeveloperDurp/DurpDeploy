// Package api durpdeploy REST API.
//
// JSON request bodies are limited to 4 MiB of encoded bytes and must contain
// exactly one object matching the endpoint schema. Unknown fields, null, and
// trailing data return 400. Oversized bodies return 413 before mutation.
// Control actions accept an empty body or an empty JSON object.
//
//	Schemes: http, https
//	BasePath: /api/v1
//	Version: 1.0.0
//	Host: localhost:8080
//
//	Consumes:
//	- application/json
//
//	Produces:
//	- application/json
//
//	SecurityDefinitions:
//	bearer:
//	  type: apiKey
//	  name: Authorization
//	  in: header
//
//	Security:
//	- bearer
//
// swagger:meta
package api

// swagger:operation GET /healthz health healthzAPI
//
// Public API liveness probe. This does not check database connectivity.
// ---
// security: []
// responses:
//   '200':
//     description: The HTTP API is running.
//     schema:
//       type: object
//       properties:
//         ok:
//           type: boolean
//   '400':
//     description: Unexpected request body.
//   '413':
//     description: Request body exceeds the encoded byte ceiling.

// Generic error response.
// swagger:model ErrorResponse
//
//nolint:unused // Used by Swagger specification generation.
type swaggerErrorResponse struct {
	// in: body
	Error string `json:"error"`
}

// Unauthorized error response.
// swagger:model UnauthorizedError
//
//nolint:unused // Used by Swagger specification generation.
type swaggerUnauthorizedError struct {
	// in: body
	Error string `json:"error"`
}

// Forbidden error response.
// swagger:model ForbiddenError
//
//nolint:unused // Used by Swagger specification generation.
type swaggerForbiddenError struct {
	// in: body
	Error string `json:"error"`
}

// Not found error response.
// swagger:model NotFoundError
//
//nolint:unused // Used by Swagger specification generation.
type swaggerNotFoundError struct {
	// in: body
	Error string `json:"error"`
}

// Bad request error response.
// swagger:model BadRequestError
//
//nolint:unused // Used by Swagger specification generation.
type swaggerBadRequestError struct {
	// in: body
	Error string `json:"error"`
}

// Request body exceeds the 4 MiB encoded byte ceiling.
// swagger:model RequestEntityTooLargeError
//
//nolint:unused // Used by Swagger specification generation.
type swaggerRequestEntityTooLargeError struct {
	// in: body
	Error string `json:"error"`
}

// Validation error response.
// swagger:model ValidationError
//
//nolint:unused // Used by Swagger specification generation.
type swaggerValidationError struct {
	// in: body
	Error string `json:"error"`
}

// Conflict error response.
// swagger:model ConflictError
//
//nolint:unused // Used by Swagger specification generation.
type swaggerConflictError struct {
	// in: body
	Error string `json:"error"`
}

// Server error response.
// swagger:model ServerError
//
//nolint:unused // Used by Swagger specification generation.
type swaggerServerError struct {
	// in: body
	Error string `json:"error"`
}

// Status message response.
// swagger:model StatusResponse
//
//nolint:unused // Used by Swagger specification generation.
type swaggerStatusResponse struct {
	// in: body
	Status string `json:"status"`
}

// Paginated list envelope.
// swagger:model PaginatedResponse
//
//nolint:unused // Used by Swagger specification generation.
type swaggerPaginatedResponse struct {
	// in: body
	Items  []map[string]any `json:"items"`
	Total  int64            `json:"total"`
	Limit  int64            `json:"limit"`
	Offset int64            `json:"offset"`
}
