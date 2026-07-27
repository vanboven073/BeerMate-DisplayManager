import type { ServerClock } from './useServerClock';
import { formatInZone } from './useServerClock';

/** Shown before the first successful state fetch. */
export function BootScreen() {
  return (
    <div class="bm-player bm-boot" role="status" aria-live="polite">
      <img
        class="bm-boot__mark"
        src="/brand/logo/beermate-mark-light.svg"
        alt=""
        width="120"
        height="87"
      />
      <p class="bm-boot__text">Starting the display&hellip;</p>
    </div>
  );
}

interface StandbyProps {
  clock: ServerClock;
  timezone: string;
  message?: string;
}

/**
 * The standby canvas.
 *
 * Deliberately not a black screen: a dim branded card confirms the system is
 * alive rather than crashed, which is the difference between "we're outside
 * opening hours" and "someone needs to go and look at it".
 */
export function StandbyScreen({ clock, timezone, message }: StandbyProps) {
  const now = clock.now();
  const time = formatInZone(now, timezone, { hour: '2-digit', minute: '2-digit', hour12: false });
  const date = formatInZone(now, timezone, {
    weekday: 'long',
    day: 'numeric',
    month: 'long',
  });

  return (
    <div class="bm-standby" role="status" aria-live="polite">
      <img
        class="bm-standby__mark"
        src="/brand/logo/beermate-wordmark-mono-ivory.svg"
        alt="BeerMate"
        width="360"
      />
      <div class="bm-standby__time" aria-label={`Current time ${time}`}>
        {time}
      </div>
      <div class="bm-standby__date">{date}</div>
      {message && <p class="bm-standby__message">{message}</p>}
    </div>
  );
}

/**
 * The offline indicator.
 *
 * Small and unobtrusive: content keeps playing from cache, so this is
 * information for whoever walks past, not an error state that should dominate
 * the screen.
 */
export function OfflineBadge() {
  return (
    <div class="bm-offline" role="status" aria-live="polite">
      <span class="bm-offline__dot" aria-hidden="true" />
      <span>Offline &mdash; showing saved content</span>
    </div>
  );
}

interface EmergencyProps {
  heading: string;
  body: string;
  severity: 'info' | 'warning' | 'critical';
}

/** A priority message layered above the playlist. */
export function EmergencyOverlay({ heading, body, severity }: EmergencyProps) {
  return (
    <div class={`bm-emergency bm-emergency--${severity}`} role="alert" aria-live="assertive">
      <div class="bm-emergency__inner">
        <div class="bm-emergency__severity">
          {severity === 'critical' ? 'Urgent' : severity === 'warning' ? 'Attention' : 'Notice'}
        </div>
        <h2 class="bm-emergency__heading">{heading}</h2>
        {body && <p class="bm-emergency__body">{body}</p>}
      </div>
    </div>
  );
}

/**
 * The branded fallback shown when a zone's content cannot be rendered.
 *
 * A blank region reads as a broken screen from across a room. A deliberate,
 * branded panel reads as "this bit is temporarily unavailable" and keeps the
 * rest of the scene usable.
 */
export function ZoneFallback({ reason }: { reason: string }) {
  return (
    <div class="bm-fallback" role="status">
      <img
        class="bm-fallback__mark"
        src="/brand/logo/beermate-mark-mono-ivory.svg"
        alt=""
        width="64"
        height="47"
      />
      <p class="bm-fallback__text">{reason}</p>
    </div>
  );
}
