/**
 * The scene editor's draft model and its translation to the API payload.
 *
 * Every default config object here mirrors its Go counterpart in
 * `internal/content/types.go` field for field. The server decodes zone
 * configuration with `DisallowUnknownFields`, so a key that does not exist on
 * the Go struct fails the entire save with a 422 — this module is the single
 * place that contract is expressed on the client, so there is one file to
 * change when a content type gains a field.
 */

import type { ContentType, Layout, Rect, Scene, Slot, Zone, ZoneStyle } from '../../lib/types';
import { parseConfig } from '../../lib/types';

/** Mirrors content.MinDurationMS. */
export const MIN_DURATION_MS = 3000;
/** Mirrors content.AllDaysMask; a mask of 0 is rejected as "no days selected". */
export const ALL_DAYS_MASK = 127;
/** Mirrors content.MaxCustomZones. */
export const MAX_CUSTOM_ZONES = 8;
/** Mirrors content.LayoutCustomGrid. */
export const CUSTOM_GRID = 'custom_grid';
/** Mirrors content.LayoutFullscreen. */
export const FULLSCREEN = 'fullscreen';

export type ZoneConfig = Record<string, unknown>;

export interface DraftZone {
  slot: string;
  label: string;
  content_type: ContentType;
  content_ref: string;
  config: ZoneConfig;
  style: ZoneStyle;
  rect: Rect;
  z: number;
}

export interface DraftScene {
  id: number;
  stable_id: string;
  position: number;
  name: string;
  layout: string;
  duration_ms: number;
  background: string;
  transition: string;
  days_mask: number;
  enabled: boolean;
  active_from: string;
  active_until: string;
  zones: DraftZone[];
}

/* ---- content types ---------------------------------------------------- */

export const CONTENT_LABEL: Record<string, string> = {
  empty: 'Empty',
  image: 'Image',
  video: 'Video',
  website: 'Website',
  countdown: 'Countdown',
  clock: 'Clock',
  kpi: 'KPI',
  qr: 'QR code',
  announcement: 'Announcement',
  image_text: 'Image + text',
  social: 'Social feed',
  text: 'Text',
  ticker: 'Ticker',
  event: 'Event',
  fallback: 'Branded fallback',
};

/**
 * Types offered in the editor, in the order an operator is likely to want them.
 *
 * `fallback` is deliberately absent: it is what the player substitutes when
 * content fails, not something worth scheduling on purpose.
 */
export const SELECTABLE_TYPES: ContentType[] = [
  'image',
  'video',
  'website',
  'text',
  'announcement',
  'image_text',
  'countdown',
  'clock',
  'kpi',
  'qr',
  'social',
  'ticker',
  'event',
  'empty',
];

/** Types whose content_ref points at another record rather than living in config. */
export const REF_TYPES: Record<string, 'media' | 'website'> = {
  image: 'media',
  video: 'media',
  website: 'website',
};

/* ---- time helpers ------------------------------------------------------ */

/** Converts an RFC3339 timestamp to a value a datetime-local input accepts. */
export function toLocalInput(iso: string): string {
  if (!iso) return '';
  const d = new Date(iso);
  if (isNaN(d.getTime())) return '';
  const pad = (n: number) => String(n).padStart(2, '0');
  return (
    `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}` +
    `T${pad(d.getHours())}:${pad(d.getMinutes())}`
  );
}

/** Converts a datetime-local value back to RFC3339 UTC. */
export function fromLocalInput(v: string): string {
  if (!v) return '';
  const d = new Date(v);
  if (isNaN(d.getTime())) return '';
  return d.toISOString();
}

function daysFromNow(n: number): string {
  return new Date(Date.now() + n * 86400000).toISOString();
}

/* ---- per-type defaults ------------------------------------------------- */

/**
 * The starting configuration for a content type.
 *
 * These are chosen so that picking a type and filling in the one obvious field
 * produces a scene that passes validation, rather than one that saves as
 * "invalid" and silently refuses to publish later.
 */
export function defaultConfig(type: ContentType): ZoneConfig {
  switch (type) {
    case 'image':
      return { fit: 'contain', background: '', title: '', subtitle: '', overlay: '', overlay_position: 'bottom' };
    case 'video':
      return { mode: 'loop', muted: true, fit: 'contain' };
    case 'website':
      return { refresh_on_show: false };
    case 'countdown':
      return {
        title: '',
        subtitle: '',
        target: daysFromNow(7),
        timezone: '',
        completion_message: '',
        hide_zero_units: false,
        expire_after_done: false,
      };
    case 'clock':
      return {
        timezone: '',
        show_seconds: false,
        show_date: true,
        title: '',
        subtitle: '',
        twenty_four_hour: true,
      };
    case 'kpi':
      return {
        title: '',
        source: 'manual',
        cards: [{ label: '', value: '', unit: '', trend: '', status: 'neutral', description: '' }],
      };
    case 'qr':
      return {
        heading: '',
        description: '',
        kind: 'url',
        data: '',
        ec_level: 'M',
        with_logo: false,
        foreground: '',
        background: '',
      };
    case 'announcement':
      return {
        heading: '',
        body: '',
        show_logo: true,
        background: '',
        align: 'center',
        text_size: 'l',
        text_color: '',
        cta: '',
        qr_data: '',
      };
    case 'image_text':
      return {
        template: 'image_left',
        heading: '',
        subtitle: '',
        body: '',
        show_logo: false,
        cta: '',
        qr_data: '',
        background: '',
        fit: 'cover',
      };
    case 'social':
      return { feed_id: 0, template: 'cards', max_items: 6, show_meta: true };
    case 'text':
      return { heading: '', body: '', align: 'center', text_size: 'l', text_color: '', background: '' };
    case 'ticker':
      return {
        source: 'manual',
        messages: [''],
        direction: 'left',
        speed_px_sec: 80,
        separator: '  •  ',
        text_size: 'm',
        text_color: '',
        background: '',
        pause_on_priority: true,
      };
    case 'event':
      return {
        title: '',
        subtitle: '',
        starts_at: daysFromNow(7),
        location: '',
        show_logo: true,
        qr_data: '',
        embed_countdown: false,
      };
    default:
      // empty and fallback need no configuration.
      return {};
  }
}

