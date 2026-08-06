import { useEffect, useRef, useState } from 'preact/hooks';
import type {
  AnnouncementConfig,
  ClockConfig,
  CountdownConfig,
  EventConfig,
  ImageConfig,
  KPIConfig,
  QRConfig,
  SocialConfig,
  SocialPost,
  TextConfig,
  TickerConfig,
  VideoConfig,
  Zone,
  ZoneStyle,
} from '../lib/types';
import { parseConfig } from '../lib/types';
import { api, mediaUrl } from '../lib/api';
import { planLayout, pageCount } from './socialLayout';
import { ZoneFallback } from './Chrome';
import { countdownParts, formatInZone, type ServerClock } from './useServerClock';

interface Props {
  zone: Zone;
  clock: ServerClock;
  timezone: string;
}

/**
 * Renders one zone's content.
 *
 * Every branch either renders real content or a branded fallback: a zone must
 * never resolve to nothing, because an empty region on a wall display is
 * indistinguishable from a crash.
 */
export function ZoneRenderer({ zone, clock, timezone }: Props) {
  const style = parseConfig<ZoneStyle>(zone.style, {});

  const wrapperStyle: Record<string, string> = {};
  if (style.background) wrapperStyle.background = style.background;
  if (style.padding) wrapperStyle.padding = `${style.padding}%`;
  if (style.border_width && style.border_color) {
    wrapperStyle.border = `${style.border_width}px solid ${style.border_color}`;
  }
  if (style.border_radius !== undefined) {
    wrapperStyle.borderRadius = `${style.border_radius}px`;
  }
  if (style.overflow) wrapperStyle.overflow = style.overflow;

  return (
    <div class="bm-zone__inner" style={wrapperStyle}>
      {style.show_label && zone.label && <div class="bm-zone__label">{zone.label}</div>}
      <ZoneContent zone={zone} clock={clock} timezone={timezone} />
    </div>
  );
}

function ZoneContent({ zone, clock, timezone }: Props) {
  switch (zone.content_type) {
    case 'image':
      return <ImageZone zone={zone} />;
    case 'video':
      return <VideoZone zone={zone} />;
    case 'website':
      return <WebsiteZone zone={zone} />;
    case 'countdown':
      return <CountdownZone zone={zone} clock={clock} />;
    case 'clock':
      return <ClockZone zone={zone} clock={clock} timezone={timezone} />;
    case 'kpi':
      return <KPIZone zone={zone} />;
    case 'qr':
      return <QRZone zone={zone} />;
    case 'announcement':
      return <AnnouncementZone zone={zone} />;
    case 'image_text':
      return <ImageTextZone zone={zone} />;
    case 'social':
      return <SocialZone zone={zone} />;
    case 'text':
      return <TextZone zone={zone} />;
    case 'ticker':
      return <TickerZone zone={zone} />;
    case 'event':
      return <EventZone zone={zone} clock={clock} timezone={timezone} />;
    case 'empty':
      return <div class="bm-zone__empty" aria-hidden="true" />;
    case 'fallback':
      return <ZoneFallback reason="Content temporarily unavailable" />;
    default:
      return <ZoneFallback reason="Unsupported content type" />;
  }
}

/* ---- image ------------------------------------------------------------ */

function ImageZone({ zone }: { zone: Zone }) {
  const cfg = parseConfig<ImageConfig>(zone.config, {});
  const [failed, setFailed] = useState(false);

  if (!zone.content_ref || failed) {
    return <ZoneFallback reason="Image unavailable" />;
  }

  const fit = cfg.fit === 'stretch' ? 'fill' : cfg.fit === 'cover' ? 'cover' : 'contain';

  return (
    <div class="bm-img" style={cfg.background ? { background: cfg.background } : undefined}>
      <img
        class="bm-img__el"
        src={mediaUrl(zone.content_ref)}
        alt={cfg.title || ''}
        style={{ objectFit: fit }}
        onError={() => setFailed(true)}
        // Decoding asynchronously keeps a large JPEG from blocking the
        // transition on a Jetson's modest CPU.
        decoding="async"
      />
      {(cfg.title || cfg.subtitle || cfg.overlay) && (
        <div class={`bm-img__overlay bm-img__overlay--${cfg.overlay_position || 'bottom'}`}>
          {cfg.title && <div class="bm-img__title">{cfg.title}</div>}
          {cfg.subtitle && <div class="bm-img__subtitle">{cfg.subtitle}</div>}
          {cfg.overlay && <div class="bm-img__text">{cfg.overlay}</div>}
        </div>
      )}
    </div>
  );
}

