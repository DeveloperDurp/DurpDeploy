# DurpDeploy Design System

## 1. Atmosphere & Identity

DurpDeploy is a compact operations console: dense navigation, readable tables,
and quiet, semantic status cues. Its signature is DaisyUI's themed neutral
surfaces, which keep deployment controls and administrative actions visually
consistent in both `mocha` and `light` themes.

## 2. Color

### Palette

| Role | Existing token | Usage |
| --- | --- | --- |
| Page surface | `bg-base-100` | Body and form surfaces |
| Raised surface | `bg-base-200` | Navbar, cards, dropdowns, code gutters |
| Contrast surface | `bg-base-300` | One-time secret/code blocks |
| Divider | `border-base-300` | Navbar and bordered form regions |
| Primary action | `btn-primary` | Main create/save actions |
| Secondary action | `btn-secondary` | Secondary creation actions |
| Destructive action | `btn-error`, `alert-error` | Delete/revoke and errors |
| Success state | `badge-success`, `alert-success` | Active/successful state |
| Warning state | `alert-warning` | One-time credentials and cautions |
| Muted text | `opacity-70`, `text-gray-400`, `text-gray-500` | Supporting copy and empty states |

Use DaisyUI semantic classes instead of raw colors; the existing `mocha` and
`light` themes supply the color values.

## 3. Typography

The existing stack is DaisyUI/Tailwind's system sans-serif, with
`font-mono` for commands, token prefixes, and one-time secrets. Page titles
use `text-3xl font-bold`; card titles use `card-title` or `text-xl font-bold`;
body text uses the default size; supporting text uses `text-sm`; metadata uses
`text-xs` or `text-sm`; and table timestamps use `text-sm whitespace-nowrap`.

## 4. Spacing & Layout

Tailwind's default 4px scale is the spacing system: `gap-2`/`p-2` for compact
menu groups, `gap-4`/`p-4` for controls, `mb-4` for title separation, and
`mb-6`/`space-y-6` for page sections. The main shell is
`w-full px-4 sm:px-6 lg:px-8`; navigation changes at `xl` so long account names
do not collide with primary links; responsive tables
keep `overflow-x-auto` and `table table-zebra table-fixed w-full`.

## 5. Components

### Navbar dropdown
- **Structure**: native `details.dropdown.dropdown-end` with a focusable
  `summary.btn.btn-ghost.btn-sm`, then `ul.dropdown-content.menu`.
- **Spacing**: `w-52 p-2` for the mobile menu and compact `btn-sm` controls.
- **States**: active links use DaisyUI's `active`; summaries retain the native
  keyboard interaction and DaisyUI focus treatment.
- **Accessibility**: links remain anchors, actions remain buttons in POST
  forms, and Escape/outside-click behavior is supplied by existing Alpine
  attributes.

### Cards and forms
- **Structure**: `card bg-base-200 shadow` with `card-body`; fields use
  `form-control`, `label`, and `input input-bordered`.
- **States**: validation uses `text-error text-sm`; alerts use semantic
  `alert-*` classes.
- Initially hidden Alpine edit forms use `x-cloak` to prevent a flash before
  initialization. Runbook editor methods live in the shared JavaScript bundle;
  templates carry initial data and short method calls.
- Native dialogs connect their visible heading with `aria-labelledby`.
  Script editor headings use Alpine-generated IDs to keep repeated editors
  distinct.
- Headers containing project names wrap their heading above Back on narrow
  screens, so long names cannot push the control outside the viewport.

### Package repository configuration
- One active project source is shown as a card, without a repository selector.
- Authentication uses Alpine to show and enable only relevant credential fields;
  `x-cloak` prevents credentials flashing before initialization.
- Version tests use an HTMX result region with `aria-live="polite"`; the submit
  button is disabled during the request and semantic alerts show success/error.
- Configuration replacement preserves historical package pins, and the page
  states that saving affects only future snapshots.

### Back navigation
- Generic Back/Go back links use native browser history, preserving previous
  URLs, filters, and pagination. They do not add entries or maintain a URL stack.
- Header links use `btn btn-ghost btn-sm`, grouped beside the other actions with
  `flex flex-wrap gap-2`. Back remains available to viewers and keyboard users.
- The existing parent URL is the fallback for an empty history. Package
  repository Back falls back to its project; the standalone viewer rejection
  page falls back to Projects. JavaScript replaces the direct entry to avoid a
  Back loop; links also work without JavaScript.
- The Navigation API distinguishes a first entry with forward history from a
  real previous page when all entries are visible. Cross-origin entries and old
  browsers hide that position; in those cases Back favors browser history over
  a guessed fallback. Modified clicks retain normal anchor behavior.
- Breadcrumbs, named destination links, and Cancel controls retain their
  explicit destinations.

### Deployment verification and rollback
- Verification uses the existing form fields, a native type selector, and
  supporting text explaining timeout and execution placement. Every new field
  has an explicit label; invalid input uses the existing form error region.
- Rollback uses a secondary action and a confirmation form with source and
  target versions, environment, and approval/gate status. The selected target
  is submitted explicitly so stale confirmations fail safely.
- Viewers see verification results but no rollback write control. All new
  surfaces wrap at small widths and keep the existing semantic focus states.

### Agent maintenance and health
- Show administrative state and heartbeat health as separate labelled badges:
  active/healthy use `badge-success`, draining/drained/stale use `badge-warning`,
  offline/revoked use `badge-error`, and disabled/unknown use `badge-ghost`.
- Drain and Resume are native POST form buttons, sized `btn-xs` in lists and
  `btn-sm` on detail pages. Revocation keeps its destructive styling.
- Workload details use existing cards and definition lists, linked deployment
  IDs, readable empty states, and `break-all` for version identifiers.
- Status text carries meaning independently of color. Controls retain native
  keyboard access and focus treatment; long values wrap on narrow screens.

### Deployment waiting state
- Queued remote work with no issued claim replaces the deployment status text
  with “Waiting for agents” in the existing `badge-warning`, with
  `aria-live="polite"`.
- Reuse the deployment's existing status polling to clear the message once
  work is claimed or the deployment finishes.

## 6. Motion & Interaction

Normal internal page links use HTMX to replace the main content, update the
navbar and title, and push the existing URL into browser history. The document
and its styles stay loaded; no page fade or layout animation is added. Focus
moves to the main landmark after navigation. Native forms, downloads, login,
logout, external links, and modified clicks keep their existing behavior.
History restoration fetches fresh content without saving protected markup in
browser storage. Existing Alpine components release their streams on removal.

Deployment logs use one native `details` disclosure per release step, in release
order, with a semantic state badge. Running, waiting, and failed steps open
automatically; users can collapse them with the keyboard. A polite status line
names the active step even while its script is quiet. Unattributed historical
logs appear as deployment messages, with unavailable states shown explicitly.

Existing interaction is intentionally minimal: native `details` disclosure,
Alpine theme/dropdown state, HTMX swaps, and `x-transition.opacity.duration.300ms`
for toasts. New controls reuse native disclosure and existing Alpine behavior;
no new motion or JavaScript package is introduced.

## 7. Depth & Surface

The existing strategy is mixed semantic elevation: `bg-base-200` distinguishes
navbar/cards/dropdowns, `border-base-300` separates the navbar and form areas,
and DaisyUI `shadow` elevates cards, stats, and menus. New surfaces reuse these
classes rather than custom shadow, border, or color values.
