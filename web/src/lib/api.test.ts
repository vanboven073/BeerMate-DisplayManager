import { describe, it, expect, afterEach } from 'vitest';
import { mediaUrl, setPlayerToken } from './api';

// Regression cover for a bug that reached a real display: every media URL in the
// player was built as a bare /media/file/<id>. That path is authorised by a
// player-token header, a ?token= query parameter, or an admin session cookie —
// and an <img>/<video> element can send none of the first, while the player
// document holds no cookie. Result: every image 401'd and the player showed
// "Image unavailable". It stayed invisible until the first playlist was
// published, because until then no image zone had ever rendered.
describe('mediaUrl', () => {
  afterEach(() => setPlayerToken(''));

  it('appends the player token so <img> and <video> can authenticate', () => {
    setPlayerToken('tok123');
    expect(mediaUrl(7)).toBe('/media/file/7?token=tok123');
  });

  it('builds thumbnail URLs from the same seam', () => {
    setPlayerToken('tok123');
    expect(mediaUrl(7, true)).toBe('/media/thumb/7?token=tok123');
  });

  // In the admin app there is no player token and the operator's session cookie
  // authorises the request, so a bare path is correct — and appending an empty
  // token would be worse than useless, since the server compares it verbatim.
  it('omits the query entirely when there is no token', () => {
    expect(mediaUrl(7)).toBe('/media/file/7');
    expect(mediaUrl('7', true)).toBe('/media/thumb/7');
  });

  it('escapes tokens containing URL-significant characters', () => {
    setPlayerToken('a+b/c=d&e');
    expect(mediaUrl(1)).toBe('/media/file/1?token=a%2Bb%2Fc%3Dd%26e');
  });
});
