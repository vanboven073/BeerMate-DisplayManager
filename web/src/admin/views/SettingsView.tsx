import { useState } from 'preact/hooks';
import { api, ApiError } from '../../lib/api';
import type { User } from '../../lib/types';
import { Card, ErrorNote } from '../ui';

const MIN_PASSWORD = 12;

export function SettingsView({ user }: { user: User }) {
  const [current, setCurrent] = useState('');
  const [next, setNext] = useState('');
  const [confirm, setConfirm] = useState('');
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [busy, setBusy] = useState(false);

  const mismatch = confirm.length > 0 && next !== confirm;
  const tooShort = next.length > 0 && next.length < MIN_PASSWORD;
  const canSubmit = current.length > 0 && next.length >= MIN_PASSWORD && next === confirm && !busy;

  async function changePassword(e: Event) {
    e.preventDefault();
    if (!canSubmit) return;
    setBusy(true);
    setError('');
    setNotice('');
    try {
      await api.post('/api/v1/auth/password', {
        current_password: current,
        new_password: next,
      });
      setNotice('Password changed. Any other signed-in sessions have been ended.');
      setCurrent('');
      setNext('');
      setConfirm('');
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Could not change the password');
    } finally {
      setBusy(false);
    }
  }

  async function endOtherSessions() {
    try {
      await api.del('/api/v1/auth/sessions');
      setNotice('All other sessions have been ended.');
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Could not end other sessions');
    }
  }

  return (
    <div class="bm-view">
      <header class="bm-view__head">
        <h1 class="bm-view__title">Settings</h1>
        <p class="bm-view__sub bm-muted">
          Signed in as {user.display_name || user.username} ({user.role})
        </p>
      </header>

      {error && <ErrorNote message={error} />}
      {notice && (
        <div class="bm-alert bm-alert--ok" role="status">
          {notice}
        </div>
      )}

      <Card title="Change your password">
        <form onSubmit={changePassword} noValidate class="bm-form">
          <div class="bm-field">
            <label class="bm-label" for="pw-current">
              Current password
            </label>
            <input
              id="pw-current"
              class="bm-input"
              type="password"
              autocomplete="current-password"
              value={current}
              disabled={busy}
              onInput={(e) => setCurrent((e.target as HTMLInputElement).value)}
            />
          </div>

          <div class="bm-field">
            <label class="bm-label" for="pw-new">
              New password
            </label>
            <input
              id="pw-new"
              class="bm-input"
              type="password"
              autocomplete="new-password"
              value={next}
              disabled={busy}
              aria-invalid={tooShort ? 'true' : 'false'}
              aria-describedby="pw-new-hint"
              onInput={(e) => setNext((e.target as HTMLInputElement).value)}
            />
            <p id="pw-new-hint" class={`bm-hint${tooShort ? ' bm-hint--error' : ''}`}>
              At least {MIN_PASSWORD} characters.
            </p>
          </div>

          <div class="bm-field">
            <label class="bm-label" for="pw-confirm">
              Confirm new password
            </label>
            <input
              id="pw-confirm"
              class="bm-input"
              type="password"
              autocomplete="new-password"
              value={confirm}
              disabled={busy}
              aria-invalid={mismatch ? 'true' : 'false'}
              onInput={(e) => setConfirm((e.target as HTMLInputElement).value)}
            />
            {mismatch && (
              <p class="bm-hint bm-hint--error" role="alert">
                The passwords do not match.
              </p>
            )}
          </div>

          <button class="bm-btn bm-btn--primary" type="submit" disabled={!canSubmit}>
            {busy ? 'Saving…' : 'Change password'}
          </button>
        </form>
      </Card>

      <Card title="Sessions">
        <p class="bm-muted">
          End every other signed-in session, for example after using a shared computer.
        </p>
        <button class="bm-btn bm-btn--secondary" type="button" onClick={endOtherSessions}>
          End all other sessions
        </button>
      </Card>

      <Card title="About">
        <p class="bm-small bm-muted">
          BeerMate Display Manager. The admin dashboard is reachable over Tailscale only; the player
          runs on the Jetson at <code class="bm-mono">http://127.0.0.1:8080/player</code>. Service
          health is at <code class="bm-mono">/health</code>.
        </p>
      </Card>
    </div>
  );
}
