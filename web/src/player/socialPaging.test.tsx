/**
 * Render tests for social feed paging.
 *
 * The operator's complaint was that changing the post count stopped looking
 * right, and that the zone never moved through posts it could not fit. These
 * assert the paging actually advances, wraps, and leaves a single page alone.
 */

import { render } from '@testing-library/preact';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import type { ServerClock } from './useServerClock';

const playerGet = vi.fn();

vi.mock('../lib/api', () => ({
  api: {
    player: {
      get: (...args: unknown[]) => playerGet(...args),
      post: vi.fn().mockResolvedValue({}),
    },
  },
  ApiError: class ApiError extends Error {},
  mediaUrl: (id: string | number) => `/media/file/${id}`,
}));

const clock: ServerClock = {
  now: () => new Date('2026-08-04T12:00:00Z'),
  sync: () => {},
  offsetMs: 0,
  tick: 0,
};

function post(n: number) {
  return {
    id: n,
    feed_id: 1,
    external_id: `x${n}`,
    author: 'beermate.events',
    author_handle: '',
    avatar_url: '',
    text: `caption ${n}`,
    media_url: '',
    media_kind: '',
    permalink: '',
    posted_at: '2026-07-22T10:29:19Z',
    moderation_state: 'approved',
    pinned: false,
  };
}

function socialZone(maxItems: number) {
  return {
    id: 1,
    scene_id: 1,
    slot: 'a',
    rect: { x: 0, y: 0, w: 100, h: 100 },
    z: 0,
    content_type: 'social',
    content_ref: '',
    config: JSON.stringify({ feed_id: 1, template: 'cards', max_items: maxItems }),
    style: '{}',
    label: '',
  };
}

/** Pretend the zone has been laid out at a given size. */
function stubZoneSize(w: number, h: number) {
  Object.defineProperty(HTMLElement.prototype, 'clientWidth', { configurable: true, value: w });
  Object.defineProperty(HTMLElement.prototype, 'clientHeight', { configurable: true, value: h });
}

async function renderZone(maxItems: number, count: number) {
  playerGet.mockImplementation(() =>
    Promise.resolve({ posts: Array.from({ length: count }, (_, i) => post(i + 1)) }),
  );
  const { ZoneRenderer } = await import('./ZoneRenderer');
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const zone = socialZone(maxItems) as any;
  return render(<ZoneRenderer zone={zone} clock={clock} timezone="Europe/Amsterdam" />);
}

/**
 * Flush mount effects and the pending fetch. Preact defers effects through
 * requestAnimationFrame, which the fake clock also owns, so the clock has to be
 * driven rather than merely awaited.
 */
async function settle() {
  await vi.advanceTimersByTimeAsync(50);
  await vi.advanceTimersByTimeAsync(50);
}

function captions(container: Element): string[] {
  return Array.from(container.querySelectorAll('.bm-social__text')).map((el) =>
    (el.textContent || '').trim(),
  );
}

describe('social paging', () => {
  beforeEach(() => {
    playerGet.mockReset();
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.resetModules();
  });

  // A zone with room for two cards, given five posts: three pages that wrap.
  it('advances through pages and wraps back to the first', async () => {
    stubZoneSize(700, 300);
    const { container } = await renderZone(6, 5);

    await settle();
    expect(captions(container)).toEqual(['caption 1', 'caption 2']);

    await vi.advanceTimersByTimeAsync(8100);
    expect(captions(container)).toEqual(['caption 3', 'caption 4']);

    // The last page is short: it renders one card rather than stretching two.
    await vi.advanceTimersByTimeAsync(8100);
    expect(captions(container)).toEqual(['caption 5']);

    await vi.advanceTimersByTimeAsync(8100);
    expect(captions(container)).toEqual(['caption 1', 'caption 2']);
  });

  it('leaves a single page static', async () => {
    stubZoneSize(1920, 1080);
    const { container } = await renderZone(4, 4);

    await settle();
    expect(captions(container)).toHaveLength(4);
    const before = captions(container);

    await vi.advanceTimersByTimeAsync(30_000);
    expect(captions(container)).toEqual(before);
  });

  // max_items is the operator's cap; posts beyond it are never shown.
  it('never renders more posts than max_items', async () => {
    stubZoneSize(1920, 1080);
    const { container } = await renderZone(3, 10);

    await settle();
    expect(captions(container).length).toBeGreaterThan(0);
    expect(captions(container).length).toBeLessThanOrEqual(3);
  });

  // A refetch must not restart the dwell - the same defect that froze scene
  // rotation earlier.
  it('keeps paging while the feed refetches underneath it', async () => {
    stubZoneSize(700, 300);
    const { container } = await renderZone(6, 5);

    await settle();
    expect(captions(container)).toEqual(['caption 1', 'caption 2']);

    // Step through the dwell in slices: an identity-based dependency would reset
    // the timer on each re-render and the page would never turn.
    for (let i = 0; i < 9; i++) {
      await vi.advanceTimersByTimeAsync(1_000);
    }
    expect(captions(container)).toEqual(['caption 3', 'caption 4']);
  });
});
