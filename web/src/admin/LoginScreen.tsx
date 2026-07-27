import { useRef, useState } from 'preact/hooks';
import { api, ApiError } from '../lib/api';
import type { User } from '../lib/types';

interface Props {
  onSignedIn: (u: User) => void;
  fatal?: string;
}

/** Sign-in form. */
export function LoginScreen({ onSignedIn, fatal }: Props) {
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const errorRef = useRef<HTMLDivElement | null>(null);

  async function submit(e: Event) {
    e.preventDefault();
    if (busy) return;
    setBusy(true);
    setError('');
    try {
      const res = await api.post<{ user: User }>('/api/v1/auth/login', { username, password });
      onSignedIn(res.user);
    } catch (err) {
      const msg =
        err instanceof ApiError ? err.message : 'Could not reach the display manager.';
      setError(msg);
      // Move focus to the message so a screen reader announces it and a keyboard
      // user is not left wondering why nothing happened.
      window.setTimeout(() => errorRef.current?.focus(), 0);
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
        <h1 class="bm-auth__title">Display Manager</h1>
        <p class="bm-auth__sub bm-muted">Sign in to manage what is on screen.</p>

        {fatal && (
          <div class="bm-alert bm-alert--warn" role="status">
            {fatal}
          </div>
        )}

        {error && (
          <div class="bm-alert bm-alert--error" role="alert" tabIndex={-1} ref={errorRef}>
            {error}
          </div>
        )}

        <div class="bm-field">
          <label class="bm-label" for="login-username">
            Username
          </label>
          <input
            id="login-username"
            class="bm-input"
            type="text"
            autocomplete="username"
            required
            value={username}
            disabled={busy}
            onInput={(e) => setUsername((e.target as HTMLInputElement).value)}
          />
        </div>

        <div class="bm-field">
          <label class="bm-label" for="login-password">
            Password
          </label>
          <input
            id="login-password"
            class="bm-input"
            type="password"
            autocomplete="current-password"
            required
            value={password}
            disabled={busy}
            onInput={(e) => setPassword((e.target as HTMLInputElement).value)}
          />
        </div>

        <button class="bm-btn bm-btn--primary bm-btn--block" type="submit" disabled={busy}>
          {busy ? 'Signing in…' : 'Sign in'}
        </button>

        <p class="bm-auth__hint bm-small bm-muted">
          Reachable over Tailscale only. If you cannot sign in, check the service with{' '}
          <code class="bm-mono">systemctl status beermate-display-manager</code>.
        </p>
      </form>
    </main>
  );
}
