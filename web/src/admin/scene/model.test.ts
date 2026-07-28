import { describe, expect, it } from 'vitest';
import type { Layout, Scene } from '../../lib/types';
import {
  ALL_DAYS_MASK,
  CUSTOM_GRID,
  defaultConfig,
  draftFromScene,
  fromLocalInput,
  newScene,
  SELECTABLE_TYPES,
  toLocalInput,
  toPayload,
  zonesForLayout,
} from './model';

/**
 * The keys each content type's Go struct declares, from
 * internal/content/types.go. The server decodes zone configuration with
 * DisallowUnknownFields, so a key the editor emits that is missing here fails
 * every save with a 422 — which is exactly the class of bug that made scenes
 * unpublishable in the first place.
 */
const GO_FIELDS: Record<string, string[]> = {
  empty: [],
  fallback: [],
  image: ['fit', 'background', 'title', 'subtitle', 'overlay', 'overlay_position'],
  video: ['mode', 'muted', 'poster_id', 'fit'],
  website: ['zoom', 'refresh_on_show'],
  countdown: [
    'title', 'subtitle', 'target', 'timezone', 'completion_message',
    'completion_image_id', 'hide_zero_units', 'expire_after_done', 'variant',
  ],
  clock: ['timezone', 'show_seconds', 'show_date', 'title', 'subtitle', 'variant', 'twenty_four_hour'],
  kpi: ['title', 'cards', 'source', 'url', 'credential_id', 'refresh_sec', 'timeout_sec', 'fallback_message'],
  qr: ['heading', 'description', 'kind', 'data', 'ec_level', 'with_logo', 'foreground', 'background'],
  announcement: [
    'heading', 'body', 'image_id', 'show_logo', 'background', 'align',
    'text_size', 'text_color', 'cta', 'qr_data',
  ],
  image_text: [
    'template', 'heading', 'subtitle', 'body', 'image_id', 'show_logo',
    'cta', 'qr_data', 'background', 'fit',
  ],
  social: ['feed_id', 'template', 'max_items', 'show_meta'],
  text: ['heading', 'body', 'align', 'text_size', 'text_color', 'background'],
  ticker: [
    'source', 'messages', 'feed_id', 'direction', 'speed_px_sec', 'separator',
    'text_size', 'text_color', 'background', 'pause_on_priority',
  ],
  event: [
    'title', 'subtitle', 'starts_at', 'location', 'image_id', 'show_logo',
    'qr_data', 'embed_countdown', 'variant',
  ],
};

/** KPI card keys, from content.KPICard. */
const KPI_CARD_FIELDS = ['label', 'value', 'unit', 'target', 'trend', 'status', 'icon', 'description', 'source_path'];

const fullscreen: Layout = {
  id: 'fullscreen',
  name: 'Fullscreen',
  description: '',
  slots: [{ id: 'a', label: 'Full screen', rect: { x: 0, y: 0, w: 100, h: 100 } }],
  custom: false,
};

const split: Layout = {
  id: 'cols_50_50',
  name: 'Two columns',
  description: '',
  slots: [
    { id: 'a', label: 'Left', rect: { x: 0, y: 0, w: 50, h: 100 } },
    { id: 'b', label: 'Right', rect: { x: 50, y: 0, w: 50, h: 100 } },
  ],
  custom: false,
};

const quad: Layout = {
  id: 'quad',
  name: 'Quad',
  description: '',
  slots: [
    { id: 'a', label: 'Top left', rect: { x: 0, y: 0, w: 50, h: 50 } },
    { id: 'b', label: 'Top right', rect: { x: 50, y: 0, w: 50, h: 50 } },
    { id: 'c', label: 'Bottom left', rect: { x: 0, y: 50, w: 50, h: 50 } },
    { id: 'd', label: 'Bottom right', rect: { x: 50, y: 50, w: 50, h: 50 } },
  ],
  custom: false,
};

const customGrid: Layout = { id: CUSTOM_GRID, name: 'Custom', description: '', slots: null, custom: true };

describe('default configs match the Go structs', () => {
  for (const type of SELECTABLE_TYPES) {
    it(`${type} emits no key the server would reject`, () => {
      const allowed = GO_FIELDS[type];
      expect(allowed, `no field list recorded for ${type}`).toBeDefined();
      for (const key of Object.keys(defaultConfig(type))) {
        expect(allowed).toContain(key);
      }
    });
  }

  it('KPI cards emit no unknown key either', () => {
    const cards = defaultConfig('kpi').cards as Array<Record<string, unknown>>;
    expect(cards.length).toBeGreaterThan(0);
    for (const key of Object.keys(cards[0]!)) {
      expect(KPI_CARD_FIELDS).toContain(key);
    }
  });

  it('produces an RFC3339 target the Go parser accepts', () => {
    const target = defaultConfig('countdown').target as string;
    expect(target).toMatch(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}/);
    expect(isNaN(new Date(target).getTime())).toBe(false);
  });
});

