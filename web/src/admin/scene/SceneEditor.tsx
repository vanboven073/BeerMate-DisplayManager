/**
 * The scene editor.
 *
 * A scene is one full screen: a layout, some scene-level timing, and one zone
 * per slot. This component owns the only network calls in the editor; the forms
 * below it are controlled components over a draft object.
 */

import { useCallback, useEffect, useMemo, useState } from 'preact/hooks';
import { api, ApiError } from '../../lib/api';
import type { Layout, Scene } from '../../lib/types';
import { ErrorNote } from '../ui';
import { ZoneForm } from './ZoneForm';
import { useRefData } from './pickers';
import {
  CUSTOM_GRID,
  MAX_CUSTOM_ZONES,
  MIN_DURATION_MS,
  type DraftScene,
  defaultConfig,
  draftFromScene,
  fromLocalInput,
  newScene,
  toLocalInput,
  toPayload,
  zonesForLayout,
} from './model';

const DAYS = [
  { bit: 1, label: 'Mon' },
  { bit: 2, label: 'Tue' },
  { bit: 4, label: 'Wed' },
  { bit: 8, label: 'Thu' },
  { bit: 16, label: 'Fri' },
  { bit: 32, label: 'Sat' },
  { bit: 64, label: 'Sun' },
];

const TRANSITIONS = [
  { value: 'fade', label: 'Fade' },
  { value: 'none', label: 'None' },
  { value: 'slide_left', label: 'Slide left' },
  { value: 'slide_up', label: 'Slide up' },
  { value: 'dissolve', label: 'Dissolve' },
];

/** A scene the caller wants opened pre-filled, e.g. from the media library. */
export interface Prefill {
  name: string;
  contentType: 'image' | 'video' | 'website';
  contentRef: string;
}

