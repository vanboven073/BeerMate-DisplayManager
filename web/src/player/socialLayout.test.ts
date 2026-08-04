import { describe, it, expect } from 'vitest';
import { planLayout, pageCount } from './socialLayout';

// A 16:9 zone filling a 1080p panel, and some realistic sub-zones from the
// split-screen layouts.
const FULL = { w: 1920, h: 1080 };
const HALF_WIDE = { w: 1920, h: 540 };
const SIDEBAR = { w: 420, h: 1080 };
const SMALL = { w: 300, h: 220 };

describe('planLayout: tiled templates', () => {
  // The reported bug: cards were pinned at three columns, so six tiled neatly
  // and any other count left a stretched row or overflowed the zone.
  it('tiles six posts as 3x2 in a full-screen zone', () => {
    const plan = planLayout('cards', 6, FULL.w, FULL.h);
    expect(plan).toEqual({ perPage: 6, columns: 3 });
  });

  // Four posts across three columns would leave an orphan on the second row.
  it('prefers 2x2 over 3+1 for four posts', () => {
    expect(planLayout('cards', 4, FULL.w, FULL.h).columns).toBe(2);
  });

  it('uses as many columns as there are posts when they fit in one row', () => {
    expect(planLayout('cards', 2, FULL.w, FULL.h)).toEqual({ perPage: 2, columns: 2 });
    expect(planLayout('cards', 1, FULL.w, FULL.h)).toEqual({ perPage: 1, columns: 1 });
  });

  it('pages when more posts are configured than fit', () => {
    const plan = planLayout('cards', 20, FULL.w, FULL.h);
    expect(plan.perPage).toBeLessThan(20);
    expect(pageCount(20, plan.perPage)).toBeGreaterThan(1);
  });

  // A short wide strip has room for one row, so it must not try to stack.
  it('uses a single row in a short wide zone', () => {
    const plan = planLayout('cards', 6, 1920, 200);
    expect(Math.ceil(plan.perPage / plan.columns)).toBe(1);
  });

  it('uses a single column in a narrow zone', () => {
    expect(planLayout('cards', 6, SIDEBAR.w, SIDEBAR.h).columns).toBe(1);
  });

  it('never exceeds the post count or the readable ceilings', () => {
    const plan = planLayout('wall', 100, FULL.w, FULL.h);
    expect(plan.perPage).toBeLessThanOrEqual(12); // MAX_COLS * MAX_ROWS
    expect(plan.columns).toBeLessThanOrEqual(4);
  });

  it('still yields a renderable page in a zone too small for the minimum card', () => {
    const plan = planLayout('cards', 6, SMALL.w, SMALL.h);
    expect(plan.perPage).toBeGreaterThanOrEqual(1);
    expect(plan.columns).toBeGreaterThanOrEqual(1);
  });

  // The first render happens before the zone has been measured.
  it('degrades safely when the zone has not been measured yet', () => {
    expect(planLayout('cards', 6, 0, 0)).toEqual({ perPage: 1, columns: 1 });
  });
});

describe('planLayout: other templates', () => {
  // These previously stacked every post into one column, which is not what
  // "single" means.
  it('shows exactly one post for single and fullscreen', () => {
    expect(planLayout('single', 6, FULL.w, FULL.h)).toEqual({ perPage: 1, columns: 1 });
    expect(planLayout('fullscreen', 6, FULL.w, FULL.h)).toEqual({ perPage: 1, columns: 1 });
  });

  it('stacks the column templates one across', () => {
    const plan = planLayout('vertical', 6, SIDEBAR.w, SIDEBAR.h);
    expect(plan.columns).toBe(1);
    expect(plan.perPage).toBeGreaterThan(1);
  });

  it('fits fewer stacked posts in a shallow zone', () => {
    const tall = planLayout('sidebar', 6, SIDEBAR.w, 1080).perPage;
    const shallow = planLayout('sidebar', 6, SIDEBAR.w, 300).perPage;
    expect(shallow).toBeLessThan(tall);
  });

  // The ticker is a horizontal strip; paging would fight how it reads.
  it('does not page the ticker', () => {
    expect(planLayout('ticker', 9, HALF_WIDE.w, 160)).toEqual({ perPage: 9, columns: 9 });
  });

  it('treats an unknown template as tiled', () => {
    expect(planLayout('something-new', 6, FULL.w, FULL.h).perPage).toBe(6);
  });
});

describe('pageCount', () => {
  it('counts pages, rounding up', () => {
    expect(pageCount(6, 6)).toBe(1);
    expect(pageCount(7, 6)).toBe(2);
    expect(pageCount(12, 6)).toBe(2);
    expect(pageCount(13, 6)).toBe(3);
  });

  it('never reports zero pages', () => {
    expect(pageCount(0, 6)).toBe(1);
    expect(pageCount(6, 0)).toBe(1);
  });
});
