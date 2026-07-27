import { useCallback, useEffect, useRef, useState } from 'preact/hooks';
import { api, ApiError } from '../lib/api';
import type { PlayerState, Scene } from '../lib/types';
import { ZoneRenderer } from './ZoneRenderer';
import { StandbyScreen, OfflineBadge, EmergencyOverlay, BootScreen } from './Chrome';
import { useServerClock } from './useServerClock';

/** How often the player reports status. */
const HEARTBEAT_MS = 10_000;

/**
 * How often full state is re-fetched even when the event stream is healthy.
 *
 * SSE delivers changes promptly, but a missed event on an unattended display
 * would persist until someone noticed. Reconciling on a slow timer bounds that
 * window without meaningful cost.
 */
const RECONCILE_MS = 120_000;

/** localStorage key for the offline playlist cache. */
const CACHE_KEY = 'beermate.player.state.v1';

/**
 * Content types that survive an internet outage because they are served from the
 * Jetson itself. When offline, scenes needing the network are skipped rather
 * than shown as broken.
 */
const OFFLINE_SAFE = new Set([
  'image',
  'video',
  'countdown',
  'clock',
  'text',
  'announcement',
  'image_text',
  'qr',
  'event',
  'empty',
  'fallback',
]);

function loadCache(): PlayerState | null {
  try {
    const raw = window.localStorage.getItem(CACHE_KEY);
    if (!raw) return null;
    const parsed = JSON.parse(raw) as PlayerState;
    if (!parsed || !Array.isArray(parsed.scenes)) return null;
    return parsed;
  } catch {
    return null;
  }
}

function saveCache(state: PlayerState): void {
  try {
    window.localStorage.setItem(CACHE_KEY, JSON.stringify(state));
  } catch {
    // Quota exhaustion must never stop playback; the cache is an optimisation.
  }
}

/** Returns the scenes eligible to play right now. */
function playableScenes(state: PlayerState | null, online: boolean): Scene[] {
  if (!state) return [];
  return state.scenes.filter((sc) => {
    if (!sc.enabled || !sc.valid) return false;
    if (!online) {
      const needsNetwork = sc.zones.some((z) => !OFFLINE_SAFE.has(z.content_type));
      if (needsNetwork) return false;
    }
    return true;
  });
}