export function SceneEditor({
  scene,
  prefill,
  onClose,
  onSaved,
}: {
  /** The scene to edit, or null to create a new one. */
  scene: Scene | null;
  prefill?: Prefill;
  onClose: () => void;
  /** Called after a successful save; `warning` is set when the scene saved but is invalid. */
  onSaved: (warning?: string) => void;
}) {
  const [layouts, setLayouts] = useState<Layout[]>([]);
  const [draft, setDraft] = useState<DraftScene | null>(null);
  const [error, setError] = useState('');
  const [fieldErrors, setFieldErrors] = useState<Array<{ field: string; message: string }>>([]);
  const [busy, setBusy] = useState(false);
  const data = useRefData(true);

  // Load the layout catalogue, then seed the draft from it.
  useEffect(() => {
    const ctrl = new AbortController();
    async function load() {
      try {
        const res = await api.get<{ layouts: Layout[] }>('/api/v1/layouts', ctrl.signal);
        const all = res.layouts || [];
        setLayouts(all);
        if (scene) {
          setDraft(draftFromScene(scene));
          return;
        }
        const first = all[0];
        const seeded = newScene(first);
        if (prefill) {
          seeded.name = prefill.name;
          const zone = seeded.zones[0];
          if (zone) {
            seeded.zones = [
              { ...zone, content_type: prefill.contentType, content_ref: prefill.contentRef,
                config: defaultConfig(prefill.contentType) },
            ];
          }
        }
        setDraft(seeded);
      } catch (e) {
        if (ctrl.signal.aborted) return;
        setError(e instanceof Error ? e.message : 'Could not load layouts');
      }
    }
    void load();
    return () => ctrl.abort();
  }, [scene, prefill]);

  // Escape closes, which is what every other dialog on the dashboard does.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [onClose]);

  const layout = useMemo(
    () => layouts.filter((l) => l.id === (draft ? draft.layout : ''))[0],
    [layouts, draft],
  );

  const fieldError = useCallback(
    (path: string) => {
      const hit = fieldErrors.filter((f) => f.field === path)[0];
      return hit ? hit.message : undefined;
    },
    [fieldErrors],
  );

  const patch = (next: Partial<DraftScene>) => setDraft((d) => (d ? { ...d, ...next } : d));

  const changeLayout = (id: string) => {
    setDraft((d) => {
      if (!d) return d;
      const target = layouts.filter((l) => l.id === id)[0];
      return { ...d, layout: id, zones: zonesForLayout(target, d.zones) };
    });
  };

  const save = async () => {
    if (!draft) return;
    setBusy(true);
    setError('');
    setFieldErrors([]);
    try {
      const body = toPayload(draft);
      if (draft.id > 0) {
        await api.put(`/api/v1/scenes/${draft.id}`, body);
      } else {
        await api.post('/api/v1/scenes', body);
      }
      onSaved();
    } catch (e) {
      if (e instanceof ApiError && e.status === 422) {
        // The server saved the scene and reported why it is not publishable.
        // Closing rather than staying open is deliberate: on a create the new
        // scene already has an id the client was not told, so a second save
        // from this form would insert a duplicate. The operator reopens the
        // row, which edits the real record.
        setFieldErrors(e.fieldErrors);
        onSaved(e.message);
        return;
      }
      setError(e instanceof ApiError ? e.message : 'Could not save the scene');
    } finally {
      setBusy(false);
    }
  };

  const title = scene ? `Edit “${scene.name}”` : 'New scene';

  return (
    <div class="bm-modal" role="dialog" aria-modal="true" aria-label={title}>
      <div class="bm-modal__backdrop" onClick={onClose} />
      <div class="bm-modal__panel">
        <header class="bm-modal__head">
          <h2 class="bm-modal__title">{title}</h2>
          <button class="bm-iconbtn" type="button" onClick={onClose} aria-label="Close">
            ✕
          </button>
        </header>

        <div class="bm-modal__body">
          {error && <ErrorNote message={error} />}
          {data.error && <ErrorNote message={data.error} />}

          {!draft ? (
            <p class="bm-muted">Loading…</p>
          ) : (
            <form class="bm-form" onSubmit={(e) => { e.preventDefault(); void save(); }} noValidate>
              <div class="bm-field">
                <label class="bm-label" for="scene-name">Name</label>
                <input id="scene-name" class="bm-input" value={draft.name} maxLength={120}
                  placeholder="What this slide is, for your own reference"
                  onInput={(e) => patch({ name: (e.target as HTMLInputElement).value })} />
                {fieldError('name') && (
                  <p class="bm-field__error bm-small" role="alert">{fieldError('name')}</p>
                )}
              </div>

              <div class="bm-field">
                <label class="bm-label" for="scene-layout">Layout</label>
                <select id="scene-layout" class="bm-select" value={draft.layout}
                  onChange={(e) => changeLayout((e.target as HTMLSelectElement).value)}>
                  {layouts.map((l) => (
                    <option key={l.id} value={l.id}>{l.name}</option>
                  ))}
                </select>
                {layout && <p class="bm-small bm-muted">{layout.description}</p>}
                {fieldError('layout') && (
                  <p class="bm-field__error bm-small" role="alert">{fieldError('layout')}</p>
                )}
              </div>

              <div class="bm-grid2">
                <div class="bm-field">
                  <label class="bm-label" for="scene-duration">Seconds on screen</label>
                  <input id="scene-duration" class="bm-input" type="number"
                    min={MIN_DURATION_MS / 1000} step={1}
                    value={String(Math.round(draft.duration_ms / 1000))}
                    onInput={(e) => {
                      const secs = Number((e.target as HTMLInputElement).value) || 0;
                      patch({ duration_ms: Math.round(secs * 1000) });
                    }} />
                  <p class="bm-small bm-muted">At least {MIN_DURATION_MS / 1000} seconds.</p>
                  {fieldError('duration_ms') && (
                    <p class="bm-field__error bm-small" role="alert">{fieldError('duration_ms')}</p>
                  )}
                </div>

                <div class="bm-field">
                  <label class="bm-label" for="scene-transition">Transition</label>
                  <select id="scene-transition" class="bm-select" value={draft.transition || 'fade'}
                    onChange={(e) => patch({ transition: (e.target as HTMLSelectElement).value })}>
                    {TRANSITIONS.map((t) => (
                      <option key={t.value} value={t.value}>{t.label}</option>
                    ))}
                  </select>
                </div>
              </div>

              <fieldset class="bm-subgroup">
                <legend class="bm-label">Days this scene plays</legend>
                <div class="bm-days">
                  {DAYS.map((d) => (
                    <label class="bm-check" key={d.bit}>
                      <input type="checkbox" checked={(draft.days_mask & d.bit) !== 0}
                        onChange={(e) => {
                          const on = (e.target as HTMLInputElement).checked;
                          patch({ days_mask: on ? draft.days_mask | d.bit : draft.days_mask & ~d.bit });
                        }} />
                      <span>{d.label}</span>
                    </label>
                  ))}
                </div>
                {draft.days_mask === 0 && (
                  <p class="bm-field__error bm-small" role="alert">
                    Select at least one day, or the scene never plays.
                  </p>
                )}
                {fieldError('days_mask') && (
                  <p class="bm-field__error bm-small" role="alert">{fieldError('days_mask')}</p>
                )}
              </fieldset>

              <details class="bm-details">
                <summary>Scheduling window and background</summary>
                <div class="bm-details__body">
                  <div class="bm-grid2">
                    <div class="bm-field">
                      <label class="bm-label" for="scene-from">Not before (optional)</label>
                      <input id="scene-from" class="bm-input" type="datetime-local"
                        value={toLocalInput(draft.active_from)}
                        onInput={(e) => patch({ active_from: fromLocalInput((e.target as HTMLInputElement).value) })} />
                    </div>
                    <div class="bm-field">
                      <label class="bm-label" for="scene-until">Not after (optional)</label>
                      <input id="scene-until" class="bm-input" type="datetime-local"
                        value={toLocalInput(draft.active_until)}
                        onInput={(e) => patch({ active_until: fromLocalInput((e.target as HTMLInputElement).value) })} />
                      {fieldError('active_until') && (
                        <p class="bm-field__error bm-small" role="alert">{fieldError('active_until')}</p>
                      )}
                    </div>
                  </div>
                  <div class="bm-field">
                    <label class="bm-label" for="scene-bg">Scene background</label>
                    <input id="scene-bg" class="bm-input" value={draft.background} placeholder="#0B0B0F"
                      onInput={(e) => patch({ background: (e.target as HTMLInputElement).value })} />
                    {fieldError('background') && (
                      <p class="bm-field__error bm-small" role="alert">{fieldError('background')}</p>
                    )}
                  </div>
                  <label class="bm-check">
                    <input type="checkbox" checked={draft.enabled}
                      onChange={(e) => patch({ enabled: (e.target as HTMLInputElement).checked })} />
                    <span>Include this scene in the rotation</span>
                  </label>
                </div>
              </details>

              {fieldError('zones') && (
                <p class="bm-field__error bm-small" role="alert">{fieldError('zones')}</p>
              )}

              {draft.zones.map((zone, i) => (
                <ZoneForm
                  key={`${draft.layout}-${i}`}
                  zone={zone}
                  index={i}
                  slotLabel={slotLabelFor(layout, i, zone.slot)}
                  custom={draft.layout === CUSTOM_GRID}
                  data={data}
                  fieldError={fieldError}
                  onChange={(next) =>
                    patch({ zones: draft.zones.map((z, j) => (i === j ? next : z)) })
                  }
                  onRemove={
                    draft.zones.length > 1
                      ? () => patch({ zones: draft.zones.filter((_, j) => j !== i) })
                      : undefined
                  }
                />
              ))}

              {draft.layout === CUSTOM_GRID && draft.zones.length < MAX_CUSTOM_ZONES && (
                <button class="bm-btn bm-btn--secondary bm-btn--sm" type="button"
                  onClick={() => patch({ zones: draft.zones.concat([nextCustomZone(draft.zones.length)]) })}>
                  Add a zone
                </button>
              )}

              <footer class="bm-modal__foot">
                <button class="bm-btn bm-btn--ghost" type="button" onClick={onClose} disabled={busy}>
                  Cancel
                </button>
                <button class="bm-btn bm-btn--primary" type="submit" disabled={busy || !draft.name.trim()}>
                  {busy ? 'Saving…' : 'Save scene'}
                </button>
              </footer>
            </form>
          )}
        </div>
      </div>
    </div>
  );
}

function slotLabelFor(layout: Layout | undefined, index: number, slot: string): string {
  if (!layout || !layout.slots) return `Zone ${index + 1} (${slot})`;
  const s = layout.slots[index];
  return s ? s.label : `Zone ${index + 1} (${slot})`;
}

/** A new custom-grid zone, stacked below the others rather than overlapping. */
function nextCustomZone(count: number) {
  const slot = String.fromCharCode(97 + count);
  return {
    slot,
    label: '',
    content_type: 'empty' as const,
    content_ref: '',
    config: {},
    style: {},
    rect: { x: 0, y: 0, w: 50, h: 50 },
    z: count,
  };
}
