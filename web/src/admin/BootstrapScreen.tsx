import { useState } from 'preact/hooks';
import { api, ApiError } from '../lib/api';
import type { User } from '../lib/types';

interface Props {
  onComplete: (u: User) => void;
}

const MIN_PASSWORD = 12;

/** First-run setup: creates the initial administrator. */
export function BootstrapScreen({ onComplete }: Props) {
  const [username, setUsername] = useState('');
  const [displayName, setDisplayName] = useState('');
  const [password, setPassword] = useState('');
  const [confirm, setConfirm] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  const mismatch = confirm.length > 0 && password !== confirm;
  const tooShort = password.length > 0 && password.length < MIN_PASSWORD;
  const canSubmit =
    username.trim().length >= 2 && password.length >= MIN_PASSWORD && password === confirm && !busy;

  async function submit(e: Event) {
    e.preventDefault();
    if (!canSubmit) return;
    setBusy(true);
    setError('');
    try {
      const res = await api.post<{ user: User }>('/api/v1/bootstrap', {
        username: username.trim(),
        display_name: displayName.trim() || username.trim(),
        password,
      });
      onComplete(res.user);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Setup failed.');
    } finally {
      setBusy(false);
    }
  }

  return (
    <main class="bm-auth">
      <form class="bm-auth__card" onSubmit={submit} noValidate>
        <img
          class="bm-auth__logo"
          src="/brand/logo/beermate-wordmark-full.svg"
          alt="BeerMate"
          width="260"
        />
        <h1 class="bm-auth__title">Welcome</h1>
        <p class="bm-auth__sub bm-muted">
          Create the first administrator account. This can only be done once.
        </p>

        {error && (
          <div class="bm-alert bm-alert--error" role="alert">
            {error}
          </div>
        )}

        <div class="bm-field">
          <label class="bm-label" for="bs-username">
            Username
          </label>
          <input
            id="bs-username"
            class="bm-input"
            type="text"
            autocomplete="username"
            required
            value={username}
            disabled={busy}
            onInput={(e) => setUsername((e.target as HTMLInputElement).value)}
            aria-describedby="bs-username-hint"
          />
          <p id="bs-username-hint" class="bm-hint">
            2–32 characters: letters, digits, dot, dash or underscore.
          </p>
        </div>

        <div class="bm-field">
          <label class="bm-label" for="bs-display">
            Display name <span class="bm-muted">(optional)</span>
          </label>
          <input
            id="bs-display"
            class="bm-input"
            type="text"
            autocomplete="name"
            value={displayName}
            disabled={busy}
            onInput={(e) => setDisplayName((e.target as HTMLInputElement).value)}
          />
        </div>

        <div class="bm-field">
          <label class="bm-label" for="bs-password">
            Password
          </label>
          <input
            id="bs-password"
            class="bm-input"
            type="password"
            autocomplete="new-password"
            required
            value={password}
            disabled={busy}
            onInput={(e) => setPassword((e.target as HTMLInputElement).value)}
            aria-describedby="bs-password-hint"
            aria-invalid={tooShort ? 'true' : 'false'}
          />
          <p id="bs-password-hint" class={`bm-hint${tooShort ? ' bm-hint--error' : ''}`}>
            {tooShort
              ? `At least ${MIN_PASSWORD} characters. A memorable passphrase of several words is stronger than a short complex one.`
              : `At least ${MIN_PASSWORD} characters. A passphrase of several words works well.`}
          </p>
        </div>

        <div class="bm-field">
          <label class="bm-label" for="bs-confirm">
            Confirm password
          </label>
          <input
            id="bs-confirm"
            class="bm-input"
            type="password"
            autocomplete="new-password"
            required
            value={confirm}
            disabled={busy}
            onInput={(e) => setConfirm((e.target as HTMLInputElement).value)}
            aria-invalid={mismatch ? 'true' : 'false'}
            aria-describedby="bs-confirm-hint"
          />
          {mismatch && (
            <p id="bs-confirm-hint" class="bm-hint bm-hint--error" role="alert">
              The passwords do not match.
            </p>
          )}
        </div>

        <button class="bm-btn bm-btn--primary bm-btn--block" type="submit" disabled={!canSubmit}>
          {busy ? 'Creating…' : 'Create administrator'}
        </button>
      </form>
    </main>
  );
}
