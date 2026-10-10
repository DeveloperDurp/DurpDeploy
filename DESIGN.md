# Design system

DurpDeploy is a compact operations console. Reuse DaisyUI semantic surfaces,
readable tables, and visible status labels in both `mocha` and `light` themes.
Theme definitions and Tailwind scanning live in `static/css/input.css`.

## Tokens and layout

| Purpose | Classes |
| --- | --- |
| Page / raised / contrast surface | `bg-base-100` / `bg-base-200` / `bg-base-300` |
| Dividers | `border-base-300` |
| Primary / secondary / destructive action | `btn-primary` / `btn-secondary` / `btn-error` |
| Success / warning / error | Semantic `badge-*` and `alert-*` classes |
| Page / card title | `text-3xl font-bold` / `card-title` or `text-xl font-bold` |
| Supporting text / metadata | `text-sm` / `text-xs` |
| Commands, checksums, credentials | `font-mono` |

Use the default system sans-serif and 4px spacing scale. Reuse `gap-2`, `gap-4`,
`p-4`, `mb-4`, and `space-y-6`. Use semantic colors, borders, and shadows.
The main shell is `w-full px-4 sm:px-6 lg:px-8`, without a centered maximum width.
The document owns scrolling except inside native dialogs and the project drawer.

`page-header` places actions beside the title on desktop and below it on phones.
Keep Save and Back together. Controls use full touch sizes on phones and compact
desktop variants. Long names and action groups wrap with `flex flex-wrap gap-2`.
Never let a title push Back outside the viewport. Navigation changes at `xl`.

## Resource lists and tables

Projects, Deployments, Environments, Lifecycles, and Templates use full-width
cards at every size. Each card has one native positioned anchor, a plain bold
heading, a chevron, and visible hover/focus treatment. Do not nest controls.
Wrap all values; metadata uses one, two, or four columns by screen size.
Keep environment/version pairs together and retain status colors and labels.

