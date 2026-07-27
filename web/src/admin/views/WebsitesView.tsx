import { useCallback, useEffect, useState } from 'preact/hooks';
import { api, ApiError } from '../../lib/api';
import type { User } from '../../lib/types';
import { Card, EmptyState, ErrorNote, StatusPill, formatWhen, type Tone } from '../ui';

interface Website {
  id: number;
  name: string;
  url: string;
  render_mode: 'iframe' | 'managed';
  requires_auth: boolean;
  profile_id: string;
  session_state: string;
  last_ok_at?: string;
  last_validated_at?: string;
  last_login_at?: string;
  last_error: string;
}

const SESSION_TONE: Record<string, Tone> = {
  none: 'idle',
  preparing: 'warn',
  active: 'ok',
  expired: 'bad',
  reauth_required: 'bad',
  error: 'bad',
};

const SESSION_LABEL: Record<string, string> = {
  none: 'No session',
  preparing: 'Login open',
  active: 'Signed in',
  expired: 'Expired',
  reauth_required: 'Reauth needed',
  error: 'Error',
};

export function WebsitesView({ user, refreshKey }: { user: User; refreshKey: number }) {
  const [sites, setSites] = useState<Website[] | null>(null);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [showAdd, setShowAdd] = useState(false);
  const canEdit = user.role === 'editor' || user.role === 'admin';

  const load = useCallback(async (signal?: AbortSignal) => {
    try {
      const res = await api.get<{ websites: Website[] }>('/api/v1/websites', signal);
      setSites(res.websites);
      setError('');
    } catch (e) {
      if (signal?.aborted) return;
      setError(e instanceof Error ? e.message : 'Could not load websites');
    }
  }, []);

  useEffect(() => {
    const ctrl = new AbortController();
    void load(ctrl.signal);
    return () => ctrl.abort();
  }, [load, refreshKey]);

  const act = useCallback(
    async (id: number, action: string, confirmMsg?: string) => {
      if (confirmMsg && !window.confirm(confirmMsg)) return;
      setError('');
      setNotice('');
      try {
        const res = await api.post<{ message?: string; session_state?: string }>(
          `/api/v1/websites/${id}/${action}`,
        );
        if (res.message) setNotice(res.message);
        await load();
      } catch (e) {
        setError(e instanceof ApiError ? e.message : `Could not ${action}`);
      }
    },
    [load],
  );

  const remove = useCallback(
    async (site: Website) => {
      if (!window.confirm(`Delete "${site.name}"?`)) return;
      try {
        await api.del(`/api/v1/websites/${site.id}`);
        await load();
      } catch (e) {
        if (e instanceof ApiError && e.status === 409) {
          if (window.confirm(`${e.message}\n\nDelete anyway?`)) {
            try {
              await api.del(`/api/v1/websites/${site.id}?force=true`);
              await load();
            } catch {
              /* surfaced on next load */
            }
          }
          return;
        }
        setError(e instanceof Error ? e.message : 'Delete failed');
      }
    },
    [load],
  );

  return (
    <div class="bm-view">
      <header class="bm-view__head">
        <div>
          <h1 class="bm-view__title">Websites</h1>
          <p class="bm-view__sub bm-muted">
            Embeddable sites show in an iframe; sites that block framing or need a login are captured
            by a managed browser on the Jetson.
          </p>
        </div>
        {canEdit && (
          <div class="bm-view__actions">
            <button class="bm-btn bm-btn--primary" type="button" onClick={() => setShowAdd((v) => !v)}>
              {showAdd ? 'Cancel' : 'Add website'}
            </button>
          </div>
        )}
      </header>

      {error && <ErrorNote message={error} />}
      {notice && (
        <div class="bm-alert bm-alert--ok" role="status">
          {notice}
        </div>
      )}

      {showAdd && canEdit && <AddWebsiteForm onDone={() => { setShowAdd(false); void load(); }} />}

      {!sites ? (
        <p class="bm-muted">Loading…</p>
      ) : sites.length === 0 ? (
        <EmptyState
          title="No websites yet"
          body="Add a URL to show a live web page or dashboard. For a page that needs a login, mark it as requiring authentication and prepare the login session on the Jetson."
        />
      ) : (
        sites.map((site) => (
          <Card key={site.id} title={site.name}>
            <div class="bm-row bm-row--gap bm-row--wrap">
              <StatusPill
                tone={site.requires_auth ? SESSION_TONE[site.session_state] || 'idle' : 'ok'}
                label={site.requires_auth ? SESSION_LABEL[site.session_state] || site.session_state : 'Public'}
              />
              <span class="bm-badge bm-small">{site.render_mode}</span>
              <a class="bm-small bm-mono" href={site.url} target="_blank" rel="noreferrer noopener">
                {site.url}
              </a>
            </div>

            {site.requires_auth && (
              <dl class="bm-kv">
                <div>
                  <dt>Last signed in</dt>
                  <dd>{site.last_login_at ? formatWhen(site.last_login_at) : 'never'}</dd>
                </div>
                <div>
                  <dt>Last validated</dt>
                  <dd>{site.last_validated_at ? formatWhen(site.last_validated_at) : 'never'}</dd>
                </div>
              </dl>
            )}
            {site.last_error && <ErrorNote message={site.last_error} inline />}

            {canEdit && (
              <div class="bm-row bm-row--gap bm-row--wrap">
                {site.requires_auth && (
                  <>
                    <button class="bm-btn bm-btn--secondary bm-btn--sm" type="button"
                      onClick={() => act(site.id, 'prepare-login')}>
                      Prepare login
                    </button>
                    <button class="bm-btn bm-btn--secondary bm-btn--sm" type="button"
                      onClick={() => act(site.id, 'finish-login')}>
                      Finish login
                    </button>
                    <button class="bm-btn bm-btn--ghost bm-btn--sm" type="button"
                      onClick={() => act(site.id, 'validate')}>
                      Validate
                    </button>
                    <button class="bm-btn bm-btn--ghost bm-btn--sm" type="button"
                      onClick={() => act(site.id, 'clear-session', 'Clear the stored login session?')}>
                      Clear session
                    </button>
                  </>
                )}
                <button class="bm-btn bm-btn--danger bm-btn--sm" type="button" onClick={() => remove(site)}>
                  Delete
                </button>
              </div>
            )}
            {site.requires_auth && (
              <p class="bm-small bm-muted">
                Prepare login opens the site on the Jetson display. Complete the login there (with a
                keyboard and mouse, or over an independently secured remote desktop), then select
                Finish login. The session survives reboots.
              </p>
            )}
          </Card>
        ))
      )}
    </div>
  );
}