/* ---- video ------------------------------------------------------------ */

function VideoZone({ zone }: { zone: Zone }) {
  const cfg = parseConfig<VideoConfig>(zone.config, {});
  const ref = useRef<HTMLVideoElement | null>(null);
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    const el = ref.current;
    if (!el) return;

    // Autoplay is only permitted while muted. Signage has no speakers by
    // default and an unmuted autoplay would simply be blocked.
    el.muted = cfg.muted !== false;
    const attempt = el.play();
    if (attempt && typeof attempt.catch === 'function') {
      attempt.catch(() => setFailed(true));
    }

    return () => {
      // Explicit teardown. Leaving a <video> attached to a decoded stream is the
      // single largest source of memory growth on a page that plays hundreds of
      // clips between browser restarts.
      try {
        el.pause();
        el.removeAttribute('src');
        el.load();
      } catch {
        // The element may already be detached; nothing further to do.
      }
    };
  }, [zone.content_ref, cfg.muted]);

  if (!zone.content_ref || failed) {
    return <ZoneFallback reason="Video unavailable" />;
  }

  const fit = cfg.fit === 'stretch' ? 'fill' : cfg.fit === 'contain' ? 'contain' : 'cover';

  return (
    <video
      ref={ref}
      class="bm-video"
      src={mediaUrl(zone.content_ref)}
      poster={cfg.poster_id ? mediaUrl(cfg.poster_id) : undefined}
      style={{ objectFit: fit }}
      autoPlay
      muted
      playsInline
      loop={cfg.mode === 'loop'}
      preload="auto"
      onError={() => setFailed(true)}
    />
  );
}

/* ---- website ---------------------------------------------------------- */

function WebsiteZone({ zone }: { zone: Zone }) {
  const [failed, setFailed] = useState(false);

  if (!zone.content_ref || failed) {
    return <ZoneFallback reason="Website unavailable" />;
  }

  // Embeddable sites render in a sandboxed iframe. Sites that refuse framing or
  // need a login are rendered by the managed browser and arrive as a capture
  // stream instead; the server decides which by the website's render_mode.
  return (
    <iframe
      class="bm-website"
      src={`/api/v1/websites/${zone.content_ref}/frame`}
      title={zone.label || 'Website'}
      // allow-same-origin is deliberately absent: the frame gets no access to
      // this document, its cookies or its storage.
      sandbox="allow-scripts allow-popups-to-escape-sandbox"
      referrerPolicy="no-referrer"
      loading="eager"
      onError={() => setFailed(true)}
    />
  );
}

/* ---- countdown -------------------------------------------------------- */

