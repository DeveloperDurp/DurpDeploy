package api

// swagger:model LifecycleVariable
type swaggerLifecycleVariable struct {
	ID          int64  `json:"id"`
	LifecycleID int64  `json:"lifecycle_id"`
	Name        string `json:"name"`
	// Empty when Secret is 1.
	Value         string `json:"value"`
	EnvironmentID *int64 `json:"environment_id"`
	Secret        int64  `json:"secret"`
	CreatedAt     int64  `json:"created_at"`
}

// swagger:model LifecycleVariableList
type swaggerLifecycleVariableList []swaggerLifecycleVariable

// swagger:model InheritedVariable
type swaggerInheritedVariable struct {
	swaggerLifecycleVariable
	LifecycleName string                   `json:"lifecycle_name"`
	Override      *swaggerVariableResponse `json:"override"`
}

// swagger:model InheritedVariableList
type swaggerInheritedVariableList []swaggerInheritedVariable

// swagger:parameters listLifecycleVariables createLifecycleVariable getLifecycleVariable updateLifecycleVariable deleteLifecycleVariable
type lifecycleVariableLifecycleParam struct {
	// in: path
	// required: true
	ID int64 `json:"id"`
}

// swagger:parameters getLifecycleVariable updateLifecycleVariable deleteLifecycleVariable
type lifecycleVariableIDParam struct {
	// in: path
	// required: true
	ID int64 `json:"varId"`
}

// swagger:parameters listInheritedVariables
type inheritedVariableProjectParam struct {
	// in: path
	// required: true
	ID int64 `json:"id"`
}

// swagger:parameters createLifecycleVariable updateLifecycleVariable
type lifecycleVariableBodyParam struct {
	// in: body
	// required: true
	Body swaggerVariableRequest `json:"body"`
}