function AddWebsiteForm({ onDone }: { onDone: () => void }) {
  const [name, setName] = useState('');
  const [url, setUrl] = useState('https://');
  const [requiresAuth, setRequiresAuth] = useState(false);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  async function submit(e: Event) {
    e.preventDefault();
    setBusy(true);
    setError('');
    try {
      await api.post('/api/v1/websites', {
        name,
        url,
        requires_auth: requiresAuth,
        render_mode: requiresAuth ? 'managed' : 'iframe',
      });
      onDone();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Could not add the website');
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card title="Add a website">
      <form class="bm-form" onSubmit={submit} noValidate>
        {error && <ErrorNote message={error} />}
        <div class="bm-field">
          <label class="bm-label" for="ws-name">Name</label>
          <input id="ws-name" class="bm-input" value={name} disabled={busy}
            onInput={(e) => setName((e.target as HTMLInputElement).value)} required />
        </div>
        <div class="bm-field">
          <label class="bm-label" for="ws-url">URL</label>
          <input id="ws-url" class="bm-input" type="url" value={url} disabled={busy}
            onInput={(e) => setUrl((e.target as HTMLInputElement).value)} required />
        </div>
        <label class="bm-row bm-row--gap">
          <input type="checkbox" checked={requiresAuth} disabled={busy}
            onChange={(e) => setRequiresAuth((e.target as HTMLInputElement).checked)} />
          <span>This site needs a login (uses the managed browser)</span>
        </label>
        <button class="bm-btn bm-btn--primary" type="submit" disabled={busy || !name || url.length < 10}>
          {busy ? 'Adding…' : 'Add website'}
        </button>
      </form>
    </Card>
  );
}
