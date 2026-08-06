# Scene creation and editing UI — design

Date: 2026-07-28
Status: approved, implementing

## Problem

Publishing a playlist is impossible on the deployed Jetson. The reported symptom
is "I can upload images and websites but cannot publish them as a scene".

The cause is not in the publish path. It is a missing feature:

- `POST /api/v1/scenes` and `PUT /api/v1/scenes/{id}` exist, are role-guarded
  (`internal/api/routes.go:46-47`) and are covered by tests.
- The admin SPA never calls them. Grepping `web/src` for `/api/v1/scenes` finds
  exactly two callers, `duplicate` and `delete`
  (`web/src/admin/views/PlaylistView.tsx:98,111`).

So a draft revision can never become non-empty. `PlaylistView.tsx:159` renders
the publish button `disabled` while `scenes.length === 0`, and the store would
refuse anyway: "cannot publish: no enabled scenes; the display would be blank"
(`internal/store/playlist.go:632`).

Media upload works because `MediaView` posts to `/api/v1/media`. There is simply
no path from an uploaded file to a scene.

A second, independent gap surfaced while tracing: `ZoneRenderer.tsx:85` has no
`case 'social'`, so a social zone renders "Unsupported content type" even though
the adapters, moderation service and player endpoint `/api/v1/player/social/{id}`
are all implemented.

## Scope

Build the scene editor as plain forms in the existing dashboard style. No drag
canvas, no live preview. Cover every layout and every content type, because the
backend and player already support them and a partial editor reproduces the same
class of gap for the next content type an operator reaches for.

## Architecture

New module `web/src/admin/scene/`:

| File | Responsibility |
|---|---|
| `defaults.ts` | Default config object per content type; scene skeleton for a layout |
| `pickers.tsx` | `MediaPicker`, `WebsitePicker`, `FeedPicker` — select an existing record |
| `contentForms.tsx` | One form per content type, fields mirroring the Go validators |
| `ZoneForm.tsx` | Content-type selector, dispatch to the config form, zone style |
| `SceneEditor.tsx` | Modal: scene fields, layout picker, one `ZoneForm` per slot, save |

Each unit is independently testable: `defaults.ts` is pure data, `contentForms`
are controlled components over a config object, `SceneEditor` owns the only
network calls.

## Constraints the editor must respect

These are where a naive editor fails against the existing validators:

1. `content.decode` uses `DisallowUnknownFields`. Config JSON is assembled
   field-by-field per type; never spread an arbitrary object into it, or every
   save returns 422.
2. Fixed layouts require exactly one zone per slot, keyed by the layout's slot
   IDs. Changing layout re-maps existing zones by position onto the new slot set,
   padding with `empty` zones or trimming the excess. Rects are not sent for
   fixed layouts — the server overwrites them (`scene.go:144`).
3. The custom grid takes rects from the zones and validates bounds and overlap.
4. `days_mask` defaults to 127; 0 is invalid. Duration floor is 3000 ms.
5. Colours must match `#rgb` / `#rrggbb` / `#rrggbbaa`.
6. At least one zone must have a content type other than `empty`.

## Data flow

`SceneEditor` loads reference data once (`/api/v1/layouts`,
`/api/v1/content-types`) and the picker lists (`/api/v1/media`,
`/api/v1/websites`, `/api/v1/social/feeds`). Save issues `POST /api/v1/scenes`
for a new scene or `PUT /api/v1/scenes/{id}` for an edit, then the parent
reloads the playlist.

## Error handling

`saveScene` returns 422 with `ValidationErrors` in `detail` for a scene that
saved but is invalid, which the client already parses via `ApiError.fieldErrors`.
Field errors are rendered inline against the matching input, keyed on the
server's field path (`zones[a].heading`). Non-422 failures show the existing
`ErrorNote`.

The 422 path is deliberately not treated as a failure: the server saves the
scene so work in progress is not lost, and publishing is what refuses invalid
scenes. The editor closes and the row shows an "Invalid" pill.

## Entry points

- `PlaylistView`: "New scene" in the header, "Edit" on each row.
- `MediaView` and `WebsitesView`: "Add to playlist" opens the editor prefilled
  with a fullscreen one-zone scene referencing that item.

## Player fix

Add `SocialZone` to `ZoneRenderer.tsx`, fetching `/api/v1/player/social/{feed}`
and rendering the configured template, so a social scene built in the editor is
not broken on screen.

## Testing

- vitest: default configs survive `DisallowUnknownFields` (no key outside the Go
  struct), layout-switch slot remapping, config serialization per type.
- Existing Go tests already cover save, validation and publish.
- Manual: run locally, create a scene, publish, confirm the revision renders.

## Out of scope

Drag-to-reorder zones, live preview thumbnails, and a visual custom-grid editor.
The custom grid is editable through numeric rect fields.
