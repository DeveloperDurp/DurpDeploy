package api

// swagger:operation POST /users/me/password users changeOwnPassword
//
// Change the authenticated local user's password.
//
// Require an unrestricted global bearer token and the current password.
// All roles can change their own password. Project-scoped and explicit-role
// tokens cannot use this operation. Browser sessions and pending MFA challenges
// are invalidated atomically. API tokens and enrolled factors remain valid.
// SSO-only accounts must change their password with their identity provider.
// New passwords must leave room for all three password fields, email, and CSRF
// within the 64 KiB encoded browser form limit. Line breaks are not allowed.
// ---
// parameters:
//   - name: body
//     in: body
//     required: true
//     schema:
//       type: object
//       additionalProperties: false
//       required: [current_password, new_password, password_confirmation]
//       properties:
//         current_password:
//           type: string
//           format: password
//         new_password:
//           type: string
//           format: password
//           minLength: 8
//         password_confirmation:
//           type: string
//           format: password
// responses:
//   '204':
//     description: Password changed. Sign in again in all browsers.
//   '400':
//     description: Invalid JSON body.
//   '401':
//     description: Missing, invalid, expired, or revoked bearer token.
//   '403':
//     description: Token has a project scope or permission ceiling.
//   '413':
//     description: Request body exceeds the encoded byte ceiling.
//   '422':
//     description: Invalid current password, missing local password, or invalid new password.
//   '500':
//     description: Password update failed. The transaction preserves the original state.
