import { useEffect, useState } from 'preact/hooks';
import { api } from '../../lib/api';
import type { Overview } from '../../lib/types';
import { StatusPill, Card, ErrorNote, formatBytes, formatWhen } from '../ui';

/** Dashboard landing page: is the display doing what it should be? */
export function OverviewView({ refreshKey }: { refreshKey: number }) {
  const [data, setData] = useState<Overview | null>(null);
  const [error, setError] = useState('');

  useEffect(() => {
    const ctrl = new AbortController();
    api
      .get<Overview>('/api/v1/overview', ctrl.signal)
      .then((d) => {
        setData(d);
        setError('');
      })
      .catch((e) => {
        if (!ctrl.signal.aborted) setError(e instanceof Error ? e.message : 'Could not load status');
      });

    // A slow poll backs up the event stream: if an event is ever missed, the
    // dashboard is still correct within half a minute.
    const timer = window.setInterval(() => {
      api.get<Overview>('/api/v1/overview').then(setData).catch(() => undefined);
    }, 30_000);

    return () => {
      ctrl.abort();
      window.clearInterval(timer);
    };
  }, [refreshKey]);

  if (error) return <ErrorNote message={error} />;
  if (!data) return <p class="bm-muted">Loading status…</p>;

  return (
    <div class="bm-view">
      <header class="bm-view__head">
        <h1 class="bm-view__title">Overview</h1>
        <p class="bm-view__sub bm-muted">
          {data.timezone} · {formatWhen(data.server_time)}
        </p>
      </header>

      <div class="bm-grid bm-grid--stats">
        <Card title="Player">
          <StatusPill
            tone={data.player.online ? 'ok' : 'bad'}
            label={data.player.online ? 'Online' : 'Offline'}
          />
          <dl class="bm-kv">
            <div>
              <dt>Now showing</dt>
              <dd>{data.player.current_scene || '—'}</dd>
            </div>
            <div>
              <dt>Up next</dt>
              <dd>{data.player.next_scene || '—'}</dd>
            </div>
            <div>
              <dt>Last heartbeat</dt>
              <dd>{data.player.last_heartbeat ? formatWhen(data.player.last_heartbeat) : 'never'}</dd>
            </div>
          </dl>
          {data.player.last_error && <ErrorNote message={data.player.last_error} inline />}
        </Card>

        <Card title="Display">
          <StatusPill
            tone={data.display.on ? 'ok' : 'idle'}
            label={data.display.on ? 'Awake' : 'Standby'}
          />
          <dl class="bm-kv">
            <div>
              <dt>Reason</dt>
              <dd>{data.display.reason}</dd>
            </div>
            <div>
              <dt>Next change</dt>
              <dd>{data.display.next_change ? formatWhen(data.display.next_change) : 'no change scheduled'}</dd>
            </div>
            <div>
              <dt>Driver</dt>
              <dd class="bm-mono">{data.display.driver}</dd>
            </div>
          </dl>
        </Card>

        <Card title="Playlist">
          <StatusPill
            tone={data.playlist.has_unpublished ? 'warn' : 'ok'}
            label={data.playlist.has_unpublished ? 'Unpublished changes' : 'Published'}
          />
          <dl class="bm-kv">
            <div>
              <dt>Live revision</dt>
              <dd>{data.playlist.published_revision ?? 'nothing published yet'}</dd>
            </div>
            <div>
              <dt>Scenes live</dt>
              <dd>{data.playlist.scene_count}</dd>
            </div>
          </dl>
          {data.playlist.has_unpublished && (
            <p class="bm-small bm-muted">
              Draft edits are not on screen until you publish them.{' '}
              <a href="#/playlist">Go to the playlist</a>.
            </p>
          )}
        </Card>

        <Card title="Storage">
          <StatusPill tone={data.storage.low ? 'warn' : 'ok'} label={data.storage.low ? 'Low space' : 'Healthy'} />
          <dl class="bm-kv">
            <div>
              <dt>Media</dt>
              <dd>
                {data.storage.media_count} files · {formatBytes(data.storage.media_bytes)}
              </dd>
            </div>
            <div>
              <dt>Free on disk</dt>
              <dd>{formatBytes(data.storage.free_bytes)}</dd>
            </div>
          </dl>
        </Card>
      </div>

      {data.emergency && (
        <div class="bm-alert bm-alert--error" role="alert">
          <strong>Emergency message live: {data.emergency.heading}</strong>
          {data.emergency.body && <p class="bm-small">{data.emergency.body}</p>}
        </div>
      )}

      <Card title="Recent activity">
        {data.recent_events && data.recent_events.length > 0 ? (
          <ul class="bm-timeline">
            {data.recent_events.map((e) => (
              <li key={e.id}>
                <span class="bm-timeline__when bm-mono bm-small">{formatWhen(e.created_at)}</span>
                <span class="bm-timeline__what">
                  <strong>{e.action.replace(/_/g, ' ')}</strong>
                  {e.actor && <span class="bm-muted"> by {e.actor}</span>}
                  {e.detail && <span class="bm-muted"> — {e.detail}</span>}
                </span>
              </li>
            ))}
          </ul>
        ) : (
          <p class="bm-muted">Nothing recorded yet.</p>
        )}
      </Card>
    </div>
  );
}
