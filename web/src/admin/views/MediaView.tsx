import { useCallback, useEffect, useRef, useState } from 'preact/hooks';
import { api, ApiError } from '../../lib/api';
import type { MediaItem, User } from '../../lib/types';
import { Card, EmptyState, ErrorNote, formatBytes, formatWhen } from '../ui';

interface MediaResponse {
  media: MediaItem[];
  total_bytes: number;
  total_count: number;
}

export function MediaView({ user, refreshKey }: { user: User; refreshKey: number }) {
  const [data, setData] = useState<MediaResponse | null>(null);
  const [error, setError] = useState('');
  const [uploading, setUploading] = useState(false);
  const fileInput = useRef<HTMLInputElement | null>(null);

  const canEdit = user.role === 'editor' || user.role === 'admin';

  const load = useCallback(async (signal?: AbortSignal) => {
    try {
      setData(await api.get<MediaResponse>('/api/v1/media', signal));
      setError('');
    } catch (e) {
      if (signal?.aborted) return;
      setError(e instanceof Error ? e.message : 'Could not load the media library');
    }
  }, []);

  useEffect(() => {
    const ctrl = new AbortController();
    void load(ctrl.signal);
    return () => ctrl.abort();
  }, [load, refreshKey]);

  const upload = useCallback(
    async (files: FileList | null) => {
      if (!files || files.length === 0) return;
      setUploading(true);
      setError('');
      try {
        // One request per file so a single rejected upload does not discard the
        // whole batch, and the operator sees exactly which file failed.
        for (let i = 0; i < files.length; i++) {
          const file = files.item(i);
          if (!file) continue;
          const form = new FormData();
          form.append('file', file);
          await api.upload('/api/v1/media', form);
        }
        await load();
      } catch (e) {
        setError(e instanceof ApiError ? e.message : 'Upload failed');
      } finally {
        setUploading(false);
        if (fileInput.current) fileInput.current.value = '';
      }
    },
    [load],
  );

  const remove = useCallback(
    async (item: MediaItem) => {
      if (!window.confirm(`Delete "${item.original_name || 'this file'}"?`)) return;
      try {
        await api.del(`/api/v1/media/${item.id}`);
        await load();
      } catch (e) {
        if (e instanceof ApiError && e.status === 409) {
          // The server refuses to orphan a scene's content. Deleting anyway is a
          // deliberate choice, so it needs an explicit second confirmation.
          if (window.confirm(`${e.message}\n\nDelete it anyway?`)) {
            try {
              await api.del(`/api/v1/media/${item.id}?force=true`);
              await load();
              return;
            } catch (e2) {
              setError(e2 instanceof Error ? e2.message : 'Delete failed');
              return;
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
          <h1 class="bm-view__title">Media</h1>
          {data && (
            <p class="bm-view__sub bm-muted">
              {data.total_count} file{data.total_count === 1 ? '' : 's'} ·{' '}
              {formatBytes(data.total_bytes)}
            </p>
          )}
        </div>
        {canEdit && (
          <div class="bm-view__actions">
            <label class="bm-btn bm-btn--primary" for="media-upload">
              {uploading ? 'Uploading…' : 'Upload files'}
            </label>
            <input
              id="media-upload"
              class="bm-visually-hidden"
              ref={fileInput}
              type="file"
              multiple
              accept="image/jpeg,image/png,image/webp,image/gif,video/mp4,video/webm,application/pdf"
              disabled={uploading}
              onChange={(e) => upload((e.target as HTMLInputElement).files)}
            />
          </div>
        )}
      </header>

      {error && <ErrorNote message={error} />}

      <p class="bm-small bm-muted">
        Accepted: JPG, PNG, WebP, GIF, MP4 (H.264), WebM and PDF. SVG is not accepted because it can
        carry scripts — export as PNG instead.
      </p>

      {!data ? (
        <p class="bm-muted">Loading…</p>
      ) : data.media.length === 0 ? (
        <EmptyState
          title="Nothing uploaded yet"
          body="Upload the images, videos or PDFs you want on the display. Files are stored on the Jetson and served locally, so they keep playing even without internet."
        />
      ) : (
        <Card>
          <ul class="bm-media-grid" role="list">
            {data.media.map((item) => (
              <li class="bm-media" key={item.id}>
                <div class="bm-media__thumb">
                  {item.has_thumbnail ? (
                    <img src={`/media/thumb/${item.id}`} alt="" loading="lazy" />
                  ) : (
                    <div class="bm-media__placeholder" aria-hidden="true">
                      {item.kind === 'video' ? '▶' : item.kind === 'pdf' ? 'PDF' : '◻'}
                    </div>
                  )}
                </div>
                <div class="bm-media__body">
                  <div class="bm-media__name" title={item.original_name}>
                    {item.original_name || `Untitled ${item.kind}`}
                  </div>
                  <div class="bm-media__meta bm-small bm-muted">
                    {item.width > 0 && (
                      <>
                        {item.width}×{item.height} ·{' '}
                      </>
                    )}
                    {formatBytes(item.bytes)} · {formatWhen(item.created_at)}
                  </div>
                  {item.warning && <div class="bm-media__warn bm-small">{item.warning}</div>}
                </div>
                {canEdit && (
                  <button
                    class="bm-btn bm-btn--danger bm-btn--sm"
                    type="button"
                    onClick={() => remove(item)}
                  >
                    Delete
                  </button>
                )}
              </li>
            ))}
          </ul>
        </Card>
      )}
    </div>
  );
}
