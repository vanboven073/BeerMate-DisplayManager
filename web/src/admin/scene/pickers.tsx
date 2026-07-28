/**
 * Selectors for the records a zone can point at.
 *
 * A zone references media, a website or a feed by id. These components load the
 * candidate records once per editor session and present them by name, so an
 * operator never has to know or type an id.
 */

import { useEffect, useState } from 'preact/hooks';
import { api } from '../../lib/api';
import type { MediaItem } from '../../lib/types';

export interface WebsiteRef {
  id: number;
  name: string;
  url: string;
  render_mode: string;
}

export interface FeedRef {
  id: number;
  name: string;
  platform: string;
  enabled: boolean;
}

/** Everything the editor's selectors need, loaded once and shared. */
export interface RefData {
  images: MediaItem[];
  videos: MediaItem[];
  websites: WebsiteRef[];
  feeds: FeedRef[];
  loaded: boolean;
  error: string;
}

const EMPTY: RefData = { images: [], videos: [], websites: [], feeds: [], loaded: false, error: '' };

/**
 * Loads the reference lists the pickers select from.
 *
 * Imported PDF pages are fetched separately because the media list excludes
 * `pdf_page` from its default query; they are ordinary images once rasterised
 * and an operator who imported a deck expects to see its pages here.
 */
export function useRefData(active: boolean): RefData {
  const [data, setData] = useState<RefData>(EMPTY);

  useEffect(() => {
    if (!active) return;
    const ctrl = new AbortController();

    async function load() {
      try {
        const [images, pages, videos, sites, feeds] = await Promise.all([
          api.get<{ media: MediaItem[] }>('/api/v1/media?kind=image&limit=500', ctrl.signal),
          api.get<{ media: MediaItem[] }>('/api/v1/media?kind=pdf_page&limit=500', ctrl.signal),
          api.get<{ media: MediaItem[] }>('/api/v1/media?kind=video&limit=500', ctrl.signal),
          api.get<{ websites: WebsiteRef[] }>('/api/v1/websites', ctrl.signal),
          api.get<{ feeds: FeedRef[] }>('/api/v1/social/feeds', ctrl.signal),
        ]);
        setData({
          images: (images.media || []).concat(pages.media || []),
          videos: videos.media || [],
          websites: sites.websites || [],
          feeds: feeds.feeds || [],
          loaded: true,
          error: '',
        });
      } catch (e) {
        if (ctrl.signal.aborted) return;
        setData({
          ...EMPTY,
          loaded: true,
          error: e instanceof Error ? e.message : 'Could not load media and websites',
        });
      }
    }
    void load();
    return () => ctrl.abort();
  }, [active]);

  return data;
}

function mediaLabel(item: MediaItem): string {
  const name = item.original_name || `Untitled ${item.kind}`;
  if (item.kind === 'pdf_page' && item.page_number) return `${name} — page ${item.page_number}`;
  return name;
}

export function MediaPicker({
  id,
  label,
  kind,
  data,
  value,
  onChange,
  allowNone,
}: {
  id: string;
  label: string;
  kind: 'image' | 'video';
  data: RefData;
  value: string;
  onChange: (v: string) => void;
  allowNone?: boolean;
}) {
  const items = kind === 'video' ? data.videos : data.images;
  const selected = items.filter((m) => String(m.id) === value)[0];

  return (
    <div class="bm-field">
      <label class="bm-label" for={id}>
        {label}
      </label>
      <div class="bm-picker">
        {selected && selected.has_thumbnail && (
          <img class="bm-picker__thumb" src={`/media/thumb/${selected.id}`} alt="" />
        )}
        <select
          id={id}
          class="bm-select"
          value={value}
          onChange={(e) => onChange((e.target as HTMLSelectElement).value)}
        >
          <option value="">{allowNone ? 'None' : `Choose ${kind === 'video' ? 'a video' : 'an image'}…`}</option>
          {items.map((m) => (
            <option key={m.id} value={String(m.id)}>
              {mediaLabel(m)}
            </option>
          ))}
        </select>
      </div>
      {data.loaded && items.length === 0 && (
        <p class="bm-small bm-muted">
          No {kind === 'video' ? 'videos' : 'images'} uploaded yet — add some from the Media page.
        </p>
      )}
    </div>
  );
}

export function WebsitePicker({
  id,
  data,
  value,
  onChange,
}: {
  id: string;
  data: RefData;
  value: string;
  onChange: (v: string) => void;
}) {
  return (
    <div class="bm-field">
      <label class="bm-label" for={id}>
        Website
      </label>
      <select
        id={id}
        class="bm-select"
        value={value}
        onChange={(e) => onChange((e.target as HTMLSelectElement).value)}
      >
        <option value="">Choose a website…</option>
        {data.websites.map((w) => (
          <option key={w.id} value={String(w.id)}>
            {w.name} ({w.render_mode})
          </option>
        ))}
      </select>
      {data.loaded && data.websites.length === 0 && (
        <p class="bm-small bm-muted">No websites yet — add one from the Websites page.</p>
      )}
    </div>
  );
}

export function FeedPicker({
  id,
  data,
  value,
  onChange,
}: {
  id: string;
  data: RefData;
  value: number;
  onChange: (v: number) => void;
}) {
  return (
    <div class="bm-field">
      <label class="bm-label" for={id}>
        Feed
      </label>
      <select
        id={id}
        class="bm-select"
        value={String(value || '')}
        onChange={(e) => onChange(Number((e.target as HTMLSelectElement).value) || 0)}
      >
        <option value="">Choose a feed…</option>
        {data.feeds.map((f) => (
          <option key={f.id} value={String(f.id)}>
            {f.name} ({f.platform}){f.enabled ? '' : ' — disabled'}
          </option>
        ))}
      </select>
      {data.loaded && data.feeds.length === 0 && (
        <p class="bm-small bm-muted">No social feeds yet — add one from the Social page.</p>
      )}
    </div>
  );
}