function CountdownZone({ zone, clock }: { zone: Zone; clock: ServerClock }) {
  const cfg = parseConfig<CountdownConfig>(zone.config, {
    title: '',
    target: '',
    timezone: 'Europe/Amsterdam',
  });

  const targetMs = Date.parse(cfg.target);
  if (Number.isNaN(targetMs)) {
    return <ZoneFallback reason="Countdown target is not set" />;
  }

  // clock.tick drives the re-render; reading it here is what makes this update
  // once a second without its own timer.
  void clock.tick;
  const parts = countdownParts(targetMs - clock.now().getTime());

  if (parts.done) {
    return (
      <div class="bm-countdown bm-countdown--done">
        <div class="bm-countdown__title">{cfg.title}</div>
        <div class="bm-countdown__message">{cfg.completion_message || "It's here!"}</div>
      </div>
    );
  }

  const units: Array<{ value: number; label: string }> = [
    { value: parts.days, label: parts.days === 1 ? 'day' : 'days' },
    { value: parts.hours, label: parts.hours === 1 ? 'hour' : 'hours' },
    { value: parts.minutes, label: parts.minutes === 1 ? 'minute' : 'minutes' },
    { value: parts.seconds, label: parts.seconds === 1 ? 'second' : 'seconds' },
  ];

  // Leading zero-valued units are dropped when configured, but never the last
  // two: "0 seconds" alone reads as broken.
  const shown = cfg.hide_zero_units ? dropLeadingZeros(units) : units;

  return (
    <div class="bm-countdown">
      <div class="bm-countdown__title">{cfg.title}</div>
      {cfg.subtitle && <div class="bm-countdown__subtitle">{cfg.subtitle}</div>}
      <div class="bm-countdown__units">
        {shown.map((u) => (
          <div class="bm-countdown__unit" key={u.label}>
            <div class="bm-countdown__value">{String(u.value).padStart(2, '0')}</div>
            <div class="bm-countdown__label">{u.label}</div>
          </div>
        ))}
      </div>
    </div>
  );
}

function dropLeadingZeros(units: Array<{ value: number; label: string }>) {
  let start = 0;
  while (start < units.length - 2 && (units[start]?.value ?? 0) === 0) start++;
  return units.slice(start);
}

/* ---- clock ------------------------------------------------------------ */

function ClockZone({ zone, clock, timezone }: { zone: Zone; clock: ServerClock; timezone: string }) {
  const cfg = parseConfig<ClockConfig>(zone.config, {});
  void clock.tick;

  const tz = cfg.timezone || timezone;
  const now = clock.now();

  const timeOpts: Intl.DateTimeFormatOptions = {
    hour: '2-digit',
    minute: '2-digit',
    hour12: cfg.twenty_four_hour === false,
  };
  if (cfg.show_seconds) timeOpts.second = '2-digit';

  return (
    <div class="bm-clock">
      {cfg.title && <div class="bm-clock__title">{cfg.title}</div>}
      <div class="bm-clock__time">{formatInZone(now, tz, timeOpts)}</div>
      {cfg.show_date !== false && (
        <div class="bm-clock__date">
          {formatInZone(now, tz, { weekday: 'long', day: 'numeric', month: 'long' })}
        </div>
      )}
      {cfg.subtitle && <div class="bm-clock__subtitle">{cfg.subtitle}</div>}
    </div>
  );
}

/* ---- KPI -------------------------------------------------------------- */

function KPIZone({ zone }: { zone: Zone }) {
  const cfg = parseConfig<KPIConfig>(zone.config, { cards: [], source: 'manual' });
  if (!cfg.cards || cfg.cards.length === 0) {
    return <ZoneFallback reason={cfg.fallback_message || 'No metrics configured'} />;
  }

  return (
    <div class="bm-kpi">
      {cfg.title && <div class="bm-kpi__title">{cfg.title}</div>}
      <div class="bm-kpi__grid" data-count={cfg.cards.length}>
        {cfg.cards.map((card, i) => (
          <div class={`bm-kpi__card bm-kpi__card--${card.status || 'neutral'}`} key={i}>
            <div class="bm-kpi__label">{card.label}</div>
            <div class="bm-kpi__value">
              {card.value}
              {card.unit && <span class="bm-kpi__unit">{card.unit}</span>}
            </div>
            {/* Trend is shown as an arrow glyph plus a text label so the meaning
                never depends on colour alone. */}
            {card.trend && (
              <div class="bm-kpi__trend">
                <span aria-hidden="true">
                  {card.trend === 'up' ? '▲' : card.trend === 'down' ? '▼' : '▬'}
                </span>
                <span class="bm-visually-hidden">{`trending ${card.trend}`}</span>
                {card.target && <span class="bm-kpi__target">target {card.target}</span>}
              </div>
            )}
            {card.description && <div class="bm-kpi__desc">{card.description}</div>}
          </div>
        ))}
      </div>
    </div>
  );
}

/* ---- social ----------------------------------------------------------- */