Home deployments use labelled cards below `md` and zebra tables above it,
with one DOM and one link per entry. Other data tables use
`table table-zebra table-fixed w-full`, percentage column widths, truncated text,
and non-wrapping timestamps/actions. See [AGENTS.md](AGENTS.md#ui-design--table-layout-conventions)
for column patterns. Flex action groups belong inside cells.

Deployment filters start collapsed below `md`. The toggle shows an Active badge;
closing it retains values. Desktop filters stay visible. Load more appends the
same cards. Releases list Version, Created At, and Actions; environment selectors
and Deploy/Force belong on the project Deploy page.

## Forms and dialogs

Use labelled native controls and existing form wrappers. Validation uses
`text-error text-sm` and semantic alerts. Shared CSS provides wrappers, label
colors, full-width fields, and 48px default controls. Hide initial Alpine forms
with `x-cloak`; keep directive expressions short and methods in the shared bundle.

Create and edit Project, Environment, Template, and Step in native dialogs.
Create Lifecycle in a native dialog.
Keep Save and Back/Cancel in a sticky header. Validation retains submitted values
inside the dialog. Success refreshes the relevant list/detail and closes it.
Cancel, Back, Escape, and backdrop clicks discard drafts and restore focus.
Preserve standalone URLs and native form submissions. Deployments use their pages.

Lifecycle cards open the full workspace page. Keep promotion stages, shared
variables for global administrators, and lifecycle settings together.
Back returns to the list. Save and Delete return there after they persist changes.
Keep Delete last and preserve read-only viewer pages.

Lifecycle variable edits apply to each new deployment or runbook execution,
including existing releases. Project values remain release and runbook snapshots.
Queued and active executions keep captured values.
Explain this boundary in the editor and release page.

Delete is the final edit section, after settings and members, with confirmation.
No Delete control appears on creation forms or viewer pages. Step deletion returns
focus to another Edit button or Add Step. The fullscreen script editor has no
outside backdrop area. Dialog headings use `aria-labelledby`; repeated editor
headings use unique Alpine IDs. Coarse-pointer devices initially focus a header
control rather than opening the keyboard. Preserve desktop keyboard navigation.

Runbook version editors use compact step summaries and the same Add/Edit modal.
Save/Cancel stay in the header and Remove at the bottom. Stable editor IDs survive
reordering. The page action is **Create new version**; Back remains last.
Schedules reserve timestamp/action space and let the Schedule column expand.

## Navigation and motion

Navbar menus use native `details.dropdown.dropdown-end`, a focusable summary,
and `ul.dropdown-content.menu`. Active links use `menu-active`.
Internal navigation uses HTMX to replace main content, update navbar/title, and
push the existing URL. Focus moves to the main landmark. History restoration
fetches fresh content without storing protected markup in browser storage.
Forms, downloads, login/logout, external links, and modified clicks stay native.
Removed Alpine components release streams and observers.

Back uses native history with the existing parent URL as a direct-entry fallback.
Replace direct entries to avoid loops; keep anchors functional without JavaScript.
Use Navigation API position when available, otherwise favor real browser history.
Breadcrumbs, named destinations, and Cancel retain explicit destinations.

The project header has one **Project menu** drawer, with Configuration, Operations,
and Settings groups. Deploy stays beside Edit and Back for writers. The drawer
is 24rem wide, capped at viewport minus 1rem. Measure the navbar for its top edge;
use internal vertical scrolling and a sticky title/Close header. Escape, backdrop,
and Close restore focus. Links close the drawer and use existing HTMX navigation.
Enter from the right in 200ms, exit in 180ms; keep native modal focus until exit.
Reduced-motion preferences disable travel. Avoid new motion or JavaScript packages.

## Home dashboard

Summary counts use two columns on phones and four on desktop. Home has no create
controls; Projects and Environments provide them. Running, waiting, latest, and
recent deployments refresh together every five seconds through one HTMX fragment.
Queued work and approvals belong under Waiting and never count as running.
Keep the page and summary cards mounted; update counts and charts in place.

Chart.js cards sit below summaries, one column on phones and two at `lg`.
Use fixed-height responsive canvases, semantic colors, visible status labels,
exact totals, and screen-reader daily counts. Load the chart bundle only with data.
Destroy chart instances and theme observers on removal. Avoid animation/redraw for
unchanged activity. Native view transitions animate deployment sections for 200ms;
keep header/charts outside them and respect reduced motion and unsupported browsers.

## Deployment and agent surfaces

Deployment details stack metadata, actions, and live step logs on phones.
Keep verification below logs; disabled verification is a compact text line.
Show scripts on project/release pages rather than duplicating them in execution
logs. Export/Rollback live in More; one polling fragment updates other actions
without moving Back. Rollback confirms explicit source/target/environment/gates.
Viewers see results and queue state without write controls.

Each release step has one native `details` log disclosure in release order.
Running, waiting, and failed steps open automatically. A polite status line names
the active step even when quiet. Show unattributed history as deployment messages.
Render terminal colors/bold as escaped spans, including plain plan diff markers.
In the light theme, mix semantic log colors with 55% `base-content` for contrast.

Use native Server container / Agent and Host / Container selects. Show only
applicable fields; require images for container paths and retain variable controls.
Agent containers require `agent/3` and a ready Docker/Podman runtime.
Show administrative state separately from heartbeat health, with labelled badges.
Drain is secondary, Resume positive, and Revoke/Delete destructive with confirmation.
When unresolved work blocks Delete, explain immediate access removal via Revoke.
Queued unclaimed work shows **Waiting for agents**, with polite live status.

## Packages and artifact review

Show one active package source without a repository selector. Enable only relevant
credential fields and use `x-cloak`. Version tests use a polite HTMX result region,
disabled submit during requests, and semantic alerts. Saving affects future snapshots.

Keep network/artifact settings in existing editor modals. Review cards show counts,
checksum, revision, expiry, and approver. Long values wrap. Native forms approve
or reject the exact revision/checksum. Preserve permissions: viewers get counts,
without write/download controls; writers can load redacted resource details.
Preserve an open disclosure during polling. Mask sensitive values and label unknown
values as known after apply. Keep producer-trust warnings visible.

Terraform additions/removals/modifications use success/error/warning colors,
action text, and signs. Before/After monospace blocks stack on phones and retain
red/green borders. Awaiting gates use warning, approved success, expired/rejected
error; status must remain understandable without color.

## Permissions and accessibility

Apply [viewer guards](AGENTS.md#viewer-role--ui-gating-pattern) to every write
control and form, including routes already gated by project/admin middleware.
Viewer lifecycle cards open read-only workspaces; template cards open History.
Retain native keyboard interaction, visible focus, labelled fields, live result
regions, reduced motion, and focus restoration after dialog dismissal.

## Historical UI follow-up

Re-check audit-detail spacing, long navbar names, template action overflow, and
lifecycle navigation during visual audits. These were historical feedback items;
the current resource-card and modal rules above remain the design contract.
