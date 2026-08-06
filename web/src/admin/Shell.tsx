import { useCallback, useEffect, useState } from 'preact/hooks';
import { api } from '../lib/api';
import type { User } from '../lib/types';
import { OverviewView } from './views/OverviewView';
import { PlaylistView } from './views/PlaylistView';
import { MediaView } from './views/MediaView';
import { WebsitesView } from './views/WebsitesView';
import { SocialView } from './views/SocialView';
import { ScheduleView } from './views/ScheduleView';
import { BackupsView } from './views/BackupsView';
import { SettingsView } from './views/SettingsView';
import { useLiveEvents } from './useLiveEvents';

type ViewId =
  | 'overview'
  | 'playlist'
  | 'media'
  | 'websites'
  | 'social'
  | 'schedule'
  | 'backups'
  | 'settings';

interface NavItem {
  id: ViewId;
  label: string;
  hash: string;
}

const NAV: NavItem[] = [
  { id: 'overview', label: 'Overview', hash: '#/overview' },
  { id: 'playlist', label: 'Playlist', hash: '#/playlist' },
  { id: 'media', label: 'Media', hash: '#/media' },
  { id: 'websites', label: 'Websites', hash: '#/websites' },
  { id: 'social', label: 'Social', hash: '#/social' },
  { id: 'schedule', label: 'Schedule', hash: '#/schedule' },
  { id: 'backups', label: 'Backups', hash: '#/backups' },
  { id: 'settings', label: 'Settings', hash: '#/settings' },
];

function viewFromHash(): ViewId {
  const h = window.location.hash.replace('#/', '');
  const found = NAV.find((n) => n.id === h);
  return found ? found.id : 'overview';
}

interface Props {
  user: User;
  onSignedOut: () => void;
}

/** Authenticated dashboard shell: navigation, live status and the active view. */
export function Shell({ user, onSignedOut }: Props) {
  const [view, setView] = useState<ViewId>(viewFromHash);
  const { connected, lastEvent } = useLiveEvents();

  // Hash routing rather than the History API: the SPA is served from a fixed
  // path and hash routes need no server-side rewrite rules.
  useEffect(() => {
    const onHash = () => setView(viewFromHash());
    window.addEventListener('hashchange', onHash);
    if (!window.location.hash) window.location.hash = '#/overview';
    return () => window.removeEventListener('hashchange', onHash);
  }, []);

  const signOut = useCallback(async () => {
    try {
      await api.post('/api/v1/auth/logout');
    } finally {
      onSignedOut();
    }
  }, [onSignedOut]);

  return (
    <div class="bm-shell">
      <a class="bm-skip" href="#main">
        Skip to main content
      </a>

      <header class="bm-topbar">
        <div class="bm-topbar__brand">
          <img src="/brand/logo/beermate-mark-full.svg" alt="" width="34" height="25" />
          <span class="bm-topbar__name">
            Display Manager<span class="bm-dot" aria-hidden="true" />
          </span>
        </div>

        <nav class="bm-nav" aria-label="Sections">
          <ul class="bm-nav__list">
            {NAV.map((item) => (
              <li key={item.id}>
                <a
                  class={`bm-nav__link${view === item.id ? ' is-active' : ''}`}
                  href={item.hash}
                  aria-current={view === item.id ? 'page' : undefined}
                >
                  {item.label}
                </a>
              </li>
            ))}
          </ul>
        </nav>

        <div class="bm-topbar__right">
          {/* Connection state is shown with a shape and a word, not colour alone. */}
          <span
            class={`bm-conn${connected ? ' is-live' : ''}`}
            title={connected ? 'Live updates connected' : 'Reconnecting to live updates'}
          >
            <span class="bm-conn__dot" aria-hidden="true" />
            {connected ? 'Live' : 'Reconnecting'}
          </span>
          <span class="bm-topbar__user bm-small">
            {user.display_name || user.username}
            <span class="bm-muted"> · {user.role}</span>
          </span>
          <button class="bm-btn bm-btn--ghost bm-btn--sm" type="button" onClick={signOut}>
            Sign out
          </button>
        </div>
      </header>

      <main class="bm-main" id="main" tabIndex={-1}>
        {view === 'overview' && <OverviewView refreshKey={lastEvent} />}
        {view === 'playlist' && <PlaylistView user={user} refreshKey={lastEvent} />}
        {view === 'media' && <MediaView user={user} refreshKey={lastEvent} />}
        {view === 'websites' && <WebsitesView user={user} refreshKey={lastEvent} />}
        {view === 'social' && <SocialView user={user} refreshKey={lastEvent} />}
        {view === 'schedule' && <ScheduleView user={user} refreshKey={lastEvent} />}
        {view === 'backups' && <BackupsView user={user} refreshKey={lastEvent} />}
        {view === 'settings' && <SettingsView user={user} />}
      </main>
    </div>
  );
}
