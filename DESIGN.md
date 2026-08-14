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
`w-full px-4 sm:px-6 lg:px-8`; navigation changes at `md`; responsive tables
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

## 6. Motion & Interaction

Existing interaction is intentionally minimal: native `details` disclosure,
Alpine theme/dropdown state, HTMX swaps, and `x-transition.opacity.duration.300ms`
for toasts. New controls reuse native disclosure and existing Alpine behavior;
no new motion or JavaScript package is introduced.

## 7. Depth & Surface

The existing strategy is mixed semantic elevation: `bg-base-200` distinguishes
navbar/cards/dropdowns, `border-base-300` separates the navbar and form areas,
and DaisyUI `shadow` elevates cards, stats, and menus. New surfaces reuse these
classes rather than custom shadow, border, or color values.
