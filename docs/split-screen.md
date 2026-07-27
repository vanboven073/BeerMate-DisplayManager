# Split-screen scenes

Split-screen is a first-class production feature, not an add-on. It falls out of
the core model: **every playlist entry is a scene of one or more zones**, and a
traditional fullscreen slide is simply a scene with a single zone covering the
whole screen. Scheduling, validation, publishing and rendering therefore have one
code path for everything.

## Scenes and zones

A **scene** is one full-screen composition: a layout, a duration, a transition, an
optional active-from/until window and day-of-week mask, and its zones. A **zone**
is a rectangle (in screen percentages) with a content type, a content reference, a
per-type config and a presentational style (background, padding, border, radius,
overflow, label).

## The 16 layouts

| Id | Name |
|---|---|
| `fullscreen` | Fullscreen |
| `cols_50_50` | Two columns 50/50 |
| `cols_60_40` | Two columns 60/40 |
| `cols_40_60` | Two columns 40/60 |
| `rows_50_50` | Two rows 50/50 |
| `main_right_sidebar` | Main + right sidebar |
| `main_left_sidebar` | Main + left sidebar |
| `main_bottom_ticker` | Main + bottom ticker |
| `main_top_banner` | Main + top banner |
| `quad` | Four quadrants |
| `left_two_right` | Large left, two stacked right |
| `top_two_bottom` | Large top, two below |
| `dashboard_social` | Dashboard + social feed |
| `image_countdown` | Image + countdown |
| `website_kpi` | Website + KPI panel |
| `custom_grid` | Freely positioned zones (≤ 8) |

Fixed layouts define **authoritative** zone geometry: a saved scene cannot drift
from its template because client-supplied rects are overwritten at validation. A
test asserts every fixed layout tiles the screen to exactly 100% with no overlap —
gaps would show as black bands, overlaps would silently hide content. The custom
grid takes geometry from the scene and is validated for bounds and overlap.

## Zone content types

`image`, `video`, `website`, `countdown`, `clock`, `kpi`, `qr`, `announcement`,
`image_text`, `social`, `text`, `ticker`, `event` (and the internal `empty` /
`fallback`). Each has a validator and a player renderer.

## Editing

Dashboard → **Playlist** → a scene. Pick a layout, assign content to each slot,
set duration and transition, optionally set an active window and day mask. Zone
slots are pre-hinted with sensible content types (e.g. a sidebar suggests social,
KPI or countdown). Reordering scenes works from the keyboard and touch, not just
drag. Editing happens in the draft; **Publish** makes it live atomically.

## Resource discipline on the Jetson

Split-screen multiplies work, so the player and backend bound it:

- **One persistent player page** renders all zones; there is no tab or window
  proliferation.
- The number of concurrently captured managed websites is capped
  (`max_managed_captures`).
- A single one-second clock drives every countdown/clock zone.
- Videos are explicitly unloaded when their scene ends; scenes are keyed by
  stable id so the previous scene's DOM is torn down entirely rather than
  reconciled.
- The custom grid is capped at 8 zones — beyond that a display becomes unreadable
  long before it becomes slow.

## Adding a layout

Add a `Layout` to the `layouts` slice in `internal/content/layout.go`. Fixed
layouts must tile to 100% with no overlap (the test enforces it). No player change
is needed — the renderer positions zones from their rects.
