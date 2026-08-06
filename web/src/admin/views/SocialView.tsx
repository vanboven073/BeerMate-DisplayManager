import { useCallback, useEffect, useState } from 'preact/hooks';
import { api, ApiError } from '../../lib/api';
import type { User } from '../../lib/types';
import { Card, EmptyState, ErrorNote, StatusPill, formatWhen, type Tone } from '../ui';

interface Feed {
  id: number;
  name: string;
  platform: string;
  source: string;
  enabled: boolean;
  moderation: 'manual' | 'auto';
  connection_state: string;
  last_refresh_at?: string;
  last_error: string;
}

interface FeedsResponse {
  feeds: Feed[];
  platforms: string[];
  pending_total: number;
}

interface SocialPost {
  id: number;
  author: string;
  text: string;
  media_url: string;
  permalink: string;
  posted_at?: string;
  moderation_state: string;
  pinned: boolean;
}

const STATE_TONE: Record<string, Tone> = {
  ok: 'ok',
  idle: 'idle',
  error: 'bad',
  unauthorised: 'bad',
  rate_limited: 'warn',
};

export function SocialView({ user, refreshKey }: { user: User; refreshKey: number }) {
  const [data, setData] = useState<FeedsResponse | null>(null);
  const [error, setError] = useState('');
  const [openFeed, setOpenFeed] = useState<number | null>(null);
  const [posts, setPosts] = useState<SocialPost[]>([]);
  const [showAdd, setShowAdd] = useState(false);
  const canEdit = user.role === 'editor' || user.role === 'admin';

  const load = useCallback(async (signal?: AbortSignal) => {
    try {
      setData(await api.get<FeedsResponse>('/api/v1/social/feeds', signal));
      setError('');
    } catch (e) {
      if (signal?.aborted) return;
      setError(e instanceof Error ? e.message : 'Could not load feeds');
    }
  }, []);

  useEffect(() => {
    const ctrl = new AbortController();
    void load(ctrl.signal);
    return () => ctrl.abort();
  }, [load, refreshKey]);

  const loadPosts = useCallback(async (feedID: number) => {
    setOpenFeed(feedID);
    try {
      const res = await api.get<{ posts: SocialPost[] }>(
        `/api/v1/social/feeds/${feedID}/posts?state=pending`,
      );
      setPosts(res.posts);
    } catch {
      setPosts([]);
    }
  }, []);

  const moderate = useCallback(
    async (postID: number, state: string) => {
      try {
        await api.post(`/api/v1/social/posts/${postID}`, { state });
        setPosts((p) => p.filter((x) => x.id !== postID));
      } catch (e) {
        setError(e instanceof Error ? e.message : 'Moderation failed');
      }
    },
    [],
  );

  const refresh = useCallback(
    async (feedID: number) => {
      try {
        await api.post(`/api/v1/social/feeds/${feedID}/refresh`);
        await load();
        if (openFeed === feedID) await loadPosts(feedID);
      } catch (e) {
        setError(e instanceof Error ? e.message : 'Refresh failed');
      }
    },
    [load, loadPosts, openFeed],
  );

  return (
    <div class="bm-view">
      <header class="bm-view__head">
        <div>
          <h1 class="bm-view__title">Social feeds</h1>
          <p class="bm-view__sub bm-muted">
            Official feeds only (RSS, Atom, JSON, YouTube, webhook, manual). Posts are held for
            approval by default before they reach the display.
          </p>
        </div>
        {canEdit && (
          <div class="bm-view__actions">
            <button class="bm-btn bm-btn--primary" type="button" onClick={() => setShowAdd((v) => !v)}>
              {showAdd ? 'Cancel' : 'Add feed'}
            </button>
          </div>
        )}
      </header>

      {error && <ErrorNote message={error} />}
      {data && data.pending_total > 0 && (
        <div class="bm-alert bm-alert--warn" role="status">
          {data.pending_total} post{data.pending_total === 1 ? '' : 's'} awaiting moderation.
        </div>
      )}

      {showAdd && canEdit && data && (
        <AddFeedForm platforms={data.platforms} onDone={() => { setShowAdd(false); void load(); }} />
      )}

      {!data ? (
        <p class="bm-muted">Loading…</p>
      ) : data.feeds.length === 0 ? (
        <EmptyState
          title="No feeds yet"
          body="Connect an RSS or Atom feed, a YouTube channel, a JSON endpoint or a webhook. Approve posts here before they appear on screen."
        />
      ) : (
        data.feeds.map((feed) => (
          <Card
            key={feed.id}
            title={feed.name}
            actions={
              canEdit ? (
                <button class="bm-btn bm-btn--ghost bm-btn--sm" type="button" onClick={() => refresh(feed.id)}>
                  Refresh now
                </button>
              ) : undefined
            }
          >
            <div class="bm-row bm-row--gap bm-row--wrap">
              <StatusPill tone={STATE_TONE[feed.connection_state] || 'idle'} label={feed.connection_state} />
              <span class="bm-badge bm-small">{feed.platform}</span>
              <span class="bm-badge bm-small">{feed.moderation} moderation</span>
              <span class="bm-small bm-muted">
                {feed.last_refresh_at ? `refreshed ${formatWhen(feed.last_refresh_at)}` : 'never refreshed'}
              </span>
            </div>
            {feed.last_error && <ErrorNote message={feed.last_error} inline />}

            {canEdit && (
              <div class="bm-row bm-row--gap bm-row--wrap">
                <button class="bm-btn bm-btn--secondary bm-btn--sm" type="button" onClick={() => loadPosts(feed.id)}>
                  Moderate posts
                </button>
                <button class="bm-btn bm-btn--danger bm-btn--sm" type="button"
                  onClick={async () => {
                    if (window.confirm(`Delete feed "${feed.name}"?`)) {
                      try {
                        await api.del(`/api/v1/social/feeds/${feed.id}`);
                        await load();
                      } catch {
                        /* surfaced on next load */
                      }
                    }
                  }}>
                  Delete
                </button>
              </div>
            )}

            {openFeed === feed.id && (
              <div class="bm-modqueue">
                {posts.length === 0 ? (
                  <p class="bm-muted bm-small">No posts awaiting moderation.</p>
                ) : (
                  posts.map((post) => (
                    <div class="bm-modpost" key={post.id}>
                      {post.media_url && (
                        <img class="bm-modpost__media" src={post.media_url} alt="" loading="lazy" />
                      )}
                      <div class="bm-modpost__body">
                        {post.author && <div class="bm-modpost__author">{post.author}</div>}
                        <p class="bm-modpost__text">{post.text}</p>
                        <div class="bm-row bm-row--gap">
                          <button class="bm-btn bm-btn--primary bm-btn--sm" type="button"
                            onClick={() => moderate(post.id, 'approved')}>
                            Approve
                          </button>
                          <button class="bm-btn bm-btn--ghost bm-btn--sm" type="button"
                            onClick={() => moderate(post.id, 'rejected')}>
                            Reject
                          </button>
                        </div>
                      </div>
                    </div>
                  ))
                )}
              </div>
            )}
          </Card>
        ))
      )}
    </div>
  );
}

