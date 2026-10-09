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
- **States**: active links use DaisyUI's `menu-active`; summaries retain the native
  keyboard interaction and DaisyUI focus treatment.
- **Accessibility**: links remain anchors, actions remain buttons in POST
  forms, and Escape/outside-click behavior is supplied by existing Alpine
  attributes.

### Cards and forms

- Runbook version editors show compact step summaries and reuse the project
  step modal layout for Add/Edit. Save/Cancel stay in the modal header; Remove
  stays at the bottom. Cancel, Escape, and backdrop dismissal discard draft
  changes. The page action is “Create new version”; Back is the last header
  action, including on runbook execution pages.

- Home charts use Chart.js in separate `bg-base-200` cards below summary
  metrics: one column on phones, two at `lg`. Fixed-height responsive canvases
  use semantic theme colors, no entrance animation, and visible status labels.
  Exact totals and screen-reader daily counts provide text alternatives. The
  chart bundle loads only when chart data exists; HTMX removal destroys chart
  instances and theme observers. The document owns scrolling.
- The home's running, waiting, latest-per-release/environment, and recent deployments
  refresh together every five seconds through one HTMX fragment request, along
  with running/today counts. Its completion event refreshes chart data through
  the existing activity endpoint. Summary cards remain mounted; only their
  count text changes. Charts update in place without animation, and unchanged
  activity does not redraw them;
  the main page remains mounted, including when the running list becomes empty.
- Dashboard refreshes use native view transitions through HTMX. Each deployment
  section keeps its visual identity while it moves or changes size, with a
  200ms ease-in-out transition. The header and charts stay outside the transition.
  Reduced-motion settings disable these layout transitions; browsers without
  the native API continue to refresh normally.
- Waiting lists queued deployments, deployment approvals, and artifact approvals
  separately from running work; none of these states counts as running.
- Home deployment entries use full-width `bg-base-200 rounded-box` cards with
  `p-4` and `mb-3` below `md`, then zebra tables at `md` and above. One DOM
  serves both layouts and the five-second refresh. Whole-entry links retain
  hover/focus highlights, follow the card radius, and show all labelled fields
  on phones; desktop rows keep their existing columns.
- Projects, Deployments, Environments, Lifecycles, and Templates use full-width resource cards on all
  screen sizes. Names remain plain bold headings; the entire card is a native
  navigation link, with a chevron, hover ring, and visible keyboard focus.
  Details wrap below the heading. No action column or repeated Edit buttons.
- Viewer environment cards are read-only. Lifecycle cards open the read-only
  workspace for viewers; template cards open History for viewers. Writers open
  the editors, where Template History is available in the header.
- Card navigation uses a positioned anchor, with no nested controls or script.
  The document owns scrolling; cards stay full width at every breakpoint.
- Project cards preserve environment/version pairs and status colors.
  Deployment cards show version, environment, status, and date. Filters and
  Load more stay above/below the card list; Export remains on deployment detail.
- **Structure**: `card bg-base-200 shadow` with `card-body`; fields use
  `form-control`, `label`, and `input input-bordered`.
  Shared CSS owns the form wrappers and labels, which DaisyUI 5 no longer
  supplies. It preserves full-width fields, semantic label colors, and 48px
  default controls. Tailwind 4 source scanning and both theme definitions
  live in `static/css/input.css`.
- **States**: validation uses `text-error text-sm`; alerts use semantic
  `alert-*` classes.
- Initially hidden Alpine edit forms use `x-cloak` to prevent a flash before
  initialization. Runbook editor methods live in the shared JavaScript bundle;
  templates carry initial data and short method calls.
- Runbook steps keep stable editor IDs when reordered; IDs are local UI state.
- Schedule tables reserve space for timestamps and controls, with the Schedule
  column taking the remaining width. Flex action groups sit inside table cells.
- Native dialogs connect their visible heading with `aria-labelledby`.
  Script editor headings use Alpine-generated IDs to keep repeated editors
  distinct.