export function Player() {
  const [state, setState] = useState<PlayerState | null>(() => loadCache());
  const [index, setIndex] = useState(0);
  const [online, setOnline] = useState(true);
  const [booted, setBooted] = useState(false);
  const [lastError, setLastError] = useState('');

  const clock = useServerClock();

  // Refs mirror state for use inside interval callbacks, so those callbacks
  // never need to be re-created and the timers never churn.
  const stateRef = useRef<PlayerState | null>(state);
  const indexRef = useRef(0);
  const onlineRef = useRef(true);
  stateRef.current = state;
  indexRef.current = index;
  onlineRef.current = online;

  const applyState = useCallback((next: PlayerState) => {
    setState((prev) => {
      // Reset the rotation only when the published revision actually changed.
      // Re-fetching the same revision during reconciliation must not restart the
      // playlist, or a short reconcile interval would freeze the display on the
      // first scene.
      if (!prev || prev.revision !== next.revision) {
        setIndex(0);
      }
      return next;
    });
    saveCache(next);
    clock.sync(next.server_time);
  }, [clock]);

  const fetchState = useCallback(
    async (signal?: AbortSignal) => {
      try {
        const next = await api.player.get<PlayerState>('/api/v1/player/state', signal);
        applyState(next);
        setOnline(true);
        setLastError('');
        setBooted(true);
      } catch (err) {
        if (signal?.aborted) return;
        setOnline(false);
        setBooted(true);
        if (err instanceof ApiError) {
          setLastError(`state fetch failed: ${err.status}`);
        } else {
          setLastError('state fetch failed');
        }
      }
    },
    [applyState],
  );

  // ---- initial load and reconciliation ---------------------------------
  useEffect(() => {
    const ctrl = new AbortController();
    void fetchState(ctrl.signal);

    const timer = window.setInterval(() => {
      void fetchState();
    }, RECONCILE_MS);

    return () => {
      ctrl.abort();
      window.clearInterval(timer);
    };
  }, [fetchState]);

  // ---- event stream ------------------------------------------------------
  useEffect(() => {
    // EventSource cannot set headers, so the player token travels as a query
    // parameter here. It is a loopback request from the Jetson to itself and
    // the URL is never logged with its query string.
    const token = (window as unknown as { __bmPlayerToken?: string }).__bmPlayerToken || '';
    const url = `/api/v1/player/events?token=${encodeURIComponent(token)}`;

    let source: EventSource | null = null;
    try {
      source = new EventSource(url);
    } catch {
      return;
    }

    const refresh = () => void fetchState();

    source.addEventListener('hello', () => setOnline(true));
    source.addEventListener('playlist', refresh);
    source.addEventListener('schedule', refresh);
    source.addEventListener('emergency', refresh);
    source.addEventListener('website', refresh);
    source.addEventListener('social', refresh);
    source.addEventListener('ping', () => setOnline(true));

    source.onopen = () => setOnline(true);
    source.onerror = () => {
      // EventSource reconnects on its own with backoff. Marking offline here
      // makes the state visible immediately; the next successful event or poll
      // clears it.
      setOnline(false);
    };

    return () => {
      source?.close();
    };
  }, [fetchState]);

  // ---- heartbeat ---------------------------------------------------------
  useEffect(() => {
    const beat = () => {
      const st = stateRef.current;
      const scenes = playableScenes(st, onlineRef.current);
      const current = scenes[indexRef.current % Math.max(scenes.length, 1)];
      void api.player
        .post('/api/v1/player/heartbeat', {
          revision_id: st?.revision ?? 0,
          current_scene: current?.stable_id ?? '',
          current_slide: current?.name ?? '',
          storage_state: 'ok',
          last_error: lastError,
          offline: !onlineRef.current,
          played_scene: current?.stable_id ?? '',
        })
        .catch(() => {
          // A failed heartbeat means the backend is unreachable; playback
          // continues from cache and the badge already reflects it.
          setOnline(false);
        });
    };

    beat();
    const timer = window.setInterval(beat, HEARTBEAT_MS);
    return () => window.clearInterval(timer);
  }, [lastError]);

  // ---- rotation ----------------------------------------------------------
  const scenes = playableScenes(state, online);
  const current = scenes.length > 0 ? scenes[index % scenes.length] : undefined;

  useEffect(() => {
    if (!current || scenes.length === 0) return;
    if (!state?.display_on) return;

    const duration = Math.max(current.duration_ms, 3000);
    const timer = window.setTimeout(() => {
      setIndex((i) => (i + 1) % Math.max(scenes.length, 1));
    }, duration);

    // Clearing on every dependency change is what stops timers accumulating
    // across the thousands of transitions this page performs between restarts.
    return () => window.clearTimeout(timer);
  }, [current, scenes.length, state?.display_on]);

  // Keep the index inside bounds when the playlist shrinks.
  useEffect(() => {
    if (scenes.length > 0 && index >= scenes.length) setIndex(0);
  }, [scenes.length, index]);

  // ---- render ------------------------------------------------------------
  if (!booted && !state) {
    return <BootScreen />;
  }

  // Standby blanks the canvas but keeps the page alive, so waking is instant and
  // no browser restart is involved.
  if (state && !state.display_on) {
    return (
      <div class="bm-player">
        <StandbyScreen clock={clock} timezone={state.timezone} />
        {!online && <OfflineBadge />}
      </div>
    );
  }

  return (
    <div class="bm-player" data-testid="player-root">
      {scenes.length === 0 ? (
        <StandbyScreen
          clock={clock}
          timezone={state?.timezone || 'Europe/Amsterdam'}
          message={
            online
              ? 'No scenes are scheduled to play right now.'
              : 'Offline - waiting for the display manager.'
          }
        />
      ) : (
        <SceneStage scene={current} clock={clock} timezone={state?.timezone || 'Europe/Amsterdam'} />
      )}

      {state?.emergency && (
        <EmergencyOverlay
          heading={state.emergency.heading}
          body={state.emergency.body}
          severity={state.emergency.severity}
        />
      )}
      {!online && <OfflineBadge />}
    </div>
  );
}

interface SceneStageProps {
  scene: Scene | undefined;
  clock: ReturnType<typeof useServerClock>;
  timezone: string;
}

/**
 * Renders one scene's zones.
 *
 * Keyed by stable_id so Preact tears down the previous scene's DOM entirely
 * rather than reconciling one video element into another. Reusing nodes across
 * scenes is how detached-DOM and stalled-media leaks accumulate on a page that
 * runs for weeks.
 */
function SceneStage({ scene, clock, timezone }: SceneStageProps) {
  if (!scene) return null;

  const background = scene.background || 'var(--bm-navy)';

  return (
    <div
      key={scene.stable_id}
      class={`bm-scene bm-scene--${scene.transition || 'fade'}`}
      style={{ background }}
      data-scene={scene.stable_id}
    >
      {scene.zones.map((zone) => (
        <div
          key={`${scene.stable_id}:${zone.slot}`}
          class="bm-zone"
          style={{
            left: `${zone.rect.x}%`,
            top: `${zone.rect.y}%`,
            width: `${zone.rect.w}%`,
            height: `${zone.rect.h}%`,
            zIndex: zone.z,
          }}
        >
          <ZoneRenderer zone={zone} clock={clock} timezone={timezone} />
        </div>
      ))}
    </div>
  );
}
