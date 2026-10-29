package api

// swagger:operation GET /projects/{id}/credentials credentials listProjectCredentials
//
// List credential metadata without secret values. Status uses the earliest configured date and a 30-day warning window.
// Project writes require effective Maintainer permission. Retained package sources can rotate without becoming active.
// Saving confirms storage only. Provider rotation and provider validation are manual.
// ---
// produces:
// - application/json
// security:
// - bearer: []
// parameters:
// - name: id
//   in: path
//   required: true
//   type: integer
//   format: int64
// responses:
//   '200':
//     description: Credential metadata
//     schema:
//       type: array
//       items:
//         $ref: '#/definitions/Credential'
//   '400':
//     description: Invalid request
//   '401':
//     description: Authentication required
//   '403':
//     description: Permission denied
//   '404':
//     description: Credential or owner not found
//   '500':
//     description: Credential operation failed

// swagger:operation PUT /projects/{id}/credentials/{kind}/{credentialId} credentials updateProjectCredentialMetadata
//
// Save expiry and rotation dates with an optional replacement. Empty value preserves the secret and last rotation time. Zero clears dates.
// A nonempty value rotates the credential and updates matching release and runbook secrets. Existing execution captures remain unchanged.
// Project writes require effective Maintainer permission. Retained package sources can rotate without becoming active.
// Saving confirms storage only. Provider rotation and provider validation are manual.
// ---
// produces:
// - application/json
// security:
// - bearer: []
// parameters:
// - name: id
//   in: path
//   required: true
//   type: integer
//   format: int64
// - name: kind
//   in: path
//   required: true
//   type: string
//   enum: [variable, package]
// - name: credentialId
//   in: path
//   required: true
//   type: integer
//   format: int64
// - name: body
//   in: body
//   required: true
//   schema:
//     $ref: '#/definitions/CredentialRotationRequest'
// responses:
//   '200':
//     description: Credential metadata
//     schema:
//       $ref: '#/definitions/CredentialRotationResult'
//   '400':
//     description: Invalid request
//   '401':
//     description: Authentication required
//   '403':
//     description: Permission denied
//   '404':
//     description: Credential or owner not found
//   '409':
//     description: Stale revision. Reload before retrying.
//   '422':
//     description: Invalid dates or replacement
//   '500':
//     description: Credential operation failed

// swagger:operation POST /projects/{id}/credentials/{kind}/{credentialId}/rotate credentials rotateProjectCredential
//
// Store a nonempty replacement and dates atomically. Project secrets update all matching release and runbook secrets. Existing execution captures remain unchanged.
// Project writes require effective Maintainer permission. Retained package sources can rotate without becoming active.
// Saving confirms storage only. Provider rotation and provider validation are manual.
// ---
// produces:
// - application/json
// security:
// - bearer: []
// parameters:
// - name: id
//   in: path
//   required: true
//   type: integer
//   format: int64
// - name: kind
//   in: path
//   required: true
//   type: string
//   enum: [variable, package]
// - name: credentialId
//   in: path
//   required: true
//   type: integer
//   format: int64
// - name: body
//   in: body
//   required: true
//   schema:
//     $ref: '#/definitions/CredentialRotationRequest'
// responses:
//   '200':
//     description: Credential metadata
//     schema:
//       $ref: '#/definitions/CredentialRotationResult'
//   '400':
//     description: Invalid request
//   '401':
//     description: Authentication required
//   '403':
//     description: Permission denied
//   '404':
//     description: Credential or owner not found
//   '409':
//     description: Stale revision. Reload before retrying.
//   '422':
//     description: Invalid dates or replacement
//   '500':
//     description: Credential operation failed

// swagger:operation GET /lifecycles/{id}/credentials credentials listLifecycleCredentials
//
// List credential metadata without secret values. Status uses the earliest configured date and a 30-day warning window.
// Requires an unrestricted global administrator. Project overrides keep their values.
// Saving confirms storage only. Provider rotation and provider validation are manual.
// ---
// produces:
// - application/json
// security:
// - bearer: []
// parameters:
// - name: id
//   in: path
//   required: true
//   type: integer
//   format: int64
// responses:
//   '200':
//     description: Credential metadata
//     schema:
//       type: array
//       items:
//         $ref: '#/definitions/Credential'
//   '400':
//     description: Invalid request
//   '401':
//     description: Authentication required
//   '403':
//     description: Permission denied
//   '404':
//     description: Credential or owner not found
//   '500':
//     description: Credential operation failed

// swagger:operation PUT /lifecycles/{id}/credentials/{kind}/{credentialId} credentials updateLifecycleCredentialMetadata
//
// Save expiry and rotation dates with an optional replacement. Empty value preserves the secret and last rotation time. Zero clears dates.
// A nonempty value replaces the shared credential for new executions. Existing execution captures remain unchanged.
// Requires an unrestricted global administrator. Project overrides keep their values.
// Saving confirms storage only. Provider rotation and provider validation are manual.
// ---
// produces:
// - application/json
// security:
// - bearer: []
// parameters:
// - name: id
//   in: path
//   required: true
//   type: integer
//   format: int64
// - name: kind
//   in: path
//   required: true
//   type: string
//   enum: [lifecycle]
// - name: credentialId
//   in: path
//   required: true
//   type: integer
//   format: int64
// - name: body
//   in: body
//   required: true
//   schema:
//     $ref: '#/definitions/CredentialRotationRequest'
// responses:
//   '200':
//     description: Credential metadata
//     schema:
//       $ref: '#/definitions/CredentialRotationResult'
//   '400':
//     description: Invalid request
//   '401':
//     description: Authentication required
//   '403':
//     description: Permission denied
//   '404':
//     description: Credential or owner not found
//   '409':
//     description: Stale revision. Reload before retrying.
//   '422':
//     description: Invalid dates or replacement
//   '500':
//     description: Credential operation failed

// swagger:operation POST /lifecycles/{id}/credentials/{kind}/{credentialId}/rotate credentials rotateLifecycleCredential
//
// Store a nonempty replacement and dates atomically. Project secrets update all matching release and runbook secrets. Existing execution captures remain unchanged.
// Requires an unrestricted global administrator. Project overrides keep their values.
// Saving confirms storage only. Provider rotation and provider validation are manual.
// ---
// produces:
// - application/json
// security:
// - bearer: []
// parameters:
// - name: id
//   in: path
//   required: true
//   type: integer
//   format: int64
// - name: kind
//   in: path
//   required: true
//   type: string
//   enum: [lifecycle]
// - name: credentialId
//   in: path
//   required: true
//   type: integer
//   format: int64
// - name: body
//   in: body
//   required: true
//   schema:
//     $ref: '#/definitions/CredentialRotationRequest'
// responses:
//   '200':
//     description: Credential metadata
//     schema:
//       $ref: '#/definitions/CredentialRotationResult'
//   '400':
//     description: Invalid request
//   '401':
//     description: Authentication required
//   '403':
//     description: Permission denied
//   '404':
//     description: Credential or owner not found
//   '409':
//     description: Stale revision. Reload before retrying.
//   '422':
//     description: Invalid dates or replacement
//   '500':
//     description: Credential operation failed