/**
 * Approved posts from one social feed.
 *
 * Posts are fetched rather than delivered with the playlist because moderation
 * changes far more often than the playlist does, and a moderator hiding a post
 * should not require republishing. The server sends an SSE `social` event on a
 * change, which refreshes player state; the interval here is the fallback for a
 * zone that stays mounted across such a refresh.
 */
function SocialZone({ zone }: { zone: Zone }) {
  const cfg = parseConfig<SocialConfig>(zone.config, { feed_id: 0, template: 'cards' });
  const [posts, setPosts] = useState<SocialPost[] | null>(null);
  const [failed, setFailed] = useState(false);

  const feedID = cfg.feed_id;
  const limit = cfg.max_items && cfg.max_items > 0 ? cfg.max_items : 6;

  useEffect(() => {
    if (!feedID) return;
    let cancelled = false;
    const ctrl = new AbortController();

    async function load() {
      try {
        const res = await api.player.get<{ posts: SocialPost[] }>(
          `/api/v1/player/social/${feedID}?limit=${limit}`,
          ctrl.signal,
        );
        if (cancelled) return;
        setPosts(res.posts || []);
        setFailed(false);
      } catch {
        if (cancelled) return;
        // Keep whatever is already on screen: a transient fetch failure should
        // not blank a zone that is currently showing valid posts.
        setFailed(true);
      }
    }

    void load();
    const timer = window.setInterval(() => void load(), 60000);
    return () => {
      cancelled = true;
      ctrl.abort();
      window.clearInterval(timer);
    };
  }, [feedID, limit]);

  if (!feedID) return <ZoneFallback reason="No social feed selected" />;
  if (posts === null) {
    return failed ? <ZoneFallback reason="Social feed unavailable" /> : <div class="bm-social bm-social--loading" />;
  }
  if (posts.length === 0) return <ZoneFallback reason="No approved posts yet" />;

  const template = cfg.template || 'cards';
  const shown = posts.slice(0, limit);

  return (
    <SocialPages template={template} posts={shown} showMeta={cfg.show_meta !== false} />
  );
}

/** How long one page of posts stays on screen. */
const SOCIAL_PAGE_MS = 8000;

/**
 * SocialPages lays the posts out for the zone's actual size and pages through
 * whatever does not fit, cross-fading between pages.
 */
function SocialPages({
  template,
  posts,
  showMeta,
}: {
  template: string;
  posts: SocialPost[];
  showMeta: boolean;
}) {
  const hostRef = useRef<HTMLDivElement | null>(null);
  const [size, setSize] = useState({ w: 0, h: 0 });
  const [page, setPage] = useState(0);

  // Zone rects are fixed for the life of a scene, so measuring on mount and on
  // window resize is enough; no ResizeObserver needed.
  useEffect(() => {
    function measure() {
      const el = hostRef.current;
      if (el) setSize({ w: el.clientWidth, h: el.clientHeight });
    }
    measure();
    window.addEventListener('resize', measure);
    return () => window.removeEventListener('resize', measure);
  }, []);

  const plan = planLayout(template, posts.length, size.w, size.h);
  const pages = pageCount(posts.length, plan.perPage);
  const current = page % pages;

  // Keyed on the page index and count, never on the posts array: the feed
  // refetches every 60s, and depending on object identity would restart the page
  // dwell each time — the same defect that froze scene rotation.
  useEffect(() => {
    if (pages <= 1) return;
    const timer = window.setTimeout(() => setPage((p) => (p + 1) % pages), SOCIAL_PAGE_MS);
    return () => window.clearTimeout(timer);
  }, [current, pages]);

  // Keep the index in range when the post count shrinks under us.
  useEffect(() => {
    if (page >= pages) setPage(0);
  }, [page, pages]);

  const slice = posts.slice(current * plan.perPage, current * plan.perPage + plan.perPage);
  const gridStyle =
    plan.columns > 1 ? { gridTemplateColumns: `repeat(${plan.columns}, 1fr)` } : undefined;

  return (
    <div class={`bm-social bm-social--${template}`} ref={hostRef}>
      {/* The key remounts the page so the fade animation replays. */}
      <div class="bm-social__page" key={current} style={gridStyle}>
        {slice.map((post) => (
        <article class="bm-social__post" key={post.id}>
          {post.media_url && post.media_kind === 'image' && (
            <div class="bm-social__media">
              <img src={post.media_url} alt="" loading="lazy" />
            </div>
          )}
          <div class="bm-social__body">
            {showMeta && (
              <header class="bm-social__meta">
                {post.avatar_url && <img class="bm-social__avatar" src={post.avatar_url} alt="" />}
                <span class="bm-social__author">{post.author || post.author_handle}</span>
                {post.posted_at && (
                  <time class="bm-social__time" dateTime={post.posted_at}>
                    {new Date(post.posted_at).toLocaleDateString()}
                  </time>
                )}
              </header>
            )}
            {/* Post text arrives HTML-stripped from the server and is rendered
                as text, never as markup. */}
            {post.text && <p class="bm-social__text">{post.text}</p>}
          </div>
        </article>
        ))}
      </div>
    </div>
  );
}

