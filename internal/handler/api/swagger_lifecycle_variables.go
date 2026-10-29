package api

// swagger:model LifecycleVariable
//
//nolint:unused // Used by Swagger specification generation.
type swaggerLifecycleVariable struct {
	IsCredential int64  `json:"is_credential"`
	ID           int64  `json:"id"`
	LifecycleID  int64  `json:"lifecycle_id"`
	Name         string `json:"name"`
	// Empty when Secret is 1.
	Value         string `json:"value"`
	EnvironmentID *int64 `json:"environment_id"`
	Secret        int64  `json:"secret"`
	CreatedAt     int64  `json:"created_at"`
}

// swagger:model LifecycleVariableList
//
//nolint:unused // Used by Swagger specification generation.
type swaggerLifecycleVariableList []swaggerLifecycleVariable

// swagger:model InheritedVariable
//
//nolint:unused // Used by Swagger specification generation.
type swaggerInheritedVariable struct {
	swaggerLifecycleVariable
	LifecycleName string                   `json:"lifecycle_name"`
	Override      *swaggerVariableResponse `json:"override"`
}

// swagger:model InheritedVariableList
//
//nolint:unused // Used by Swagger specification generation.
type swaggerInheritedVariableList []swaggerInheritedVariable

// swagger:parameters listLifecycleVariables createLifecycleVariable getLifecycleVariable updateLifecycleVariable deleteLifecycleVariable
//
//nolint:unused // Used by Swagger specification generation.
type lifecycleVariableLifecycleParam struct {
	// in: path
	// required: true
	ID int64 `json:"id"`
}

// swagger:parameters getLifecycleVariable updateLifecycleVariable deleteLifecycleVariable
//
//nolint:unused // Used by Swagger specification generation.
type lifecycleVariableIDParam struct {
	// in: path
	// required: true
	ID int64 `json:"varId"`
}

// swagger:parameters listInheritedVariables
//
//nolint:unused // Used by Swagger specification generation.
type inheritedVariableProjectParam struct {
	// in: path
	// required: true
	ID int64 `json:"id"`
}

// swagger:parameters createLifecycleVariable updateLifecycleVariable
//
//nolint:unused // Used by Swagger specification generation.
type lifecycleVariableBodyParam struct {
	// in: body
	// required: true
	Body swaggerVariableRequest `json:"body"`
}
