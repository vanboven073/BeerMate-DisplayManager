import { describe, it, expect } from 'vitest';
import { countdownParts } from './useServerClock';

describe('countdownParts', () => {
  it('breaks a duration into day/hour/minute/second parts', () => {
    const ms = ((2 * 24 + 3) * 3600 + 4 * 60 + 5) * 1000;
    const parts = countdownParts(ms);
    expect(parts).toEqual({ days: 2, hours: 3, minutes: 4, seconds: 5, done: false });
  });

  it('reports done at or past zero', () => {
    expect(countdownParts(0).done).toBe(true);
    expect(countdownParts(-5000).done).toBe(true);
  });

  it('handles sub-minute remainders', () => {
    const parts = countdownParts(45 * 1000);
    expect(parts).toEqual({ days: 0, hours: 0, minutes: 0, seconds: 45, done: false });
  });
});
