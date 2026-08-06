import { useCallback, useEffect, useMemo, useRef, useState } from 'preact/hooks';

/**
 * A clock synchronised to the server.
 *
 * Countdowns and clock slides must not drift with the Jetson's local clock,
 * which has no RTC battery on the original Nano and can be badly wrong after a
 * cold boot until NTP settles. Every state fetch carries the server time; the
 * offset between that and the browser clock is applied to every reading.
 */
export interface ServerClock {
  /** Current server-corrected time. */
  now(): Date;
  /** Applies a server timestamp to recompute the offset. */
  sync(serverTimeISO: string): void;
  /** Milliseconds the local clock is ahead of the server (negative if behind). */
  offsetMs: number;
  /** Increments once per second so consumers re-render. */
  tick: number;
}

export function useServerClock(): ServerClock {
  const offsetRef = useRef(0);
  const [offsetMs, setOffsetMs] = useState(0);
  const [tick, setTick] = useState(0);

  const sync = useCallback((serverTimeISO: string) => {
    const server = Date.parse(serverTimeISO);
    if (Number.isNaN(server)) return;
    const next = Date.now() - server;
    offsetRef.current = next;
    // Only re-render on a meaningful correction. NTP jitter of a few hundred
    // milliseconds should not push a re-render through the whole scene tree.
    setOffsetMs((prev) => (Math.abs(prev - next) > 500 ? next : prev));
  }, []);

  const now = useCallback(() => new Date(Date.now() - offsetRef.current), []);

  useEffect(() => {
    // A single one-second interval drives every clock and countdown on the page.
    // Per-component timers would multiply with the number of zones and are the
    // classic way a signage page slowly consumes a core.
    const id = window.setInterval(() => setTick((t) => (t + 1) % 86400), 1000);
    return () => window.clearInterval(id);
  }, []);

  // Memoised so the object only changes when a reading actually changes. A fresh
  // object every render leaks into consumers' useCallback/useEffect dependencies;
  // that is how the player ended up refetching state on every render. `now` and
  // `sync` are stable, so depend on those directly when you do not need `tick`.
  return useMemo(() => ({ now, sync, offsetMs, tick }), [now, sync, offsetMs, tick]);
}

/** Formats a duration as countdown parts. */
export function countdownParts(msRemaining: number): {
  days: number;
  hours: number;
  minutes: number;
  seconds: number;
  done: boolean;
} {
  if (msRemaining <= 0) {
    return { days: 0, hours: 0, minutes: 0, seconds: 0, done: true };
  }
  const total = Math.floor(msRemaining / 1000);
  return {
    days: Math.floor(total / 86400),
    hours: Math.floor((total % 86400) / 3600),
    minutes: Math.floor((total % 3600) / 60),
    seconds: total % 60,
    done: false,
  };
}

/**
 * Formats a time in a named IANA zone.
 *
 * Falls back to the browser's local zone if the runtime rejects the identifier,
 * so an unknown timezone degrades to a slightly wrong clock rather than a blank
 * zone on a wall-mounted screen.
 */
export function formatInZone(
  date: Date,
  timezone: string,
  opts: Intl.DateTimeFormatOptions,
): string {
  try {
    return new Intl.DateTimeFormat('en-GB', { ...opts, timeZone: timezone }).format(date);
  } catch {
    return new Intl.DateTimeFormat('en-GB', opts).format(date);
  }
}