- Headers containing project names wrap their heading above Back on narrow
  screens, so long names cannot push the control outside the viewport.

### Remote step execution
- Reuse native labelled selects for Server container / Agent and Host / Container.
- Show agent mode only for Agent steps. Show and require an image for either container path.
- Disable fields that do not apply. Keep variable restrictions visible for all modes.
- Explain that agent containers require a ready Docker or Podman runtime and agent/3.
- Reuse the existing form error region and viewer guards; controls wrap on small screens.

### Package repository configuration
- One active project source is shown as a card, without a repository selector.
- Authentication uses Alpine to show and enable only relevant credential fields;
  `x-cloak` prevents credentials flashing before initialization.
- Version tests use an HTMX result region with `aria-live="polite"`; the submit
  button is disabled during the request and semantic alerts show success/error.
- Configuration replacement preserves historical package pins, and the page
  states that saving affects only future snapshots.

### Deployment verification and rollback
- Verification uses the existing form fields, a native type selector, and
  supporting text explaining timeout and execution placement. Every new field
  has an explicit label; invalid input uses the existing form error region.
- Rollback uses a secondary action and a confirmation form with source and
  target versions, environment, and approval/gate status. The selected target
  is submitted explicitly so stale confirmations fail safely.
- Viewers see verification results but no rollback write control. All new
  surfaces wrap at small widths and keep the existing semantic focus states.

### Deployment detail on narrow screens
- Use a vertical stack with document scrolling. Status and the active step sit
  with the title; labeled metadata wraps long values in `text-sm`.
- Live step logs precede verification and step definitions in source order.
  Disabled verification is a compact `text-sm` line, not a card.
- Primary actions and Back use `btn` on narrow screens and `sm:btn-sm` above
  that breakpoint. Export and Rollback use the existing native dropdown.
- Step definitions use native expandable cards below `sm`, with full names and
  wrapped scripts; the existing fixed table remains above `sm`.
