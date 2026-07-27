/** Shared types mirroring the Go API payloads. */

export interface Rect {
  x: number;
  y: number;
  w: number;
  h: number;
}

export type ContentType =
  | 'empty'
  | 'image'
  | 'video'
  | 'website'
  | 'countdown'
  | 'clock'
  | 'kpi'
  | 'qr'
  | 'announcement'
  | 'image_text'
  | 'social'
  | 'text'
  | 'ticker'
  | 'event'
  | 'fallback';

export interface Zone {
  id: number;
  scene_id: number;
  slot: string;
  rect: Rect;
  z: number;
  content_type: ContentType;
  content_ref: string;
  /** Per-type configuration, JSON encoded. */
  config: string;
  /** Presentational wrapper settings, JSON encoded. */
  style: string;
  label: string;
}

export interface Scene {
  id: number;
  revision_id: number;
  stable_id: string;
  name: string;
  position: number;
  enabled: boolean;
  layout: string;
  layout_json: string;
  duration_ms: number;
  background: string;
  transition: string;
  active_from?: string;
  active_until?: string;
  days_mask: number;
  valid: boolean;
  validation_message: string;
  last_played_at?: string;
  last_error: string;
  created_at: string;
  created_by: string;
  updated_at: string;
  updated_by: string;
  zones: Zone[];
}

export interface Slot {
  id: string;
  label: string;
  rect: Rect;
  hint?: string[];
}

export interface Layout {
  id: string;
  name: string;
  description: string;
  slots: Slot[] | null;
  custom: boolean;
}

export interface Revision {
  id: number;
  is_draft: boolean;
  published_at?: string;
  created_at: string;
  created_by: string;
  published_by: string;
  note: string;
  keep: boolean;
  scene_count: number;
}

export interface MediaItem {
  id: number;
  kind: 'image' | 'video' | 'pdf' | 'pdf_page';
  original_name: string;
  mime: string;
  bytes: number;
  width: number;
  height: number;
  duration_ms: number;
  sha256: string;
  has_thumbnail: boolean;
  poster_id?: number;
  parent_id?: number;
  page_number?: number;
  warning?: string;
  created_at: string;
  created_by: string;
}

export interface User {
  id: number;
  username: string;
  display_name: string;
  role: 'viewer' | 'editor' | 'admin';
  disabled: boolean;
  must_change_password: boolean;
  last_login_at?: string;
  created_at: string;
}

export interface ScheduleRule {
  id: number;
  kind: 'weekly' | 'date';
  weekday?: number;
  on_date?: string;
  enabled: boolean;
  on_time?: string;
  off_time?: string;
  label?: string;
}

export interface ScheduleOverride {
  id: number;
  mode: 'wake' | 'sleep';
  starts_at: string;
  expires_at: string;
  created_by: string;
}

export interface ScheduleDecision {
  on: boolean;
  reason: string;
  source: string;
  next_change?: string;
}

export interface EmergencyMessage {
  id: number;
  heading: string;
  body: string;
  severity: 'info' | 'warning' | 'critical';
  starts_at: string;
  expires_at: string;
  dismissed_at?: string;
  created_by: string;
  created_at: string;
}

/** The payload the player polls and receives over SSE. */
export interface PlayerState {
  revision: number;
  scenes: Scene[];
  display_on: boolean;
  server_time: string;
  timezone: string;
  emergency?: {
    heading: string;
    body: string;
    severity: 'info' | 'warning' | 'critical';
    expires_at: string;
  };
}

export interface Overview {
  player: {
    online: boolean;
    last_heartbeat?: string;
    current_scene: string;
    next_scene: string;
    current_slide: string;
    browser: string;
    last_error: string;
    revision: number;
  };
  display: {
    on: boolean;
    reason: string;
    source: string;
    driver: string;
    next_change?: string;
  };
  playlist: {
    draft_revision: number;
    published_revision: number | null;
    scene_count: number;
    has_unpublished: boolean;
  };
  storage: {
    media_bytes: number;
    media_count: number;
    free_bytes: number;
    low: boolean;
  };
  subscribers: { players: number; admins: number };
  server_time: string;
  timezone: string;
  emergency?: EmergencyMessage;
  recent_events: AuditEntry[];
}

