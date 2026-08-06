/**
 * Typed client for the BeerMate Display Manager API.
 *
 * Two authentication modes share one client:
 *   - admin  : session cookie plus a CSRF header on state-changing requests
 *   - player : a bearer-style token injected into the player document
 *
 * The CSRF token is read from a cookie the server sets deliberately without
 * HttpOnly, which is what makes the double-submit pattern work.
 */

export interface Bootstrap {
  player_token: string;
  mode: 'admin' | 'player';
}

/** Reads the server-injected bootstrap block. */
export function readBootstrap(): Bootstrap {
  const el = document.getElementById('bm-bootstrap');
  const fallback: Bootstrap = { player_token: '', mode: 'admin' };
  if (!el || !el.textContent) return fallback;
  const raw = el.textContent.trim();
  // In `vite dev` the Go server is not serving the shell, so the placeholder
  // survives untouched. Treat that as admin mode rather than crashing.
  if (!raw || raw === '__BEERMATE_BOOTSTRAP__') return fallback;
  try {
    const parsed = JSON.parse(raw) as Partial<Bootstrap>;
    return {
      player_token: typeof parsed.player_token === 'string' ? parsed.player_token : '',
      mode: parsed.mode === 'player' ? 'player' : 'admin',
    };
  } catch {
    return fallback;
  }
}

const CSRF_COOKIE = 'beermate_csrf';
const CSRF_HEADER = 'X-BeerMate-CSRF';
const PLAYER_HEADER = 'X-BeerMate-Player';

function readCookie(name: string): string {
  const prefix = name + '=';
  const parts = document.cookie ? document.cookie.split(';') : [];
  for (let i = 0; i < parts.length; i++) {
    const part = (parts[i] || '').trim();
    if (part.indexOf(prefix) === 0) {
      return decodeURIComponent(part.substring(prefix.length));
    }
  }
  return '';
}

/** An API failure carrying the HTTP status and any structured field errors. */
export class ApiError extends Error {
  readonly status: number;
  readonly detail: unknown;

  constructor(status: number, message: string, detail?: unknown) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.detail = detail;
  }

  /** True when the session has expired or was revoked. */
  get isUnauthorised(): boolean {
    return this.status === 401;
  }

  /** Field-level validation errors, when the server supplied them. */
  get fieldErrors(): Array<{ field: string; message: string }> {
    if (Array.isArray(this.detail)) {
      return this.detail as Array<{ field: string; message: string }>;
    }
    return [];
  }
}

export interface RequestOptions {
  method?: string;
  body?: unknown;
  signal?: AbortSignal;
  /** Sends the player token instead of relying on the session cookie. */
  asPlayer?: boolean;
  /** Raw body (FormData) passed through without JSON encoding. */
  raw?: BodyInit;
}

let playerToken = '';

/** Stores the player token for subsequent requests. */
export function setPlayerToken(token: string): void {
  playerToken = token;
}

/**
 * Builds a URL for a media blob served by /media/file/ or /media/thumb/.
 *
 * These URLs end up in <img src> and <video src>, which cannot carry the
 * X-BeerMate-Player header that request() uses, and the player document holds no
 * session cookie. The server therefore also accepts the token as a query
 * parameter; this is the only place that should construct such a URL, so the
 * token is never forgotten again.
 *
 * In the admin app playerToken is empty and the operator's session cookie
 * authorises the request, so the bare path is correct there.
 */
export function mediaUrl(id: string | number, thumb = false): string {
  const base = `/media/${thumb ? 'thumb' : 'file'}/${id}`;
  return playerToken ? `${base}?token=${encodeURIComponent(playerToken)}` : base;
}

/** Performs an API request and decodes the JSON response. */
export async function request<T>(path: string, opts: RequestOptions = {}): Promise<T> {
  const method = opts.method || 'GET';
  const headers: Record<string, string> = { Accept: 'application/json' };

  const stateChanging = method !== 'GET' && method !== 'HEAD' && method !== 'OPTIONS';
  if (stateChanging && !opts.asPlayer) {
    const csrf = readCookie(CSRF_COOKIE);
    if (csrf) headers[CSRF_HEADER] = csrf;
  }
  if (opts.asPlayer && playerToken) {
    headers[PLAYER_HEADER] = playerToken;
  }

  let body: BodyInit | undefined;
  if (opts.raw !== undefined) {
    body = opts.raw;
  } else if (opts.body !== undefined) {
    headers['Content-Type'] = 'application/json';
    body = JSON.stringify(opts.body);
  }

  const init: RequestInit = {
    method,
    headers,
    // Cookies must ride along for admin requests; same-origin is enough and
    // avoids sending them anywhere unexpected.
    credentials: 'same-origin',
  };
  if (body !== undefined) init.body = body;
  if (opts.signal) init.signal = opts.signal;

  const res = await fetch(path, init);

  if (res.status === 204) return undefined as T;

  const text = await res.text();
  let parsed: unknown = null;
  if (text) {
    try {
      parsed = JSON.parse(text);
    } catch {
      parsed = null;
    }
  }

  if (!res.ok) {
    const obj = (parsed || {}) as { error?: string; detail?: unknown };
    throw new ApiError(res.status, obj.error || res.statusText || 'Request failed', obj.detail);
  }
  return parsed as T;
}

export const api = {
  get: <T>(path: string, signal?: AbortSignal) => request<T>(path, signal ? { signal } : {}),
  post: <T>(path: string, body?: unknown) => request<T>(path, { method: 'POST', body }),
  put: <T>(path: string, body?: unknown) => request<T>(path, { method: 'PUT', body }),
  patch: <T>(path: string, body?: unknown) => request<T>(path, { method: 'PATCH', body }),
  del: <T>(path: string) => request<T>(path, { method: 'DELETE' }),
  upload: <T>(path: string, form: FormData) => request<T>(path, { method: 'POST', raw: form }),
  player: {
    get: <T>(path: string, signal?: AbortSignal) =>
      request<T>(path, signal ? { asPlayer: true, signal } : { asPlayer: true }),
    post: <T>(path: string, body?: unknown) =>
      request<T>(path, { method: 'POST', body, asPlayer: true }),
  },
};