/* ---- zones ------------------------------------------------------------- */

function blankZone(slot: string, label: string, rect: Rect, z: number): DraftZone {
  return {
    slot,
    label,
    content_type: 'empty',
    content_ref: '',
    config: {},
    style: {},
    rect,
    z,
  };
}

const FULL_RECT: Rect = { x: 0, y: 0, w: 100, h: 100 };

/**
 * Rebuilds the zone list for a layout, carrying existing content across.
 *
 * A fixed layout owns its slot IDs and geometry, so switching layout re-maps
 * zones by position: the first zone keeps its content in the first slot of the
 * new layout, and so on. Surplus zones are dropped and missing ones are added
 * empty. Without this, switching layout would leave zones referring to slots the
 * new layout does not define, which the server rejects.
 */
export function zonesForLayout(layout: Layout | undefined, existing: DraftZone[]): DraftZone[] {
  if (!layout) return existing;

  if (layout.custom) {
    if (existing.length > 0) return existing.slice(0, MAX_CUSTOM_ZONES);
    return [blankZone('a', '', { ...FULL_RECT }, 0)];
  }

  const slots: Slot[] = layout.slots || [];
  return slots.map((slot, i) => {
    const prev = existing[i];
    const rect = slot.rect ? { ...slot.rect } : { ...FULL_RECT };
    if (!prev) return blankZone(slot.id, '', rect, i);
    return { ...prev, slot: slot.id, rect, z: i };
  });
}

/** A brand-new scene on the given layout. */
export function newScene(layout: Layout | undefined): DraftScene {
  return {
    id: 0,
    stable_id: '',
    position: 0,
    name: '',
    layout: layout ? layout.id : FULLSCREEN,
    duration_ms: 10000,
    background: '',
    transition: 'fade',
    days_mask: ALL_DAYS_MASK,
    enabled: true,
    active_from: '',
    active_until: '',
    zones: zonesForLayout(layout, []),
  };
}

/** Loads an existing scene into the editable draft shape. */
export function draftFromScene(scene: Scene): DraftScene {
  return {
    id: scene.id,
    stable_id: scene.stable_id,
    position: scene.position,
    name: scene.name,
    layout: scene.layout,
    duration_ms: scene.duration_ms,
    background: scene.background,
    transition: scene.transition,
    days_mask: scene.days_mask || ALL_DAYS_MASK,
    enabled: scene.enabled,
    active_from: scene.active_from || '',
    active_until: scene.active_until || '',
    zones: (scene.zones || []).map((z: Zone, i: number) => ({
      slot: z.slot,
      label: z.label,
      content_type: z.content_type,
      content_ref: z.content_ref,
      config: parseConfig<ZoneConfig>(z.config, {}),
      style: parseConfig<ZoneStyle>(z.style, {}),
      rect: z.rect || { ...FULL_RECT },
      z: typeof z.z === 'number' ? z.z : i,
    })),
  };
}

/** True when a style object carries nothing worth sending. */
function styleIsEmpty(style: ZoneStyle): boolean {
  const keys = Object.keys(style) as Array<keyof ZoneStyle>;
  for (const k of keys) {
    const v = style[k];
    if (v !== '' && v !== 0 && v !== false && v !== undefined && v !== null) return false;
  }
  return true;
}

/**
 * Builds the JSON body for POST /scenes or PUT /scenes/{id}.
 *
 * `enabled`, `days_mask` and `position` are always sent: the Go zero value for
 * each is meaningful and wrong. An unsent `enabled` would save a scene that
 * publish then refuses to count, `days_mask` 0 fails validation outright, and on
 * an update `position` 0 would silently move the scene to the top of the list.
 */
export function toPayload(draft: DraftScene): Record<string, unknown> {
  const body: Record<string, unknown> = {
    name: draft.name,
    layout: draft.layout,
    duration_ms: draft.duration_ms,
    background: draft.background,
    transition: draft.transition,
    days_mask: draft.days_mask,
    enabled: draft.enabled,
    zones: draft.zones.map((z) => ({
      slot: z.slot,
      rect: z.rect,
      z: z.z,
      content_type: z.content_type,
      content_ref: z.content_ref,
      config: JSON.stringify(z.config || {}),
      style: styleIsEmpty(z.style) ? '' : JSON.stringify(z.style),
      label: z.label,
    })),
  };
  if (draft.id > 0) {
    body.id = draft.id;
    body.stable_id = draft.stable_id;
    body.position = draft.position;
  }
  if (draft.active_from) body.active_from = draft.active_from;
  if (draft.active_until) body.active_until = draft.active_until;
  return body;
}