describe('zonesForLayout', () => {
  it('creates one zone per slot with the layout geometry', () => {
    const zones = zonesForLayout(split, []);
    expect(zones.map((z) => z.slot)).toEqual(['a', 'b']);
    expect(zones[1]!.rect).toEqual({ x: 50, y: 0, w: 50, h: 100 });
  });

  it('carries content across when the layout grows', () => {
    const before = zonesForLayout(fullscreen, []);
    before[0]!.content_type = 'image';
    before[0]!.content_ref = '7';

    const after = zonesForLayout(split, before);
    expect(after).toHaveLength(2);
    expect(after[0]!.content_type).toBe('image');
    expect(after[0]!.content_ref).toBe('7');
    // The added slot starts empty rather than duplicating the first zone.
    expect(after[1]!.content_type).toBe('empty');
  });

  it('drops surplus zones when the layout shrinks', () => {
    const four = zonesForLayout(quad, []);
    const one = zonesForLayout(fullscreen, four);
    expect(one).toHaveLength(1);
    expect(one[0]!.slot).toBe('a');
  });

  it('re-keys slots so they always match the new layout', () => {
    // A zone left on slot "d" after switching to a two-slot layout would be
    // rejected: "layout has no slot d".
    const four = zonesForLayout(quad, []);
    const two = zonesForLayout(split, four);
    expect(two.map((z) => z.slot)).toEqual(['a', 'b']);
  });

  it('keeps the operator-defined zones for a custom grid', () => {
    const zones = zonesForLayout(split, []);
    const custom = zonesForLayout(customGrid, zones);
    expect(custom).toHaveLength(2);
  });

  it('gives a fresh custom grid one full-screen zone', () => {
    const zones = zonesForLayout(customGrid, []);
    expect(zones).toHaveLength(1);
    expect(zones[0]!.rect).toEqual({ x: 0, y: 0, w: 100, h: 100 });
  });
});

describe('toPayload', () => {
  it('sends a new scene enabled and on every day', () => {
    // days_mask 0 fails validation and enabled=false makes publish report
    // "no enabled scenes", so both zero values have to be overridden.
    const body = toPayload(newScene(fullscreen)) as Record<string, unknown>;
    expect(body.enabled).toBe(true);
    expect(body.days_mask).toBe(ALL_DAYS_MASK);
  });

  it('omits id, stable_id and position when creating', () => {
    const body = toPayload(newScene(fullscreen));
    expect(body).not.toHaveProperty('id');
    expect(body).not.toHaveProperty('stable_id');
    // Position 0 lets the server append; sending it would pin the scene first.
    expect(body).not.toHaveProperty('position');
  });

  it('preserves identity and position when editing', () => {
    const draft = newScene(fullscreen);
    draft.id = 12;
    draft.stable_id = 'abc';
    draft.position = 4;
    const body = toPayload(draft);
    expect(body.id).toBe(12);
    expect(body.stable_id).toBe('abc');
    expect(body.position).toBe(4);
  });

  it('serialises zone config as a JSON string', () => {
    const draft = newScene(fullscreen);
    draft.zones[0]!.content_type = 'text';
    draft.zones[0]!.config = defaultConfig('text');
    const zones = (toPayload(draft) as { zones: Array<{ config: string }> }).zones;
    expect(typeof zones[0]!.config).toBe('string');
    expect(JSON.parse(zones[0]!.config).align).toBe('center');
  });

  it('sends an empty style rather than a JSON object full of defaults', () => {
    const zones = (toPayload(newScene(fullscreen)) as { zones: Array<{ style: string }> }).zones;
    expect(zones[0]!.style).toBe('');
  });

  it('sends a style object once something is set', () => {
    const draft = newScene(fullscreen);
    draft.zones[0]!.style = { show_label: true };
    const zones = (toPayload(draft) as { zones: Array<{ style: string }> }).zones;
    expect(JSON.parse(zones[0]!.style).show_label).toBe(true);
  });

  it('omits a blank scheduling window instead of sending empty strings', () => {
    const body = toPayload(newScene(fullscreen));
    expect(body).not.toHaveProperty('active_from');
    expect(body).not.toHaveProperty('active_until');
  });

  it('defaults a new scene above the minimum duration', () => {
    const body = toPayload(newScene(fullscreen)) as { duration_ms: number };
    expect(body.duration_ms).toBeGreaterThanOrEqual(3000);
  });
});

describe('draftFromScene', () => {
  const scene: Scene = {
    id: 3,
    revision_id: 1,
    stable_id: 'sid',
    name: 'Promo',
    position: 2,
    enabled: true,
    layout: 'fullscreen',
    layout_json: '',
    duration_ms: 8000,
    background: '',
    transition: 'fade',
    days_mask: 127,
    valid: true,
    validation_message: '',
    last_error: '',
    created_at: '',
    created_by: '',
    updated_at: '',
    updated_by: '',
    zones: [
      {
        id: 9, scene_id: 3, slot: 'a', rect: { x: 0, y: 0, w: 100, h: 100 }, z: 0,
        content_type: 'image', content_ref: '5',
        config: '{"fit":"cover"}', style: '', label: '',
      },
    ],
  };

  it('round-trips a scene back into the same payload', () => {
    const body = toPayload(draftFromScene(scene)) as {
      id: number; position: number; zones: Array<{ config: string; content_ref: string }>;
    };
    expect(body.id).toBe(3);
    expect(body.position).toBe(2);
    expect(body.zones[0]!.content_ref).toBe('5');
    expect(JSON.parse(body.zones[0]!.config).fit).toBe('cover');
  });

  it('repairs a zero days mask so an old scene stays playable', () => {
    const draft = draftFromScene({ ...scene, days_mask: 0 });
    expect(draft.days_mask).toBe(ALL_DAYS_MASK);
  });
});

describe('datetime round-trip', () => {
  it('survives local input and back', () => {
    const iso = fromLocalInput('2026-08-01T18:30');
    expect(toLocalInput(iso)).toBe('2026-08-01T18:30');
  });

  it('treats a blank value as unset', () => {
    expect(fromLocalInput('')).toBe('');
    expect(toLocalInput('')).toBe('');
  });

  it('does not crash on an unparseable timestamp', () => {
    expect(toLocalInput('not-a-date')).toBe('');
  });
});
