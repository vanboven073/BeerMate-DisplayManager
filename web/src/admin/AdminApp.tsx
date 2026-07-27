import { useCallback, useEffect, useState } from 'preact/hooks';
import { api, ApiError } from '../lib/api';
import type { User } from '../lib/types';
import { LoginScreen } from './LoginScreen';
import { BootstrapScreen } from './BootstrapScreen';
import { Shell } from './Shell';

type Phase = 'loading' | 'bootstrap' | 'login' | 'ready';

interface MeResponse {
  user: User;
  csrf_token: string;
}

/**
 * Root of the admin dashboard.
 *
 * Resolves one of three states at startup: the instance has no administrator yet
 * (first-run setup), nobody is signed in (login), or a session is active.
 */
export function AdminApp() {
  const [phase, setPhase] = useState<Phase>('loading');
  const [user, setUser] = useState<User | null>(null);
  const [fatal, setFatal] = useState('');

  const resolve = useCallback(async () => {
    try {
      const me = await api.get<MeResponse>('/api/v1/auth/me');
      setUser(me.user);
      setPhase('ready');
      return;
    } catch (err) {
      if (!(err instanceof ApiError) || !err.isUnauthorised) {
        setFatal(err instanceof Error ? err.message : 'Could not reach the display manager.');
        setPhase('login');
        return;
      }
      // Not signed in: find out whether this is a fresh installation.
    }

    try {
      const status = await api.get<{ needs_bootstrap: boolean }>('/api/v1/bootstrap/status');
      setPhase(status.needs_bootstrap ? 'bootstrap' : 'login');
    } catch {
      setFatal('Could not reach the display manager.');
      setPhase('login');
    }
  }, []);

  useEffect(() => {
    void resolve();
  }, [resolve]);

  const handleSignedIn = useCallback((u: User) => {
    setUser(u);
    setFatal('');
    setPhase('ready');
  }, []);

  const handleSignedOut = useCallback(() => {
    setUser(null);
    setPhase('login');
  }, []);

  if (phase === 'loading') {
    return (
      <div class="bm-splash" role="status" aria-live="polite">
        <img src="/brand/logo/beermate-mark-full.svg" alt="" width="96" height="70" />
        <p class="bm-muted">Loading&hellip;</p>
      </div>
    );
  }

  if (phase === 'bootstrap') {
    return <BootstrapScreen onComplete={handleSignedIn} />;
  }

  if (phase === 'login' || !user) {
    return <LoginScreen onSignedIn={handleSignedIn} fatal={fatal} />;
  }

  return <Shell user={user} onSignedOut={handleSignedOut} />;
}
