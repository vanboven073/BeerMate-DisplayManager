import { render } from 'preact';
import './styles/brand.css';
import './styles/player.css';
import './styles/admin.css';
import { readBootstrap, setPlayerToken } from './lib/api';
import { Player } from './player/Player';
import { AdminApp } from './admin/AdminApp';

/**
 * Entry point for both applications.
 *
 * One bundle serves /admin and /player so the brand tokens, fonts and shared
 * components are downloaded once rather than twice. Which app runs is decided by
 * the server-injected bootstrap block, falling back to the URL path when running
 * under `vite dev` where the Go handler is not serving the shell.
 */
function boot(): void {
  const bootstrap = readBootstrap();

  const isPlayerPath = window.location.pathname.indexOf('/player') === 0;
  const mode = bootstrap.mode === 'player' || isPlayerPath ? 'player' : 'admin';

  const root = document.getElementById('app');
  if (!root) {
    throw new Error('mount point #app is missing from the document');
  }

  if (mode === 'player') {
    setPlayerToken(bootstrap.player_token);
    // EventSource cannot carry custom headers, so the token is also stashed for
    // the stream URL. It never leaves this document.
    (window as unknown as { __bmPlayerToken?: string }).__bmPlayerToken =
      bootstrap.player_token;

    document.documentElement.classList.add('bm-dark');
    document.title = 'BeerMate Display';

    // The player owns the whole screen for months at a time. Preventing the
    // context menu and text selection stops an accidental touch or a stray
    // mouse from leaving a menu open on the wall.
    document.addEventListener('contextmenu', (e) => e.preventDefault());

    render(<Player />, root);
    return;
  }

  document.title = 'BeerMate Display Manager';
  render(<AdminApp />, root);
}

boot();
