/**
 * Render tests for the scene editor.
 *
 * These exist because the bug this editor fixes was not a broken code path but
 * a missing one: nothing in the dashboard ever called POST /api/v1/scenes. A
 * test that mounts the editor and asserts it produces that request is the one
 * that would have caught it.
 */

import { render, screen, waitFor, fireEvent } from '@testing-library/preact';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { SceneEditor } from './SceneEditor';

const layouts = {
  layouts: [
    {
      id: 'fullscreen',
      name: 'Fullscreen',
      description: 'One zone filling the entire screen.',
      slots: [{ id: 'a', label: 'Full screen', rect: { x: 0, y: 0, w: 100, h: 100 } }],
      custom: false,
    },
    {
      id: 'cols_50_50',
      name: 'Two columns (50/50)',
      description: 'Two equal columns.',
      slots: [
        { id: 'a', label: 'Left', rect: { x: 0, y: 0, w: 50, h: 100 } },
        { id: 'b', label: 'Right', rect: { x: 50, y: 0, w: 50, h: 100 } },
      ],
      custom: false,
    },
  ],
};

interface Captured {
  url: string;
  method: string;
  body: unknown;
}

let captured: Captured[] = [];

function jsonResponse(body: unknown, status = 200) {
  return Promise.resolve({
    ok: status < 400,
    status,
    headers: { get: () => 'application/json' },
    json: () => Promise.resolve(body),
    text: () => Promise.resolve(JSON.stringify(body)),
  } as unknown as Response);
}

beforeEach(() => {
  captured = [];
  document.cookie = 'beermate_csrf=test-token';

  vi.stubGlobal('fetch', (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    const method = (init && init.method) || 'GET';
    captured.push({
      url,
      method,
      body: init && typeof init.body === 'string' ? JSON.parse(init.body) : undefined,
    });

    if (url.indexOf('/api/v1/layouts') === 0) return jsonResponse(layouts);
    if (url.indexOf('/api/v1/media') === 0) return jsonResponse({ media: [] });
    if (url.indexOf('/api/v1/websites') === 0) return jsonResponse({ websites: [] });
    if (url.indexOf('/api/v1/social/feeds') === 0) return jsonResponse({ feeds: [] });
    if (url.indexOf('/api/v1/scenes') === 0) return jsonResponse({ scene: { id: 1 } }, 201);
    return jsonResponse({});
  });
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('SceneEditor', () => {
  it('renders the form with the layout catalogue', async () => {
    render(<SceneEditor scene={null} onClose={() => {}} onSaved={() => {}} />);

    await waitFor(() => expect(screen.getByLabelText('Name')).toBeTruthy());
    expect(screen.getByLabelText('Layout')).toBeTruthy();
    expect(screen.getByLabelText('Seconds on screen')).toBeTruthy();
    // The fullscreen layout contributes exactly one zone.
    expect(screen.getByText('Full screen')).toBeTruthy();
  });

  it('posts a new scene that publish will accept', async () => {
    render(<SceneEditor scene={null} onClose={() => {}} onSaved={() => {}} />);
    await waitFor(() => expect(screen.getByLabelText('Name')).toBeTruthy());

    fireEvent.input(screen.getByLabelText('Name'), { target: { value: 'Promo' } });
    fireEvent.change(screen.getByLabelText('Content'), { target: { value: 'text' } });
    await waitFor(() => expect(screen.getByLabelText('Heading')).toBeTruthy());
    fireEvent.input(screen.getByLabelText('Heading'), { target: { value: 'Hello' } });
    fireEvent.click(screen.getByText('Save scene'));

    await waitFor(() => {
      const post = captured.filter((c) => c.method === 'POST' && c.url === '/api/v1/scenes')[0];
      expect(post, 'the editor never called POST /api/v1/scenes').toBeTruthy();
    });

    const post = captured.filter((c) => c.method === 'POST' && c.url === '/api/v1/scenes')[0]!;
    const body = post.body as {
      name: string; enabled: boolean; days_mask: number; duration_ms: number;
      zones: Array<{ slot: string; content_type: string; config: string }>;
    };

    expect(body.name).toBe('Promo');
    // Both of these are what make the scene count toward publish.
    expect(body.enabled).toBe(true);
    expect(body.days_mask).toBe(127);
    expect(body.duration_ms).toBeGreaterThanOrEqual(3000);
    expect(body.zones).toHaveLength(1);
    expect(body.zones[0]!.slot).toBe('a');
    expect(body.zones[0]!.content_type).toBe('text');
    expect(JSON.parse(body.zones[0]!.config).heading).toBe('Hello');
  });

  it('adds a second zone when the layout becomes a split', async () => {
    render(<SceneEditor scene={null} onClose={() => {}} onSaved={() => {}} />);
    await waitFor(() => expect(screen.getByLabelText('Layout')).toBeTruthy());

    fireEvent.change(screen.getByLabelText('Layout'), { target: { value: 'cols_50_50' } });

    await waitFor(() => expect(screen.getByText('Left')).toBeTruthy());
    expect(screen.getByText('Right')).toBeTruthy();
  });

  it('sends a prefilled media reference straight through', async () => {
    render(
      <SceneEditor
        scene={null}
        prefill={{ name: 'poster.png', contentType: 'image', contentRef: '42' }}
        onClose={() => {}}
        onSaved={() => {}}
      />,
    );
    await waitFor(() => expect(screen.getByLabelText('Name')).toBeTruthy());
    expect((screen.getByLabelText('Name') as HTMLInputElement).value).toBe('poster.png');

    fireEvent.click(screen.getByText('Save scene'));

    await waitFor(() => {
      expect(captured.filter((c) => c.method === 'POST' && c.url === '/api/v1/scenes')).toHaveLength(1);
    });
    const post = captured.filter((c) => c.method === 'POST' && c.url === '/api/v1/scenes')[0]!;
    const body = post.body as { zones: Array<{ content_type: string; content_ref: string }> };
    expect(body.zones[0]!.content_type).toBe('image');
    expect(body.zones[0]!.content_ref).toBe('42');
  });

  it('puts rather than posts when editing an existing scene', async () => {
    const scene = {
      id: 7, revision_id: 2, stable_id: 'sid', name: 'Existing', position: 3, enabled: true,
      layout: 'fullscreen', layout_json: '', duration_ms: 9000, background: '', transition: 'fade',
      days_mask: 127, valid: true, validation_message: '', last_error: '',
      created_at: '', created_by: '', updated_at: '', updated_by: '',
      zones: [{
        id: 1, scene_id: 7, slot: 'a', rect: { x: 0, y: 0, w: 100, h: 100 }, z: 0,
        content_type: 'text' as const, content_ref: '', config: '{"heading":"Hi"}', style: '', label: '',
      }],
    };

    render(<SceneEditor scene={scene} onClose={() => {}} onSaved={() => {}} />);
    await waitFor(() => expect(screen.getByLabelText('Name')).toBeTruthy());
    fireEvent.click(screen.getByText('Save scene'));

    await waitFor(() => {
      expect(captured.filter((c) => c.method === 'PUT' && c.url === '/api/v1/scenes/7')).toHaveLength(1);
    });
    const put = captured.filter((c) => c.method === 'PUT')[0]!;
    const body = put.body as { position: number; stable_id: string };
    // Losing either of these on an edit reorders the playlist or breaks history.
    expect(body.position).toBe(3);
    expect(body.stable_id).toBe('sid');
  });
});
