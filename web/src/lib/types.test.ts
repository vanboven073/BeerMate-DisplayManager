import { describe, it, expect } from 'vitest';
import { parseConfig } from './types';

describe('parseConfig', () => {
  it('parses valid JSON config', () => {
    expect(parseConfig('{"fit":"contain"}', { fit: 'cover' })).toEqual({ fit: 'contain' });
  });

  // A zone must never crash the player because its config is malformed; it falls
  // back to a safe default, and the player then renders a branded fallback.
  it('returns the fallback for invalid JSON', () => {
    expect(parseConfig('{not json', { fit: 'cover' })).toEqual({ fit: 'cover' });
  });

  it('returns the fallback for empty input', () => {
    expect(parseConfig('', { a: 1 })).toEqual({ a: 1 });
  });

  it('returns the fallback for a non-object JSON value', () => {
    expect(parseConfig('42', { a: 1 })).toEqual({ a: 1 });
    expect(parseConfig('null', { a: 1 })).toEqual({ a: 1 });
  });
});
