package api

// swagger:operation POST /lint lint lintScriptWarnings
//
// Analyze caller text with built-in regex warnings without execution.
// Every valid effective role and global or scoped PAT can use this operation.
// This grants no stored project or script access. Browser lint stays separate.
// Empty diagnostics do not establish valid syntax or safe execution.
// Reject duplicate fields, invalid UTF-8, lone surrogates, NUL, and query selectors.
// Encoded bodies are limited to 524288 bytes. Scripts are limited to 65536 UTF-8 bytes.
// A smaller configured JSON ceiling applies. Admission has four router-owned slots and no queue.
// Listener deadlines cover I/O. Analysis has a separate two-second deadline after decoding.
// Use Cache-Control: no-store. Retain no source or results in application logs or audit.
// Normal token last-use telemetry remains enabled. External proxy logging is outside this contract.
// ---
// consumes:
// - application/json
// produces:
// - application/json
// security:
// - bearer: []
// parameters:
// - name: body
//   in: body
//   required: true
//   schema:
//     type: object
//     additionalProperties: false
//     x-max-json-bytes: 524288
//     required: [profile, script]
//     properties:
//       profile:
//         type: string
//         enum: [builtin-shell-warnings-v1]
//       script:
//         type: string
//         maxLength: 65536
//         x-max-utf8-bytes: 65536
//         description: The UTF-8 byte ceiling applies in addition to the character ceiling.
// responses:
//   '200':
//     description: Complete analysis. Exactly 100 findings are not truncated. More findings return the first 100 with truncated true.
//     headers:
//       Cache-Control:
//         type: string
//         description: no-store
//     schema:
//       type: object
//       additionalProperties: false
//       x-max-json-bytes: 32768
//       required: [profile, diagnostics, truncated]
//       properties:
//         profile:
//           type: string
//           enum: [builtin-shell-warnings-v1]
//         truncated:
//           type: boolean
//         diagnostics:
//           type: array
//           maxItems: 100
//           items:
//             type: object
//             additionalProperties: false
//             required: [line, level, code, message]
//             properties:
//               line:
//                 type: integer
//                 minimum: 1
//               level:
//                 type: string
//                 enum: [warning]
//               code:
//                 type: integer
//                 enum: [1000, 2086]
//               message:
//                 type: string
//                 maxLength: 128
//                 enum: [Script is missing a shebang line, Variable reference matched the built-in warning rule]
//   '400':
//     description: Invalid request, profile, text, JSON, or query selectors. Error details contain no source.
//   '401':
//     description: Missing, invalid, expired, or revoked bearer credential. Cookie authentication is not accepted.
//   '403':
//     description: Effective role is not supported. No analysis occurs.
//   '408':
//     description: Request cancellation or read deadline. Disconnected callers cannot receive a promised response.
//   '413':
//     description: Encoded, decoded, or smaller configured JSON ceiling exceeded.
//   '415':
//     description: Expected application/json with absent or UTF-8 charset.
//   '500':
//     description: Internal response failure. Normal generic error scrubbing applies.
//   '503':
//     description: All four admission slots are occupied. No buffering or analysis occurs. Normal generic error scrubbing applies.
//   '504':
//     description: Analysis deadline exceeded. Discard partial diagnostics. Normal generic error scrubbing applies.
