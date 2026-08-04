/**
 * Regression test for a display-freezing bug found on the Jetson.
 *
 * The rotation effect depended on the *object identity* of the current scene.
 * Every state fetch re-parses JSON, so each one produced a new object, cleared
 * the pending dwell timeout and started a fresh one. On the device an SSE event
 * storm drove ~1.5 fetches per second against a 10-second dwell, so the timeout
 * never fired and the playlist sat on scene 0 forever.
 *
 * The test drives that exact shape: refetches triggered repeatedly while a scene
 * is showing, then asserts the playlist still advances on schedule.
 */

import { render, waitFor } from '@testing-library/preact';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';

const listeners: Record<string, Array<() => void>> = {};

class RecordingEventSource {
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  addEventListener(type: string, fn: () => void): void {
    (listeners[type] ||= []).push(fn);
  }
  removeEventListener(): void {}
  close(): void {}
}

function scene(stableID: string, mediaRef: string, durationMs: number) {
  return {
    id: Number(mediaRef),
    revision_id: 1,
    stable_id: stableID,
    name: `scene ${stableID}`,
    position: Number(mediaRef),
    enabled: true,
    layout: 'fullscreen',
    layout_json: '{}',
    duration_ms: durationMs,
    background: '',
    transition: 'none',
    days_mask: 127,
    valid: true,
    validation_message: '',
    zones: [
      {
        id: Number(mediaRef),
        scene_id: Number(mediaRef),
        slot: 'a',
        rect: { x: 0, y: 0, w: 100, h: 100 },
        z: 0,
        content_type: 'image',
        content_ref: mediaRef,
        config: '{}',
        style: '{}',
        label: '',
      },
    ],
  };
}

// A fresh object graph per call, exactly as JSON.parse would produce.
function freshState() {
  return {
    revision: 1,
    display_on: true,
    server_time: new Date().toISOString(),
    timezone: 'Europe/Amsterdam',
    scenes: [scene('sA', '2', 10_000), scene('sB', '3', 10_000)],
  };
}

const playerGet = vi.fn();
const playerPost = vi.fn().mockResolvedValue({});

vi.mock('../lib/api', () => ({
  api: {
    player: {
      get: (...args: unknown[]) => playerGet(...args),
      post: (...args: unknown[]) => playerPost(...args),
    },
  },
  ApiError: class ApiError extends Error {
    status = 0;
  },
  mediaUrl: (id: string | number) => `/media/file/${id}`,
}));

async function importPlayer() {
  return (await import('./Player')).Player;
}

function shownMedia(container: Element): string | null {
  const img = container.querySelector('img');
  return img ? img.getAttribute('src') : null;
}

describe('player rotation', () => {
  beforeEach(() => {
    for (const k of Object.keys(listeners)) delete listeners[k];
    playerGet.mockReset().mockImplementation(() => Promise.resolve(freshState()));
    // @ts-expect-error installing a test double onto the global
    globalThis.EventSource = RecordingEventSource;
    window.localStorage.clear();
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.resetModules();
  });

  it('advances to the next scene after the dwell even while state is refetched', async () => {
    const Player = await importPlayer();
    const { container } = render(<Player />);

    await waitFor(() => expect(shownMedia(container)).toBe('/media/file/2'));

    // Nine seconds of dwell, with a refetch every second — the storm that froze
    // the real display. Each one resolves a brand-new state object.
    for (let i = 0; i < 9; i++) {
      for (const fn of listeners['playlist'] || []) fn();
      await vi.advanceTimersByTimeAsync(1_000);
    }

    // Still on the first scene: only 9s of a 10s dwell have passed.
    expect(shownMedia(container)).toBe('/media/file/2');

    // Crossing 10s must advance, refetches notwithstanding.
    await vi.advanceTimersByTimeAsync(1_500);
    await waitFor(() => expect(shownMedia(container)).toBe('/media/file/3'));
  });

  it('keeps cycling across several scene changes', async () => {
    const Player = await importPlayer();
    const { container } = render(<Player />);

    await waitFor(() => expect(shownMedia(container)).toBe('/media/file/2'));

    await vi.advanceTimersByTimeAsync(10_500);
    await waitFor(() => expect(shownMedia(container)).toBe('/media/file/3'));

    await vi.advanceTimersByTimeAsync(10_500);
    await waitFor(() => expect(shownMedia(container)).toBe('/media/file/2'));
  });
});
