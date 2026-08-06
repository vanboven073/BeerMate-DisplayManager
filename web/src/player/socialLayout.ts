/**
 * Layout planning for a social feed zone.
 *
 * A zone can be any rectangle of any layout, and the operator picks how many
 * posts to show. The old CSS pinned the tiled templates at three columns
 * regardless, so six posts happened to tile neatly and any other number left a
 * stretched row or overflowed the zone. This decides, from the zone's measured
 * size and the post count, how many posts fit on a page and how many columns to
 * use; whatever does not fit is paged through.
 *
 * Pure on purpose: the arithmetic is the part worth testing, and it needs no DOM.
 */

/** Smallest card that still reads from across a room, in CSS pixels. */
const MIN_CARD_W = 240;
const MIN_CARD_H = 190;

/** Ceilings that stop a very large zone from producing unreadable confetti. */
const MAX_COLS = 4;
const MAX_ROWS = 3;

export interface LayoutPlan {
  /** Posts rendered on one page. Always at least 1. */
  perPage: number;
  /** Grid columns for the page. 1 for stacked templates. */
  columns: number;
}

function clamp(n: number, lo: number, hi: number): number {
  return Math.max(lo, Math.min(hi, n));
}

/**
 * planLayout decides the page size and column count for a zone.
 *
 * width/height are the zone's measured content box. A zero or negative size —
 * the first render, before measurement — falls back to a single post, which is
 * always safe to render.
 */
export function planLayout(
  template: string,
  count: number,
  width: number,
  height: number,
): LayoutPlan {
  if (count <= 0) return { perPage: 1, columns: 1 };

  switch (template) {
    // One post at a time, rotating. These templates previously stacked every
    // post into one column, which is not what "single" means.
    case 'single':
    case 'fullscreen':
      return { perPage: 1, columns: 1 };

    // The ticker is a horizontal strip; paging would fight the way it reads.
    case 'ticker':
      return { perPage: count, columns: count };

    // Stacked templates: one column, as many rows as the height affords.
    case 'vertical':
    case 'sidebar':
    case 'latest': {
      const rows = clamp(Math.floor(height / MIN_CARD_H), 1, MAX_ROWS);
      return { perPage: Math.min(count, rows), columns: 1 };
    }
  }

  // Tiled templates: cards, grid, wall.
  const maxCols = clamp(Math.floor(width / MIN_CARD_W), 1, MAX_COLS);
  const maxRows = clamp(Math.floor(height / MIN_CARD_H), 1, MAX_ROWS);
  const perPage = Math.min(count, maxCols * maxRows);

  // Choose the column count that leaves the fewest empty cells, breaking ties
  // towards the shape of the zone. Four posts in a zone wide enough for three
  // columns should be 2x2, not a row of three with one orphan underneath.
  const ideal = height > 0 ? Math.sqrt(perPage * (width / height)) : maxCols;
  let columns = maxCols;
  let best = Number.POSITIVE_INFINITY;
  for (let c = 1; c <= maxCols; c++) {
    const rows = Math.ceil(perPage / c);
    if (rows > maxRows) continue;
    const score = (c * rows - perPage) * 10 + Math.abs(c - ideal);
    if (score < best) {
      best = score;
      columns = c;
    }
  }

  return { perPage, columns };
}

/** pageCount reports how many pages a post count needs. Always at least 1. */
export function pageCount(count: number, perPage: number): number {
  if (count <= 0 || perPage <= 0) return 1;
  return Math.max(1, Math.ceil(count / perPage));
}
