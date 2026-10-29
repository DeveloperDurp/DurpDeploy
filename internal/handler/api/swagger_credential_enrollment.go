package api

// swagger:operation POST /projects/{id}/credentials credentials createProjectCredential
//
// Create an explicitly classified secret variable. Existing secrets require individual enrollment.
// Writes require effective Maintainer access.
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
// - name: body
//   in: body
//   required: true
//   schema:
//     $ref: '#/definitions/CredentialCreateRequest'
// responses:
//   '201':
//     description: Credential operation succeeded
//     schema:
//       $ref: '#/definitions/Credential'
//   '400':
//     description: Invalid request
//   '401':
//     description: Authentication required
//   '403':
//     description: Permission denied
//   '404':
//     description: Owner or credential not found
//   '409':
//     description: Stale revision
//   '422':
//     description: Invalid credential fields
//   '500':
//     description: Credential operation failed

// swagger:operation GET /projects/{id}/credentials/candidates credentials listProjectCredentialCandidates
//
// List ordinary secrets available for individual enrollment. Values are excluded.
// Writes require effective Maintainer access.
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
//     description: Credential operation succeeded
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
//     description: Owner or credential not found
//   '409':
//     description: Stale revision
//   '422':
//     description: Invalid credential fields
//   '500':
//     description: Credential operation failed

// swagger:operation POST /projects/{id}/credentials/enroll credentials enrollProjectCredential
//
// Enroll one existing secret. Preserve its value and dates. Require the current candidate revision.
// Writes require effective Maintainer access.
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
// - name: body
//   in: body
//   required: true
//   schema:
//     $ref: '#/definitions/CredentialEnrollmentRequest'
// responses:
//   '200':
//     description: Credential operation succeeded
//     schema:
//       $ref: '#/definitions/Credential'
//   '400':
//     description: Invalid request
//   '401':
//     description: Authentication required
//   '403':
//     description: Permission denied
//   '404':
//     description: Owner or credential not found
//   '409':
//     description: Stale revision
//   '422':
//     description: Invalid credential fields
//   '500':
//     description: Credential operation failed

// swagger:operation DELETE /projects/{id}/credentials/{kind}/{credentialId} credentials deleteProjectCredential
//
// Delete a credential variable source. Preserve release snapshots and execution history.
// Writes require effective Maintainer access.
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
//   enum: [variable]
// - name: credentialId
//   in: path
//   required: true
//   type: integer
//   format: int64
// - name: body
//   in: body
//   required: true
//   schema:
//     type: object
//     required: [revision]
//     properties:
//       revision:
//         type: integer
//         format: int64
// responses:
//   '204':
//     description: Credential operation succeeded
//   '400':
//     description: Invalid request
//   '401':
//     description: Authentication required
//   '403':
//     description: Permission denied
//   '404':
//     description: Owner or credential not found
//   '409':
//     description: Stale revision
//   '422':
//     description: Invalid credential fields
//   '500':
//     description: Credential operation failed

// swagger:operation GET /projects/{id}/credentials/status credentials getProjectCredentialStatus
//
// Read expiry banner counts. Include applicable shared credentials and authenticated package sources.
// Writes require effective Maintainer access.
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
//     description: Credential operation succeeded
//     schema:
//       $ref: '#/definitions/CredentialStatusSummary'
//   '400':
//     description: Invalid request
//   '401':
//     description: Authentication required
//   '403':
//     description: Permission denied
//   '404':
//     description: Owner or credential not found
//   '409':
//     description: Stale revision
//   '422':
//     description: Invalid credential fields
//   '500':
//     description: Credential operation failed

// swagger:operation POST /lifecycles/{id}/credentials credentials createLifecycleCredential
//
// Create an explicitly classified secret variable. Existing secrets require individual enrollment.
// Require an unrestricted global administrator.
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
// - name: body
//   in: body
//   required: true
//   schema:
//     $ref: '#/definitions/CredentialCreateRequest'
// responses:
//   '201':
//     description: Credential operation succeeded
//     schema:
//       $ref: '#/definitions/Credential'
//   '400':
//     description: Invalid request
//   '401':
//     description: Authentication required
//   '403':
//     description: Permission denied
//   '404':
//     description: Owner or credential not found
//   '409':
//     description: Stale revision
//   '422':
//     description: Invalid credential fields
//   '500':
//     description: Credential operation failed

// swagger:operation GET /lifecycles/{id}/credentials/candidates credentials listLifecycleCredentialCandidates
//
// List ordinary secrets available for individual enrollment. Values are excluded.
// Require an unrestricted global administrator.
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
//     description: Credential operation succeeded
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
//     description: Owner or credential not found
//   '409':
//     description: Stale revision
//   '422':
//     description: Invalid credential fields
//   '500':
//     description: Credential operation failed

// swagger:operation POST /lifecycles/{id}/credentials/enroll credentials enrollLifecycleCredential
//
// Enroll one existing secret. Preserve its value and dates. Require the current candidate revision.
// Require an unrestricted global administrator.
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
// - name: body
//   in: body
//   required: true
//   schema:
//     $ref: '#/definitions/CredentialEnrollmentRequest'
// responses:
//   '200':
//     description: Credential operation succeeded
//     schema:
//       $ref: '#/definitions/Credential'
//   '400':
//     description: Invalid request
//   '401':
//     description: Authentication required
//   '403':
//     description: Permission denied
//   '404':
//     description: Owner or credential not found
//   '409':
//     description: Stale revision
//   '422':
//     description: Invalid credential fields
//   '500':
//     description: Credential operation failed

// swagger:operation DELETE /lifecycles/{id}/credentials/{kind}/{credentialId} credentials deleteLifecycleCredential
//
// Delete a credential variable source. Preserve release snapshots and execution history.
// Require an unrestricted global administrator.
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
//     type: object
//     required: [revision]
//     properties:
//       revision:
//         type: integer
//         format: int64
// responses:
//   '204':
//     description: Credential operation succeeded
//   '400':
//     description: Invalid request
//   '401':
//     description: Authentication required
//   '403':
//     description: Permission denied
//   '404':
//     description: Owner or credential not found
//   '409':
//     description: Stale revision
//   '422':
//     description: Invalid credential fields
//   '500':
//     description: Credential operation failed