/* ---- QR --------------------------------------------------------------- */

function QRZone({ zone }: { zone: Zone }) {
  const cfg = parseConfig<QRConfig>(zone.config, { kind: 'text', data: '' });
  if (!cfg.data) return <ZoneFallback reason="No QR content configured" />;

  // The code is rendered server-side: the payload never has to be encoded in
  // JavaScript, and the resulting PNG is cacheable.
  const params = new URLSearchParams({ data: cfg.data, ec: cfg.ec_level || 'M' });
  if (cfg.foreground) params.set('fg', cfg.foreground);
  if (cfg.background) params.set('bg', cfg.background);

  return (
    <div class="bm-qr">
      {cfg.heading && <div class="bm-qr__heading">{cfg.heading}</div>}
      <img class="bm-qr__img" src={`/api/v1/qr?${params.toString()}`} alt="QR code" />
      {cfg.description && <div class="bm-qr__desc">{cfg.description}</div>}
    </div>
  );
}

/* ---- announcement, image+text, text ----------------------------------- */

function AnnouncementZone({ zone }: { zone: Zone }) {
  const cfg = parseConfig<AnnouncementConfig>(zone.config, { heading: '' });
  const style: Record<string, string> = {};
  if (cfg.background) style.background = cfg.background;
  if (cfg.text_color) style.color = cfg.text_color;

  return (
    <div class={`bm-announce bm-announce--${cfg.align || 'center'} bm-announce--${cfg.text_size || 'l'}`} style={style}>
      {cfg.show_logo && (
        <img class="bm-announce__logo" src="/brand/logo/beermate-wordmark-mono-ivory.svg" alt="BeerMate" width="220" />
      )}
      {cfg.image_id ? <img class="bm-announce__img" src={mediaUrl(cfg.image_id)} alt="" /> : null}
      <h2 class="bm-announce__heading">{cfg.heading}</h2>
      {cfg.body && <p class="bm-announce__body">{cfg.body}</p>}
      {cfg.cta && <div class="bm-announce__cta">{cfg.cta}</div>}
    </div>
  );
}

function ImageTextZone({ zone }: { zone: Zone }) {
  const cfg = parseConfig<AnnouncementConfig & { template?: string; subtitle?: string }>(
    zone.config,
    { heading: '' },
  );
  const template = cfg.template || 'image_left';

  return (
    <div class={`bm-imagetext bm-imagetext--${template}`}>
      {cfg.image_id && (
        <div class="bm-imagetext__media">
          <img src={mediaUrl(cfg.image_id)} alt="" />
        </div>
      )}
      <div class="bm-imagetext__body">
        {cfg.show_logo && (
          <img class="bm-imagetext__logo" src="/brand/logo/beermate-wordmark-mono-ivory.svg" alt="BeerMate" width="180" />
        )}
        <h2 class="bm-imagetext__heading">{cfg.heading}</h2>
        {cfg.subtitle && <div class="bm-imagetext__subtitle">{cfg.subtitle}</div>}
        {cfg.body && <p class="bm-imagetext__text">{cfg.body}</p>}
        {cfg.cta && <div class="bm-imagetext__cta">{cfg.cta}</div>}
      </div>
    </div>
  );
}