- Action groups use the [cluster pattern](https://github.com/changeroa/StyleGallery/blob/main/patterns/in-line-grouping/cluster.md):
  `flex flex-wrap gap-2`, with no internal scroll container. Summary controls
  use `min-h-12` from the existing spacing scale for touch access.

### Deployment list cards
- Filters start collapsed below `md`, with an Alpine toggle and an Active badge
  when filters apply. Closing the panel retains its values. At `md` and above,
  the filter form stays visible and the toggle is hidden.
- Full-width resource cards show project headings and labeled version,
  environment, status, and date fields at every size. The same cards are
  appended by Load more; no duplicate mobile DOM or action column.
- Filters use a two-column grid on phones with project/environment spanning
  both columns. Native inputs and View/Export/Filter/Clear/Load more controls
  use normal touch-sized controls; desktop keeps compact table controls.
- Metadata uses one column on phones, two on tablets, and four on desktop.
  The document owns scrolling; no sideways scrolling is needed.

### Project list cards
- Use the same resource cards at every size; names and descriptions wrap in full.
- Header controls keep normal touch sizes on phones.
- Project environment/version pairs use one, two, or four columns by screen
  size. Each version stays with its environment; long versions
  wrap without hiding status colors or text.
- Keep one set of cards for full pages and HTMX fragments, the existing viewer
  guards, and document scrolling.
- Environment and lifecycle deletion lives in a separate section of the edit
  page, with a confirmation and normal-sized destructive button. Their lists
  use the resource cards above. Canceling confirmation leaves the item intact; deletion returns
  to the list. New forms and viewer pages have no Delete control.
- Both edit pages put Save and Back in a wrapping header. Save updates the
  settings and keeps the edit page open; Back uses the shared history behavior.
  Environment creation uses header Save and Back.

### Agent maintenance and health
- Show administrative state and heartbeat health as separate labelled badges:
  active/healthy use `badge-success`, draining/drained/stale use `badge-warning`,
  offline/revoked use `badge-error`, and disabled/unknown use `badge-ghost`.
- Drain and Resume are native POST form buttons, sized `btn-xs` in lists and
  `btn-sm` on detail pages. Delete uses `btn-error`, the same sizes, and the
  existing HTMX confirmation. Confirmation explains inventory removal and
  retained history; success returns to the fleet list.
  Keep Revoke available for immediate access removal when work blocks Delete.
- Workload details use existing cards and definition lists, linked deployment
  IDs, readable empty states, and `break-all` for version identifiers.
- Status text carries meaning independently of color. Controls retain native
  keyboard access and focus treatment; long values wrap on narrow screens.

### Deployment waiting state
- Project/environment queues use a `badge-primary` queued status, a readable queue
  position, and a project-authorized link to active work. Reuse status polling
  and native Cancel buttons; viewers see queue state without write controls.
- Queued remote work with no issued claim replaces the deployment status text
  with “Waiting for agents” in the existing `badge-warning`, with
  `aria-live="polite"`.
- Reuse the deployment's existing status polling to clear the message once
  work is claimed or the deployment finishes.

### Generated artifact approval
- Step and template network/approval fields remain inside the existing editor
  modals. Runbook step modals preserve container network settings. Deployment
  approval cards sit above step logs, use wrapping metadata and full phone
  touch targets, and keep all permissions and exact-revision confirmations.
- Deployment actions use one shared polling fragment, with Export/Rollback in
  More. Queue and approval transitions update Cancel/Re-run without moving Back.
  Dashboard charts label and color queue and artifact approval states.
- Reuse `card bg-base-200 shadow`, labelled badges, definition lists, and existing small buttons for gate review.
- Show action counts, checksums, expiry, and approver. Resource details are writer-only; mask sensitive review values. Scripts and logs use the same display rules as other deployments. Long checksums wrap.
- Native labelled form fields configure network and approval paths. Native POST forms approve or reject the exact revision and checksum. Viewers receive no write or download control.
- Write-capable members can open a native details disclosure for Terraform resource changes. Load it on demand and preserve the open disclosure during status polling. Resource addresses and actions wrap; Before and After use labelled, wrapping monospace blocks, stacked on small screens. Mask sensitive values and show unknown values as known after apply. Keep the producer-supplied review warning visible; viewers see counts only.
- Awaiting gates use warning badges; approved uses success; rejected or expired uses error. State text carries meaning without color.
- Terraform actions use green `success` for additions, red `error` for removals,
  and yellow `warning` for modifications, with visible action labels and signs.
  Before/After blocks retain red/green borders. Step logs render terminal red,
  green, yellow, and bold cues as escaped text spans; plain plan diff markers
  receive the same colors. Stored and streamed logs share this renderer.
  In the light theme, colored text mixes 55% `base-content` into its semantic
  color to keep small log text and review labels readable on pale surfaces.

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

## 6. Motion & Interaction

### Home dashboard
Summary counts use a full-width two-column grid on phones and four columns on
desktop. Deployment sections use a fixed five-column table, with stacked,
labelled rows below `md`. Each row has one native link over the whole row,
with a hover highlight and keyboard focus. Names and values remain plain
text; rows have no buttons or card styling.

### Shared page structure
Main list and detail pages use `page-header`: a title column and an action
column aligned to the upper right on desktop. On phones, actions occupy the
next row, aligned right, so title length cannot change their position.
Header buttons share a compact desktop height and full-size phone controls.
Home has no creation actions; Projects and Environments provide those controls.
Edit forms put Save and Back together in that header; creation forms keep
Create and Cancel together below the fields. Destructive actions remain in
their separate section. Domain actions such as Deploy and Save immutable
version retain their specific labels and existing submit behavior.
Project overview navigation uses a single Project menu control in the header.
It opens a native dialog drawer on the right, with Configuration, Operations,
and Settings groups of plain menu links. Deploy remains a primary header action
beside Edit and Back. Viewers retain navigation but do not see Deploy or Schedules.
The drawer uses `bg-base-100`, `border-base-300`, and the existing menu hover/focus
states. Its width is 24rem, capped at the viewport minus 1rem. The drawer and
backdrop start at the visible navbar's lower edge and fill the remaining
viewport height; opening and resizing measure the navbar instead of assuming
a fixed header height. The panel owns vertical scrolling, with a sticky title/Close header.
Outside click, Escape, and Close dismiss it and restore focus to Project menu.
Selecting a link closes it and uses the existing HTMX navigation and URLs.
Drawer motion follows the side-panel mechanism from beui.dev's drawer: enter
from `translateX(100%)` over 200ms ease-out and leave toward the right over
180ms ease-in. Only transform moves; reduced-motion preferences disable travel.
Native modal focus and dismissal remain active until the exit finishes.
Shared spacing and responsive sizing provide consistency without removing color.
Environment and template forms fill the main content width, including their
headers and fields, without a centered maximum-width container.
Template Delete appears only below the edit form, with confirmation; the list
opens the editor and History is in its header. Viewer cards open History.
New forms and viewer pages have no Delete control.
New Environment uses the same header Save/Back controls as Edit Environment;
Save submits its native creation form and Back uses the shared history behavior.
Project deletion is the final edit section, below Members and all settings.
Project detail opens its existing editor in a native modal. Save refreshes the
detail and closes the dialog; Back and Escape discard unsaved fields. Members
stay inside the modal, validation errors retain submitted fields, and Delete
remains last. The standalone edit URL remains supported. The modal scrolls
internally with Save/Back in a sticky header and restores focus on closing.
Step editing uses one native modal dialog with a vertical form and Save/Cancel
in its sticky top-right header, matching project editing. The list stays
visible behind an inert backdrop; Save refreshes the list and closes the
dialog, while validation errors stay inside it. Cancel and Escape discard the
edit and return focus to its Edit button. Delete appears only at the bottom
of the edit modal. Confirmation refreshes the list and closes the modal;
focus moves to another step's Edit button, or Add Step when the list is empty.
Editor modals use the same clickable backdrop as confirmation and notification
dialogs. Clicking outside closes the dialog and discards unsaved fields, while
clicks inside keep it open. The fullscreen script editor has no outside area.
On devices with a coarse primary pointer, opening or refreshing forms does not
automatically focus an input, textarea, or select. Native dialog focus remains
on a header control; users tap a field to open the keyboard. Desktop field focus,
keyboard navigation, and focus restoration on dismissal remain available.
New Project, Environment, Lifecycle, and Template open the existing forms in
the same modal. Save remains in the top-right header while scrolling. Cancel,
Back, Escape, and backdrop clicks close without creating a resource. Validation
stays inside the dialog; successful creation closes it and refreshes the list
without a document reload. Direct form URLs and native POSTs remain supported.
Add Step uses the same dialog as Edit Step, with Save/Cancel in its sticky
header. Validation preserves the script and placement fields inside the modal;
Save refreshes the list and returns focus to Add Step. Cancel, Escape, and
outside clicks discard the new step. The fullscreen script editor still works
inside this dialog.

Environment, Lifecycle, and Template cards open their editors in the same
dialog. Save and Delete refresh the list and close the dialog without changing
its URL. Back, Escape, and outside clicks discard unsaved settings and return
focus to the card. Validation retains submitted fields inside the dialog.
Lifecycle stage controls remain usable inside the modal. Delete is the last
section, and standalone editor URLs and read-only viewer behavior remain.
Deployments keep their existing pages.
The releases list contains Version, Created At, and Actions columns. Deployment
environment selectors and Deploy/Force controls belong to the project Deploy
page. Release creation, snapshot links, and deletion remain on the releases page.

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
