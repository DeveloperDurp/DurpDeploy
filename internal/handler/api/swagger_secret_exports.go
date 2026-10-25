package api

// swagger:operation POST /admin/secret-exports variables requestSecretExport
//
// Request an export of secret-marked project or shared lifecycle variables.
//
// Require an instance administrator and a global bearer token with admin permission.
// Open approval_url in a browser as the same user. Confirm the selected scope.
// Reauthenticate with the local password or OAuth/OIDC, including required MFA.
// Each export requires new authentication. The request expires after five minutes.
// Keep claim_token private. It is returned once and never belongs in a URL.
// ---
// parameters:
//   - name: body
//     in: body
//     required: true
//     schema:
//       type: object
//       required: [scope, scope_id]
//       properties:
//         scope:
//           type: string
//           enum: [project, lifecycle]
//         scope_id:
//           type: integer
//           format: int64
//           minimum: 1
// responses:
//   '201':
//     description: Pending export. No secret values are returned.
//     schema:
//       type: object
//       properties:
//         id:
//           type: string
//         approval_url:
//           type: string
//           description: Relative browser URL. This identifier does not authorize a download.
//         claim_token:
//           type: string
//           description: Single-use download credential. Store it securely until approval.
//         expires_at:
//           type: integer
//           format: int64
//   '400':
//     description: Invalid request or scope.
//   '401':
//     description: Missing or invalid bearer token.
//   '403':
//     description: Instance administrator and global token required.
//   '404':
//     description: Project or lifecycle not found.
//   '413':
//     description: Request exceeds the JSON body limit.
//   '500':
//     description: Export request failed.

// swagger:operation POST /admin/secret-exports/{exportId}/download variables downloadSecretExport
//
// Download an approved export once as a JSON attachment.
//
// Require the original bearer token and claim_token after browser reauthentication.
// The approving browser session must remain valid. Logout invalidates approval.
// Project administrators without the global admin role cannot export secrets.
// Ordinary variable reads remain masked. Non-secret variables and snapshots are excluded.
// Successful downloads generate an audit event without secret values.
// ---
// parameters:
//   - name: exportId
//     in: path
//     required: true
//     type: string
//   - name: body
//     in: body
//     required: true
//     schema:
//       type: object
//       required: [claim_token]
//       properties:
//         claim_token:
//           type: string
// responses:
//   '200':
//     description: JSON attachment. Cache-Control is no-store.
//     schema:
//       type: object
//       properties:
//         scope:
//           type: string
//         scope_id:
//           type: integer
//           format: int64
//         variables:
//           type: array
//           items:
//             type: object
//             properties:
//               id:
//                 type: integer
//                 format: int64
//               name:
//                 type: string
//               value:
//                 type: string
//                 description: Plaintext secret value.
//               environment_id:
//                 type: integer
//                 format: int64
//                 x-nullable: true
//   '400':
//     description: Invalid JSON body.
//   '401':
//     description: Missing or invalid bearer token.
//   '403':
//     description: Instance administrator and global token required.
//   '404':
//     description: Export is unapproved, expired, canceled, consumed, or bound to different credentials.
//   '413':
//     description: Request exceeds the JSON body limit.
//   '500':
//     description: Export failed. No partial export is returned.
