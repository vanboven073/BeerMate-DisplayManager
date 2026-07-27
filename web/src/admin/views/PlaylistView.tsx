import { useCallback, useEffect, useState } from 'preact/hooks';
import { api, ApiError } from '../../lib/api';
import type { Revision, Scene, User } from '../../lib/types';
import { Card, EmptyState, ErrorNote, StatusPill, formatDuration, formatWhen } from '../ui';

interface PlaylistResponse {
  revision: Revision;
  scenes: Scene[];
}

const CONTENT_LABEL: Record<string, string> = {
  image: 'Image',
  video: 'Video',
  website: 'Website',
  countdown: 'Countdown',
  clock: 'Clock',
  kpi: 'KPI',
  qr: 'QR code',
  announcement: 'Announcement',
  image_text: 'Image + text',
  social: 'Social feed',
  text: 'Text',
  ticker: 'Ticker',
  event: 'Event',
  empty: 'Empty',
  fallback: 'Fallback',
};

export function PlaylistView({ user, refreshKey }: { user: User; refreshKey: number }) {
  const [data, setData] = useState<PlaylistResponse | null>(null);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState('');

  const canEdit = user.role === 'editor' || user.role === 'admin';

  const load = useCallback(async (signal?: AbortSignal) => {
    try {
      const res = await api.get<PlaylistResponse>('/api/v1/playlist', signal);
      setData(res);
      setError('');
    } catch (e) {
      if (signal?.aborted) return;
      setError(e instanceof Error ? e.message : 'Could not load the playlist');
    }
  }, []);

  useEffect(() => {
    const ctrl = new AbortController();
    void load(ctrl.signal);
    return () => ctrl.abort();
  }, [load, refreshKey]);

  const move = useCallback(
    async (from: number, to: number) => {
      if (!data || !canEdit) return;
      const ids = data.scenes.map((s) => s.id);
      if (to < 0 || to >= ids.length) return;
      const moved = ids.splice(from, 1)[0];
      if (moved === undefined) return;
      ids.splice(to, 0, moved);

      // Optimistic reorder keeps the list responsive; a failure reloads truth.
      const reordered = ids
        .map((id) => data.scenes.find((s) => s.id === id))
        .filter((s): s is Scene => Boolean(s));
      setData({ ...data, scenes: reordered });

      try {
        await api.post('/api/v1/playlist/reorder', { scene_ids: ids });
      } catch (e) {
        setError(e instanceof Error ? e.message : 'Reorder failed');
        void load();
      }
    },
    [data, canEdit, load],
  );

  const toggle = useCallback(
    async (scene: Scene) => {
      if (!canEdit) return;
      try {
        await api.post('/api/v1/playlist/bulk', {
          scene_ids: [scene.id],
          action: scene.enabled ? 'disable' : 'enable',
        });
        await load();
      } catch (e) {
        setError(e instanceof Error ? e.message : 'Could not change the scene');
      }
    },
    [canEdit, load],
  );

  const duplicate = useCallback(
    async (scene: Scene) => {
      try {
        await api.post(`/api/v1/scenes/${scene.id}/duplicate`);
        await load();
      } catch (e) {
        setError(e instanceof Error ? e.message : 'Could not duplicate the scene');
      }
    },
    [load],
  );

  const remove = useCallback(
    async (scene: Scene) => {
      if (!window.confirm(`Delete "${scene.name}"? This cannot be undone.`)) return;
      try {
        await api.del(`/api/v1/scenes/${scene.id}`);
        await load();
      } catch (e) {
        setError(e instanceof Error ? e.message : 'Could not delete the scene');
      }
    },
    [load],
  );

  const publish = useCallback(async () => {
    setBusy(true);
    setError('');
    setNotice('');
    try {
      const res = await api.post<{ published_revision: number; scene_count: number }>(
        '/api/v1/playlist/publish',
        { note: '' },
      );
      setNotice(`Published revision ${res.published_revision} with ${res.scene_count} scene(s).`);
      await load();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'Publish failed');
    } finally {
      setBusy(false);
    }
  }, [load]);

  if (error && !data) return <ErrorNote message={error} />;
  if (!data) return <p class="bm-muted">Loading playlist…</p>;

  const invalidCount = data.scenes.filter((s) => s.enabled && !s.valid).length;

  return (
    <div class="bm-view">
      <header class="bm-view__head">
        <div>
          <h1 class="bm-view__title">Playlist</h1>
          <p class="bm-view__sub bm-muted">
            Draft revision {data.revision.id} · {data.scenes.length} scene
            {data.scenes.length === 1 ? '' : 's'}
          </p>
        </div>
        {canEdit && (
          <div class="bm-view__actions">
            <button
              class="bm-btn bm-btn--primary"
              type="button"
              onClick={publish}
              disabled={busy || data.scenes.length === 0}
            >
              {busy ? 'Publishing…' : 'Publish to screen'}
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
      {invalidCount > 0 && (
        <div class="bm-alert bm-alert--warn" role="status">
          {invalidCount} enabled scene{invalidCount === 1 ? ' has' : 's have'} validation errors and
          must be fixed or disabled before publishing.
        </div>
      )}

      {data.scenes.length === 0 ? (
        <EmptyState
          title="No scenes yet"
          body="A scene is one full screen. Add images, a countdown, a dashboard or a split-screen composition, then publish to put it on the display."
        />
      ) : (
        <Card>
          <ol class="bm-scenes" role="list">
            {data.scenes.map((scene, i) => (
              <li class={`bm-scene-row${scene.enabled ? '' : ' is-disabled'}`} key={scene.id}>
                <div class="bm-scene-row__order">
                  {/* Buttons rather than drag-only: reordering must work from the
                      keyboard and on a touch screen, not just with a mouse. */}
                  <button
                    class="bm-iconbtn"
                    type="button"
                    onClick={() => move(i, i - 1)}
                    disabled={i === 0 || !canEdit}
                    aria-label={`Move ${scene.name} up`}
                  >
                    ↑
                  </button>
                  <span class="bm-scene-row__pos bm-mono">{i + 1}</span>
                  <button
                    class="bm-iconbtn"
                    type="button"
                    onClick={() => move(i, i + 1)}
                    disabled={i === data.scenes.length - 1 || !canEdit}
                    aria-label={`Move ${scene.name} down`}
                  >
                    ↓
                  </button>
                </div>

                <div class="bm-scene-row__main">
                  <div class="bm-scene-row__name">{scene.name}</div>
                  <div class="bm-scene-row__meta bm-small bm-muted">
                    <span>{scene.layout.replace(/_/g, ' ')}</span>
                    <span>·</span>
                    <span>{formatDuration(scene.duration_ms)}</span>
                    <span>·</span>
                    <span>
                      {scene.zones.map((z) => CONTENT_LABEL[z.content_type] || z.content_type).join(', ')}
                    </span>
                    {scene.last_played_at && (
                      <>
                        <span>·</span>
                        <span>last played {formatWhen(scene.last_played_at)}</span>
                      </>
                    )}
                  </div>
                  {!scene.valid && scene.validation_message && (
                    <p class="bm-scene-row__error bm-small">{scene.validation_message}</p>
                  )}
                </div>

                <div class="bm-scene-row__status">
                  {scene.valid ? (
                    <StatusPill tone={scene.enabled ? 'ok' : 'idle'} label={scene.enabled ? 'Enabled' : 'Disabled'} />
                  ) : (
                    <StatusPill tone="bad" label="Invalid" />
                  )}
                </div>

                {canEdit && (
                  <div class="bm-scene-row__actions">
                    <button class="bm-btn bm-btn--ghost bm-btn--sm" type="button" onClick={() => toggle(scene)}>
                      {scene.enabled ? 'Disable' : 'Enable'}
                    </button>
                    <button class="bm-btn bm-btn--ghost bm-btn--sm" type="button" onClick={() => duplicate(scene)}>
                      Duplicate
                    </button>
                    <button class="bm-btn bm-btn--danger bm-btn--sm" type="button" onClick={() => remove(scene)}>
                      Delete
                    </button>
                  </div>
                )}
              </li>
            ))}
          </ol>
        </Card>
      )}
    </div>
  );
}