function TextZone({ zone }: { zone: Zone }) {
  const cfg = parseConfig<TextConfig>(zone.config, {});
  const style: Record<string, string> = {};
  if (cfg.background) style.background = cfg.background;
  if (cfg.text_color) style.color = cfg.text_color;

  return (
    <div class={`bm-text bm-text--${cfg.align || 'center'}`} style={style}>
      {cfg.heading && <h2 class="bm-text__heading">{cfg.heading}</h2>}
      {cfg.body && <p class="bm-text__body">{cfg.body}</p>}
    </div>
  );
}

/* ---- ticker ----------------------------------------------------------- */

function TickerZone({ zone }: { zone: Zone }) {
  const cfg = parseConfig<TickerConfig>(zone.config, { source: 'manual', messages: [] });
  const messages = cfg.messages && cfg.messages.length > 0 ? cfg.messages : [];

  if (messages.length === 0) {
    return <ZoneFallback reason="No ticker messages" />;
  }

  const separator = cfg.separator || '  •  ';
  const text = messages.join(separator);
  const speed = cfg.speed_px_sec || 80;
  // Duration is derived from the text length so the scroll speed stays constant
  // regardless of how much content there is.
  const durationSec = Math.max(10, Math.round((text.length * 14) / speed));

  const style: Record<string, string> = {
    animationDuration: `${durationSec}s`,
    animationDirection: cfg.direction === 'right' ? 'reverse' : 'normal',
  };
  const wrapStyle: Record<string, string> = {};
  if (cfg.background) wrapStyle.background = cfg.background;
  if (cfg.text_color) wrapStyle.color = cfg.text_color;

  return (
    <div class="bm-ticker" style={wrapStyle}>
      <div class="bm-ticker__track" style={style}>
        {/* Duplicated so the loop has no visible gap at the wrap point. */}
        <span class="bm-ticker__text">{text}</span>
        <span class="bm-ticker__text" aria-hidden="true">
          {separator}
          {text}
        </span>
      </div>
    </div>
  );
}

/* ---- event ------------------------------------------------------------ */

function EventZone({ zone, clock, timezone }: { zone: Zone; clock: ServerClock; timezone: string }) {
  const cfg = parseConfig<EventConfig>(zone.config, { title: '' });
  void clock.tick;

  const startsMs = cfg.starts_at ? Date.parse(cfg.starts_at) : NaN;
  const parts = Number.isNaN(startsMs) ? null : countdownParts(startsMs - clock.now().getTime());

  return (
    <div class="bm-event">
      {cfg.image_id && <img class="bm-event__bg" src={mediaUrl(cfg.image_id)} alt="" />}
      <div class="bm-event__inner">
        {cfg.show_logo && (
          <img class="bm-event__logo" src="/brand/logo/beermate-wordmark-mono-ivory.svg" alt="BeerMate" width="200" />
        )}
        <h2 class="bm-event__title">{cfg.title}</h2>
        {cfg.subtitle && <div class="bm-event__subtitle">{cfg.subtitle}</div>}
        <div class="bm-event__meta">
          {!Number.isNaN(startsMs) && (
            <span>
              {formatInZone(new Date(startsMs), timezone, {
                weekday: 'long',
                day: 'numeric',
                month: 'long',
                hour: '2-digit',
                minute: '2-digit',
                hour12: false,
              })}
            </span>
          )}
          {cfg.location && <span>{cfg.location}</span>}
        </div>
        {cfg.embed_countdown && parts && !parts.done && (
          <div class="bm-event__countdown">
            {parts.days > 0 && <span>{parts.days}d</span>}
            <span>{String(parts.hours).padStart(2, '0')}h</span>
            <span>{String(parts.minutes).padStart(2, '0')}m</span>
            <span>{String(parts.seconds).padStart(2, '0')}s</span>
          </div>
        )}
      </div>
    </div>
  );
}
