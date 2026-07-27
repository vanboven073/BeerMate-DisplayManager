import { useCallback, useEffect, useState } from 'preact/hooks';
import { api, ApiError } from '../../lib/api';
import type { User } from '../../lib/types';
import { Card, EmptyState, ErrorNote, formatBytes, formatWhen } from '../ui';

interface Backup {
  id: number;
  filename: string;
  bytes: number;
  sha256: string;
  kind: string;
  note: string;
  created_at: string;
  created_by: string;
}

const KIND_LABEL: Record<string, string> = {
  manual: 'Manual',
  pre_change: 'Automatic (pre-change)',
  pre_restore: 'Automatic (pre-restore)',
  scheduled: 'Scheduled',
};

export function BackupsView({ user, refreshKey }: { user: User; refreshKey: number }) {
  const [backups, setBackups] = useState<Backup[] | null>(null);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [busy, setBusy] = useState(false);
  const canEdit = user.role === 'editor' || user.role === 'admin';
  const isAdmin = user.role === 'admin';

  const load = useCallback(async (signal?: AbortSignal) => {
    try {
      const res = await api.get<{ backups: Backup[] }>('/api/v1/backups', signal);
      setBackups(res.backups);
      setError('');
    } catch (e) {
      if (signal?.aborted) return;
      setError(e instanceof Error ? e.message : 'Could not load backups');
    }
  }, []);

  useEffect(() => {
    const ctrl = new AbortController();
    void load(ctrl.signal);
    return () => ctrl.abort();
  }, [load, refreshKey]);

  const create = useCallback(async () => {
    setBusy(true);
    setError('');
    setNotice('');
    try {
      await api.post('/api/v1/backups', { note: 'manual backup' });
      setNotice('Backup created.');
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Backup failed');
    } finally {
      setBusy(false);
    }
  }, [load]);

  const restore = useCallback(
    async (b: Backup) => {
      if (
        !window.confirm(
          `Restore from ${b.filename}?\n\nThis replaces all current data with the contents of ` +
            `this backup. A safety snapshot is taken first, and the service will restart.`,
        )
      )
        return;
      setBusy(true);
      setError('');
      try {
        const res = await api.post<{ message: string }>(`/api/v1/backups/${b.id}/restore`);
        setNotice(res.message);
      } catch (e) {
        setError(e instanceof ApiError ? e.message : 'Restore failed');
      } finally {
        setBusy(false);
      }
    },
    [],
  );

  const verify = useCallback(async (b: Backup) => {
    setError('');
    setNotice('');
    try {
      await api.get(`/api/v1/backups/${b.id}/verify`);
      setNotice(`${b.filename} verified: checksum and manifest are intact.`);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'Verification failed');
    }
  }, []);

  const remove = useCallback(
    async (b: Backup) => {
      if (!window.confirm(`Delete backup ${b.filename}?`)) return;
      try {
        await api.del(`/api/v1/backups/${b.id}`);
        await load();
      } catch (e) {
        setError(e instanceof Error ? e.message : 'Delete failed');
      }
    },
    [load],
  );

  return (
    <div class="bm-view">
      <header class="bm-view__head">
        <div>
          <h1 class="bm-view__title">Backups</h1>
          <p class="bm-view__sub bm-muted">
            A backup contains the database, playlist, scenes, schedule and settings. It excludes
            browser login sessions and the encryption key, so it is safe to download.
          </p>
        </div>
        {canEdit && (
          <div class="bm-view__actions">
            <button class="bm-btn bm-btn--primary" type="button" onClick={create} disabled={busy}>
              {busy ? 'Working…' : 'Create backup'}
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

      {!backups ? (
        <p class="bm-muted">Loading…</p>
      ) : backups.length === 0 ? (
        <EmptyState
          title="No backups yet"
          body="Create a backup before making big changes. The system also takes one automatically before a restore or a legacy import."
        />
      ) : (
        <Card>
          <table class="bm-table">
            <thead>
              <tr>
                <th scope="col">Created</th>
                <th scope="col">Type</th>
                <th scope="col">Size</th>
                <th scope="col">By</th>
                <th scope="col">Actions</th>
              </tr>
            </thead>
            <tbody>
              {backups.map((b) => (
                <tr key={b.id}>
                  <th scope="row">{formatWhen(b.created_at)}</th>
                  <td>{KIND_LABEL[b.kind] || b.kind}</td>
                  <td>{formatBytes(b.bytes)}</td>
                  <td>{b.created_by || '—'}</td>
                  <td>
                    <div class="bm-row bm-row--gap bm-row--wrap">
                      <a class="bm-btn bm-btn--ghost bm-btn--sm" href={`/api/v1/backups/${b.id}/download`}>
                        Download
                      </a>
                      <button class="bm-btn bm-btn--ghost bm-btn--sm" type="button" onClick={() => verify(b)}>
                        Verify
                      </button>
                      {isAdmin && (
                        <button class="bm-btn bm-btn--secondary bm-btn--sm" type="button"
                          onClick={() => restore(b)} disabled={busy}>
                          Restore
                        </button>
                      )}
                      {canEdit && (
                        <button class="bm-btn bm-btn--danger bm-btn--sm" type="button" onClick={() => remove(b)}>
                          Delete
                        </button>
                      )}
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </Card>
      )}
    </div>
  );
}
