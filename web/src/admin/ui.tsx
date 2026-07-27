import type { ComponentChildren } from 'preact';

/* Shared presentational primitives for the admin dashboard. */

export function Card({ title, children, actions }: {
  title?: string;
  children: ComponentChildren;
  actions?: ComponentChildren;
}) {
  return (
    <section class="bm-card">
      {(title || actions) && (
        <header class="bm-card__head">
          {title && <h2 class="bm-card__title">{title}</h2>}
          {actions && <div class="bm-card__actions">{actions}</div>}
        </header>
      )}
      <div class="bm-card__body">{children}</div>
    </section>
  );
}

export type Tone = 'ok' | 'warn' | 'bad' | 'idle' | 'info';

/**
 * A status indicator.
 *
 * Every pill carries a text label as well as its colour, and a distinct glyph,
 * so the state is legible to someone who cannot distinguish the hues.
 */
export function StatusPill({ tone, label }: { tone: Tone; label: string }) {
  const glyph = tone === 'ok' ? '●' : tone === 'warn' ? '▲' : tone === 'bad' ? '■' : '○';
  return (
    <span class={`bm-pill bm-pill--${tone}`}>
      <span aria-hidden="true">{glyph}</span>
      {label}
    </span>
  );
}

export function ErrorNote({ message, inline }: { message: string; inline?: boolean }) {
  return (
    <div class={`bm-alert bm-alert--error${inline ? ' bm-alert--inline' : ''}`} role="alert">
      {message}
    </div>
  );
}

export function EmptyState({ title, body, action }: {
  title: string;
  body: string;
  action?: ComponentChildren;
}) {
  return (
    <div class="bm-empty">
      <img src="/brand/logo/beermate-mark-full.svg" alt="" width="64" height="47" />
      <h3 class="bm-empty__title">{title}</h3>
      <p class="bm-empty__body bm-muted">{body}</p>
      {action}
    </div>
  );
}

export function formatBytes(n: number): string {
  if (!n || n < 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let i = 0;
  let v = n;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v < 10 && i > 0 ? v.toFixed(1) : Math.round(v)} ${units[i]}`;
}

export function formatWhen(iso: string): string {
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return '—';
  const d = new Date(t);
  const diffMs = Date.now() - t;
  const mins = Math.round(diffMs / 60000);

  // Relative for anything recent, absolute beyond an hour: "3 minutes ago" is
  // more useful for a heartbeat, a date is more useful for an audit entry.
  if (Math.abs(mins) < 1) return 'just now';
  if (mins > 0 && mins < 60) return `${mins} min ago`;
  if (mins < 0 && mins > -60) return `in ${Math.abs(mins)} min`;

  return new Intl.DateTimeFormat('en-GB', {
    day: 'numeric',
    month: 'short',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  }).format(d);
}

export function formatDuration(ms: number): string {
  const s = Math.round(ms / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  const rem = s % 60;
  return rem === 0 ? `${m}m` : `${m}m ${rem}s`;
}