export interface AuditEntry {
  id: number;
  created_at: string;
  actor: string;
  action: string;
  target_type?: string;
  target_id?: string;
  detail?: string;
  ip?: string;
}

/* ---- per-type zone configuration ------------------------------------- */

export interface ImageConfig {
  fit?: 'contain' | 'cover' | 'stretch';
  background?: string;
  title?: string;
  subtitle?: string;
  overlay?: string;
  overlay_position?: 'top' | 'center' | 'bottom';
}

export interface VideoConfig {
  mode?: 'once' | 'loop' | 'until_complete' | 'duration';
  muted?: boolean;
  poster_id?: number;
  fit?: 'contain' | 'cover' | 'stretch';
}

export interface CountdownConfig {
  title: string;
  subtitle?: string;
  target: string;
  timezone: string;
  completion_message?: string;
  completion_image_id?: number;
  hide_zero_units?: boolean;
  expire_after_done?: boolean;
  variant?: string;
}

export interface ClockConfig {
  timezone?: string;
  show_seconds?: boolean;
  show_date?: boolean;
  title?: string;
  subtitle?: string;
  variant?: string;
  twenty_four_hour?: boolean;
}

export interface KPICard {
  label: string;
  value: string;
  unit?: string;
  target?: string;
  trend?: 'up' | 'down' | 'flat';
  status?: 'good' | 'warn' | 'bad' | 'neutral';
  icon?: string;
  description?: string;
}

export interface KPIConfig {
  title?: string;
  cards: KPICard[];
  source: 'manual' | 'api';
  url?: string;
  refresh_sec?: number;
  fallback_message?: string;
}

export interface QRConfig {
  heading?: string;
  description?: string;
  kind: 'url' | 'text' | 'contact' | 'wifi';
  data: string;
  ec_level?: 'L' | 'M' | 'Q' | 'H';
  with_logo?: boolean;
  foreground?: string;
  background?: string;
}

export interface AnnouncementConfig {
  heading: string;
  body?: string;
  image_id?: number;
  show_logo?: boolean;
  background?: string;
  align?: 'left' | 'center' | 'right';
  text_size?: 's' | 'm' | 'l' | 'xl';
  text_color?: string;
  cta?: string;
  qr_data?: string;
}

export interface TextConfig {
  heading?: string;
  body?: string;
  align?: 'left' | 'center' | 'right';
  text_size?: string;
  text_color?: string;
  background?: string;
}

export interface TickerConfig {
  source: 'manual' | 'feed' | 'emergency';
  messages?: string[];
  feed_id?: number;
  direction?: 'left' | 'right';
  speed_px_sec?: number;
  separator?: string;
  text_size?: string;
  text_color?: string;
  background?: string;
  pause_on_priority?: boolean;
}

export interface EventConfig {
  title: string;
  subtitle?: string;
  starts_at?: string;
  location?: string;
  image_id?: number;
  show_logo?: boolean;
  qr_data?: string;
  embed_countdown?: boolean;
  variant?: string;
}

export interface ZoneStyle {
  background?: string;
  padding?: number;
  border_width?: number;
  border_color?: string;
  border_radius?: number;
  overflow?: 'hidden' | 'visible' | 'scroll';
  fit?: 'contain' | 'cover' | 'stretch';
  show_label?: boolean;
}

/** Parses a zone's JSON config, returning a default on any failure. */
export function parseConfig<T>(raw: string, fallback: T): T {
  if (!raw) return fallback;
  try {
    const parsed = JSON.parse(raw);
    if (parsed && typeof parsed === 'object') return parsed as T;
    return fallback;
  } catch {
    return fallback;
  }
}