function AddFeedForm({ platforms, onDone }: { platforms: string[]; onDone: () => void }) {
  const [name, setName] = useState('');
  const [platform, setPlatform] = useState(platforms[0] || 'rss');
  const [source, setSource] = useState('');
  const [token, setToken] = useState('');
  const [moderation, setModeration] = useState<'manual' | 'auto'>('manual');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  const needsSource = platform !== 'manual';

  async function submit(e: Event) {
    e.preventDefault();
    setBusy(true);
    setError('');
    try {
      await api.post('/api/v1/social/feeds', {
        name, platform, source, moderation, enabled: true,
        include_media: true, include_text: true, template: 'cards',
        refresh_sec: 900, max_items: 20,
        token: token || undefined,
      });
      onDone();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Could not add the feed');
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card title="Add a social feed">
      <form class="bm-form" onSubmit={submit} noValidate>
        {error && <ErrorNote message={error} />}
        <div class="bm-field">
          <label class="bm-label" for="sf-name">Name</label>
          <input id="sf-name" class="bm-input" value={name} disabled={busy}
            onInput={(e) => setName((e.target as HTMLInputElement).value)} required />
        </div>
        <div class="bm-field">
          <label class="bm-label" for="sf-platform">Platform</label>
          <select id="sf-platform" class="bm-input" value={platform} disabled={busy}
            onChange={(e) => setPlatform((e.target as HTMLSelectElement).value)}>
            {platforms.map((p) => <option value={p} key={p}>{p}</option>)}
          </select>
        </div>
        {needsSource && (
          <div class="bm-field">
            <label class="bm-label" for="sf-source">
              {platform === 'youtube' ? 'Channel ID or feed URL' : 'Feed URL or source'}
            </label>
            <input id="sf-source" class="bm-input" value={source} disabled={busy}
              onInput={(e) => setSource((e.target as HTMLInputElement).value)} required />
          </div>
        )}
        {platform === 'json' && (
          <div class="bm-field">
            <label class="bm-label" for="sf-token">API token <span class="bm-muted">(optional, stored encrypted)</span></label>
            <input id="sf-token" class="bm-input" type="password" value={token} disabled={busy}
              onInput={(e) => setToken((e.target as HTMLInputElement).value)} autocomplete="off" />
          </div>
        )}
        <div class="bm-field">
          <label class="bm-label" for="sf-mod">Moderation</label>
          <select id="sf-mod" class="bm-input" value={moderation} disabled={busy}
            onChange={(e) => setModeration((e.target as HTMLSelectElement).value as 'manual' | 'auto')}>
            <option value="manual">Manual (recommended) — approve each post</option>
            <option value="auto">Automatic — show posts immediately</option>
          </select>
        </div>
        <button class="bm-btn bm-btn--primary" type="submit" disabled={busy || !name}>
          {busy ? 'Adding…' : 'Add feed'}
        </button>
      </form>
    </Card>
  );
}
