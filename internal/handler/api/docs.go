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
type swaggerErrorResponse struct {
	// in: body
	Error string `json:"error"`
}

// Unauthorized error response.
// swagger:model UnauthorizedError
type swaggerUnauthorizedError struct {
	// in: body
	Error string `json:"error"`
}

// Forbidden error response.
// swagger:model ForbiddenError
type swaggerForbiddenError struct {
	// in: body
	Error string `json:"error"`
}

// Not found error response.
// swagger:model NotFoundError
type swaggerNotFoundError struct {
	// in: body
	Error string `json:"error"`
}

// Bad request error response.
// swagger:model BadRequestError
type swaggerBadRequestError struct {
	// in: body
	Error string `json:"error"`
}

// Request body exceeds the 4 MiB encoded byte ceiling.
// swagger:model RequestEntityTooLargeError
type swaggerRequestEntityTooLargeError struct {
	// in: body
	Error string `json:"error"`
}

// Validation error response.
// swagger:model ValidationError
type swaggerValidationError struct {
	// in: body
	Error string `json:"error"`
}

// Conflict error response.
// swagger:model ConflictError
type swaggerConflictError struct {
	// in: body
	Error string `json:"error"`
}

// Server error response.
// swagger:model ServerError
type swaggerServerError struct {
	// in: body
	Error string `json:"error"`
}

// Status message response.
// swagger:model StatusResponse
type swaggerStatusResponse struct {
	// in: body
	Status string `json:"status"`
}

// Paginated list envelope.
// swagger:model PaginatedResponse
type swaggerPaginatedResponse struct {
	// in: body
	Items  []map[string]any `json:"items"`
	Total  int64            `json:"total"`
	Limit  int64            `json:"limit"`
	Offset int64            `json:"offset"`
}
