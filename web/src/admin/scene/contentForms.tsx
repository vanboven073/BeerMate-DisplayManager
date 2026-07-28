/**
 * One configuration form per zone content type.
 *
 * Each form writes only keys that exist on the matching Go struct in
 * `internal/content/types.go`. The server decodes with DisallowUnknownFields,
 * so an extra key here fails the save; see `model.ts` for the defaults that
 * establish the shape.
 */

import type { ContentType } from '../../lib/types';
import { CheckField, ColorField, DateTimeField, NumberField, SelectField, TextArea, TextField } from './fields';
import { FeedPicker, MediaPicker, WebsitePicker, type RefData } from './pickers';
import { fromLocalInput, toLocalInput, type ZoneConfig } from './model';

const FIT_OPTIONS = [
  { value: 'contain', label: 'Contain (whole image visible)' },
  { value: 'cover', label: 'Cover (fills, may crop)' },
  { value: 'stretch', label: 'Stretch (may distort)' },
];

const ALIGN_OPTIONS = [
  { value: 'left', label: 'Left' },
  { value: 'center', label: 'Centre' },
  { value: 'right', label: 'Right' },
];

const SIZE_OPTIONS = [
  { value: 's', label: 'Small' },
  { value: 'm', label: 'Medium' },
  { value: 'l', label: 'Large' },
  { value: 'xl', label: 'Extra large' },
];

const IMAGE_TEXT_TEMPLATES = [
  { value: 'image_left', label: 'Image left, text right' },
  { value: 'image_right', label: 'Image right, text left' },
  { value: 'full_background', label: 'Text over a full-bleed image' },
  { value: 'centered_caption', label: 'Image with a centred caption' },
  { value: 'header_image_footer', label: 'Header, image, footer' },
  { value: 'logo_title_body', label: 'Logo, title, body' },
];

const SOCIAL_TEMPLATES = [
  'single',
  'cards',
  'vertical',
  'ticker',
  'grid',
  'wall',
  'sidebar',
  'fullscreen',
  'latest',
].map((t) => ({ value: t, label: t.charAt(0).toUpperCase() + t.slice(1) }));

export interface FormProps {
  cfg: ZoneConfig;
  set: (key: string, value: unknown) => void;
  contentRef: string;
  setRef: (v: string) => void;
  data: RefData;
  /** Prefix for input ids, unique per zone. */
  idp: string;
  /** Server-reported error for a config field, if any. */
  err: (field: string) => string | undefined;
}

/* ---- accessors --------------------------------------------------------- */

const str = (cfg: ZoneConfig, k: string): string => (typeof cfg[k] === 'string' ? (cfg[k] as string) : '');
const num = (cfg: ZoneConfig, k: string): number => (typeof cfg[k] === 'number' ? (cfg[k] as number) : 0);
const bool = (cfg: ZoneConfig, k: string): boolean => cfg[k] === true;
const list = (cfg: ZoneConfig, k: string): string[] => (Array.isArray(cfg[k]) ? (cfg[k] as string[]) : []);

/* ---- dispatcher -------------------------------------------------------- */

export function ContentForm(props: FormProps & { type: ContentType }) {
  switch (props.type) {
    case 'image':
      return <ImageForm {...props} />;
    case 'video':
      return <VideoForm {...props} />;
    case 'website':
      return <WebsiteForm {...props} />;
    case 'text':
      return <TextForm {...props} />;
    case 'announcement':
      return <AnnouncementForm {...props} />;
    case 'image_text':
      return <ImageTextForm {...props} />;
    case 'countdown':
      return <CountdownForm {...props} />;
    case 'clock':
      return <ClockForm {...props} />;
    case 'kpi':
      return <KPIForm {...props} />;
    case 'qr':
      return <QRForm {...props} />;
    case 'social':
      return <SocialForm {...props} />;
    case 'ticker':
      return <TickerForm {...props} />;
    case 'event':
      return <EventForm {...props} />;
    case 'empty':
      return <p class="bm-small bm-muted">This zone stays blank. At least one zone must have content.</p>;
    default:
      return null;
  }
}

/* ---- image ------------------------------------------------------------- */

function ImageForm({ cfg, set, contentRef, setRef, data, idp, err }: FormProps) {
  return (
    <>
      <MediaPicker id={`${idp}-media`} label="Image" kind="image" data={data} value={contentRef} onChange={setRef} />
      <SelectField id={`${idp}-fit`} label="Fit" value={str(cfg, 'fit') || 'contain'}
        options={FIT_OPTIONS} error={err('fit')} onChange={(v) => set('fit', v)} />
      <TextField id={`${idp}-title`} label="Title (optional)" value={str(cfg, 'title')} maxLength={120}
        error={err('title')} onChange={(v) => set('title', v)} />
      <TextField id={`${idp}-subtitle`} label="Subtitle (optional)" value={str(cfg, 'subtitle')} maxLength={200}
        error={err('subtitle')} onChange={(v) => set('subtitle', v)} />
      <TextArea id={`${idp}-overlay`} label="Overlay text (optional)" value={str(cfg, 'overlay')} maxLength={2000}
        error={err('overlay')} onChange={(v) => set('overlay', v)} />
      <SelectField id={`${idp}-overlaypos`} label="Overlay position" value={str(cfg, 'overlay_position') || 'bottom'}
        options={[
          { value: 'top', label: 'Top' },
          { value: 'center', label: 'Centre' },
          { value: 'bottom', label: 'Bottom' },
        ]}
        onChange={(v) => set('overlay_position', v)} />
      <ColorField id={`${idp}-bg`} label="Background" value={str(cfg, 'background')}
        error={err('background')} onChange={(v) => set('background', v)} />
    </>
  );
}

/* ---- video ------------------------------------------------------------- */

function VideoForm({ cfg, set, contentRef, setRef, data, idp, err }: FormProps) {
  return (
    <>
      <MediaPicker id={`${idp}-media`} label="Video" kind="video" data={data} value={contentRef} onChange={setRef} />
      <SelectField id={`${idp}-mode`} label="Playback" value={str(cfg, 'mode') || 'loop'}
        options={[
          { value: 'loop', label: 'Loop for the scene duration' },
          { value: 'once', label: 'Play once' },
          { value: 'until_complete', label: 'Hold the scene until it finishes' },
          { value: 'duration', label: 'Use the video length as the duration' },
        ]}
        error={err('mode')} onChange={(v) => set('mode', v)} />
      <SelectField id={`${idp}-fit`} label="Fit" value={str(cfg, 'fit') || 'contain'}
        options={FIT_OPTIONS} error={err('fit')} onChange={(v) => set('fit', v)} />
      <MediaPicker id={`${idp}-poster`} label="Poster image (optional)" kind="image" data={data} allowNone
        value={num(cfg, 'poster_id') ? String(num(cfg, 'poster_id')) : ''}
        onChange={(v) => set('poster_id', Number(v) || 0)} />
      <CheckField id={`${idp}-muted`} label="Muted" checked={bool(cfg, 'muted')}
        onChange={(v) => set('muted', v)} />
      <p class="bm-small bm-muted">
        The Jetson has no audio output configured by default; leaving this muted avoids a video that
        refuses to autoplay.
      </p>
    </>
  );
}

/* ---- website ----------------------------------------------------------- */

function WebsiteForm({ cfg, set, contentRef, setRef, data, idp, err }: FormProps) {
  return (
    <>
      <WebsitePicker id={`${idp}-site`} data={data} value={contentRef} onChange={setRef} />
      <NumberField id={`${idp}-zoom`} label="Zoom" value={num(cfg, 'zoom')} min={0} max={4} step={0.05}
        hint="0 keeps the site's own scale. Otherwise between 0.25 and 4."
        error={err('zoom')} onChange={(v) => set('zoom', v)} />
      <CheckField id={`${idp}-refresh`} label="Reload every time this scene appears"
        checked={bool(cfg, 'refresh_on_show')} onChange={(v) => set('refresh_on_show', v)} />
    </>
  );
}

/* ---- text -------------------------------------------------------------- */

function TextForm({ cfg, set, idp, err }: FormProps) {
  return (
    <>
      <TextField id={`${idp}-heading`} label="Heading" value={str(cfg, 'heading')} maxLength={120}
        error={err('heading')} onChange={(v) => set('heading', v)} />
      <TextArea id={`${idp}-body`} label="Body" value={str(cfg, 'body')} rows={4} maxLength={2000}
        hint="A heading or a body is required." error={err('body')} onChange={(v) => set('body', v)} />
      <SelectField id={`${idp}-align`} label="Alignment" value={str(cfg, 'align') || 'center'}
        options={ALIGN_OPTIONS} error={err('align')} onChange={(v) => set('align', v)} />
      <SelectField id={`${idp}-size`} label="Text size" value={str(cfg, 'text_size') || 'l'}
        options={SIZE_OPTIONS} onChange={(v) => set('text_size', v)} />
      <ColorField id={`${idp}-color`} label="Text colour" value={str(cfg, 'text_color')}
        error={err('text_color')} onChange={(v) => set('text_color', v)} />
      <ColorField id={`${idp}-bg`} label="Background" value={str(cfg, 'background')}
        error={err('background')} onChange={(v) => set('background', v)} />
    </>
  );
}

/* ---- announcement ------------------------------------------------------ */

function AnnouncementForm({ cfg, set, data, idp, err }: FormProps) {
  return (
    <>
      <TextField id={`${idp}-heading`} label="Heading" value={str(cfg, 'heading')} maxLength={120}
        error={err('heading')} onChange={(v) => set('heading', v)} />
      <TextArea id={`${idp}-body`} label="Body" value={str(cfg, 'body')} rows={4} maxLength={2000}
        error={err('body')} onChange={(v) => set('body', v)} />
      <MediaPicker id={`${idp}-image`} label="Image (optional)" kind="image" data={data} allowNone
        value={num(cfg, 'image_id') ? String(num(cfg, 'image_id')) : ''}
        onChange={(v) => set('image_id', Number(v) || 0)} />
      <TextField id={`${idp}-cta`} label="Call to action (optional)" value={str(cfg, 'cta')} maxLength={80}
        error={err('cta')} onChange={(v) => set('cta', v)} />
      <TextField id={`${idp}-qr`} label="QR content (optional)" value={str(cfg, 'qr_data')}
        hint="Adds a scannable code beside the text." onChange={(v) => set('qr_data', v)} />
      <SelectField id={`${idp}-align`} label="Alignment" value={str(cfg, 'align') || 'center'}
        options={ALIGN_OPTIONS} error={err('align')} onChange={(v) => set('align', v)} />
      <SelectField id={`${idp}-size`} label="Text size" value={str(cfg, 'text_size') || 'l'}
        options={SIZE_OPTIONS} error={err('text_size')} onChange={(v) => set('text_size', v)} />
      <ColorField id={`${idp}-color`} label="Text colour" value={str(cfg, 'text_color')}
        error={err('text_color')} onChange={(v) => set('text_color', v)} />
      <ColorField id={`${idp}-bg`} label="Background" value={str(cfg, 'background')}
        error={err('background')} onChange={(v) => set('background', v)} />
      <CheckField id={`${idp}-logo`} label="Show the BeerMate logo" checked={bool(cfg, 'show_logo')}
        onChange={(v) => set('show_logo', v)} />
    </>
  );
}

/* ---- image + text ------------------------------------------------------ */

function ImageTextForm({ cfg, set, data, idp, err }: FormProps) {
  return (
    <>
      <SelectField id={`${idp}-tpl`} label="Template" value={str(cfg, 'template') || 'image_left'}
        options={IMAGE_TEXT_TEMPLATES} error={err('template')} onChange={(v) => set('template', v)} />
      <MediaPicker id={`${idp}-image`} label="Image" kind="image" data={data} allowNone
        value={num(cfg, 'image_id') ? String(num(cfg, 'image_id')) : ''}
        onChange={(v) => set('image_id', Number(v) || 0)} />
      <TextField id={`${idp}-heading`} label="Heading" value={str(cfg, 'heading')} maxLength={120}
        error={err('heading')} onChange={(v) => set('heading', v)} />
      <TextField id={`${idp}-subtitle`} label="Subtitle" value={str(cfg, 'subtitle')} maxLength={200}
        error={err('subtitle')} onChange={(v) => set('subtitle', v)} />
      <TextArea id={`${idp}-body`} label="Body" value={str(cfg, 'body')} rows={4} maxLength={2000}
        error={err('body')} onChange={(v) => set('body', v)} />
      <TextField id={`${idp}-cta`} label="Call to action (optional)" value={str(cfg, 'cta')} maxLength={80}
        error={err('cta')} onChange={(v) => set('cta', v)} />
      <TextField id={`${idp}-qr`} label="QR content (optional)" value={str(cfg, 'qr_data')}
        onChange={(v) => set('qr_data', v)} />
      <SelectField id={`${idp}-fit`} label="Image fit" value={str(cfg, 'fit') || 'cover'}
        options={FIT_OPTIONS} onChange={(v) => set('fit', v)} />
      <ColorField id={`${idp}-bg`} label="Background" value={str(cfg, 'background')}
        onChange={(v) => set('background', v)} />
      <CheckField id={`${idp}-logo`} label="Show the BeerMate logo" checked={bool(cfg, 'show_logo')}
        onChange={(v) => set('show_logo', v)} />
    </>
  );
}

/* ---- countdown --------------------------------------------------------- */

function CountdownForm({ cfg, set, data, idp, err }: FormProps) {
  return (
    <>
      <TextField id={`${idp}-title`} label="Title" value={str(cfg, 'title')} maxLength={120}
        error={err('title')} onChange={(v) => set('title', v)} />
      <TextField id={`${idp}-subtitle`} label="Subtitle (optional)" value={str(cfg, 'subtitle')} maxLength={200}
        error={err('subtitle')} onChange={(v) => set('subtitle', v)} />
      <DateTimeField id={`${idp}-target`} label="Counts down to" value={toLocalInput(str(cfg, 'target'))}
        error={err('target')} onChange={(v) => set('target', fromLocalInput(v))} />
      <TextField id={`${idp}-tz`} label="Timezone (optional)" value={str(cfg, 'timezone')}
        placeholder="Europe/Amsterdam" hint="Blank uses the server timezone."
        error={err('timezone')} onChange={(v) => set('timezone', v)} />
      <TextArea id={`${idp}-done`} label="Message when it reaches zero" value={str(cfg, 'completion_message')}
        maxLength={2000} error={err('completion_message')} onChange={(v) => set('completion_message', v)} />
      <MediaPicker id={`${idp}-doneimg`} label="Image when it reaches zero (optional)" kind="image" data={data}
        allowNone value={num(cfg, 'completion_image_id') ? String(num(cfg, 'completion_image_id')) : ''}
        onChange={(v) => set('completion_image_id', Number(v) || 0)} />
      <CheckField id={`${idp}-hide`} label="Hide leading zero units (days, hours)"
        checked={bool(cfg, 'hide_zero_units')} onChange={(v) => set('hide_zero_units', v)} />
      <CheckField id={`${idp}-expire`} label="Skip this scene once the countdown is finished"
        checked={bool(cfg, 'expire_after_done')} onChange={(v) => set('expire_after_done', v)} />
    </>
  );
}

/* ---- clock ------------------------------------------------------------- */

function ClockForm({ cfg, set, idp, err }: FormProps) {
  return (
    <>
      <TextField id={`${idp}-title`} label="Title (optional)" value={str(cfg, 'title')} maxLength={120}
        error={err('title')} onChange={(v) => set('title', v)} />
      <TextField id={`${idp}-subtitle`} label="Subtitle (optional)" value={str(cfg, 'subtitle')} maxLength={200}
        error={err('subtitle')} onChange={(v) => set('subtitle', v)} />
      <TextField id={`${idp}-tz`} label="Timezone (optional)" value={str(cfg, 'timezone')}
        placeholder="Europe/Amsterdam" hint="Blank uses the server timezone."
        error={err('timezone')} onChange={(v) => set('timezone', v)} />
      <CheckField id={`${idp}-24`} label="24-hour clock" checked={bool(cfg, 'twenty_four_hour')}
        onChange={(v) => set('twenty_four_hour', v)} />
      <CheckField id={`${idp}-secs`} label="Show seconds" checked={bool(cfg, 'show_seconds')}
        onChange={(v) => set('show_seconds', v)} />
      <CheckField id={`${idp}-date`} label="Show the date" checked={bool(cfg, 'show_date')}
        onChange={(v) => set('show_date', v)} />
    </>
  );
}

/* ---- KPI --------------------------------------------------------------- */

interface CardShape {
  label: string;
  value: string;
  unit?: string;
  trend?: string;
  status?: string;
  description?: string;
}

function KPIForm({ cfg, set, idp, err }: FormProps) {
  const cards: CardShape[] = Array.isArray(cfg.cards) ? (cfg.cards as CardShape[]) : [];
  const source = str(cfg, 'source') || 'manual';

  const setCard = (i: number, key: string, value: string) => {
    const next = cards.map((c, j) => (i === j ? { ...c, [key]: value } : c));
    set('cards', next);
  };

  return (
    <>
      <TextField id={`${idp}-title`} label="Title (optional)" value={str(cfg, 'title')} maxLength={120}
        onChange={(v) => set('title', v)} />
      <SelectField id={`${idp}-source`} label="Source" value={source}
        options={[
          { value: 'manual', label: 'Typed in by hand' },
          { value: 'api', label: 'Fetched from an API' },
        ]}
        error={err('source')} onChange={(v) => set('source', v)} />

      {source === 'api' && (
        <>
          <TextField id={`${idp}-url`} label="API URL" value={str(cfg, 'url')} type="url"
            error={err('url')} onChange={(v) => set('url', v)} />
          <NumberField id={`${idp}-refresh`} label="Refresh every (seconds)" value={num(cfg, 'refresh_sec')}
            min={30} max={86400} hint="Between 30 and 86400." error={err('refresh_sec')}
            onChange={(v) => set('refresh_sec', v)} />
          <TextField id={`${idp}-fallback`} label="Message if the fetch fails" value={str(cfg, 'fallback_message')}
            onChange={(v) => set('fallback_message', v)} />
        </>
      )}

      <fieldset class="bm-subgroup">
        <legend class="bm-label">Cards</legend>
        {err('cards') && (
          <p class="bm-field__error bm-small" role="alert">
            {err('cards')}
          </p>
        )}
        {cards.map((card, i) => (
          <div class="bm-card-row" key={i}>
            <TextField id={`${idp}-c${i}-label`} label={`Card ${i + 1} label`} value={card.label || ''}
              maxLength={80} error={err(`cards[${i}].label`)} onChange={(v) => setCard(i, 'label', v)} />
            <TextField id={`${idp}-c${i}-value`} label="Value" value={card.value || ''}
              onChange={(v) => setCard(i, 'value', v)} />
            <TextField id={`${idp}-c${i}-unit`} label="Unit" value={card.unit || ''} maxLength={24}
              error={err(`cards[${i}].unit`)} onChange={(v) => setCard(i, 'unit', v)} />
            <SelectField id={`${idp}-c${i}-trend`} label="Trend" value={card.trend || ''}
              options={[
                { value: '', label: 'None' },
                { value: 'up', label: 'Up' },
                { value: 'down', label: 'Down' },
                { value: 'flat', label: 'Flat' },
              ]}
              onChange={(v) => setCard(i, 'trend', v)} />
            <SelectField id={`${idp}-c${i}-status`} label="Status" value={card.status || 'neutral'}
              options={[
                { value: 'neutral', label: 'Neutral' },
                { value: 'good', label: 'Good' },
                { value: 'warn', label: 'Warning' },
                { value: 'bad', label: 'Bad' },
              ]}
              onChange={(v) => setCard(i, 'status', v)} />
            {cards.length > 1 && (
              <button class="bm-btn bm-btn--ghost bm-btn--sm" type="button"
                onClick={() => set('cards', cards.filter((_, j) => j !== i))}>
                Remove card {i + 1}
              </button>
            )}
          </div>
        ))}
        {cards.length < 6 && (
          <button class="bm-btn bm-btn--secondary bm-btn--sm" type="button"
            onClick={() => set('cards', cards.concat([{ label: '', value: '', unit: '', trend: '', status: 'neutral', description: '' }]))}>
            Add a card
          </button>
        )}
      </fieldset>
    </>
  );
}

/* ---- QR ---------------------------------------------------------------- */

function QRForm({ cfg, set, idp, err }: FormProps) {
  const kind = str(cfg, 'kind') || 'url';
  return (
    <>
      <SelectField id={`${idp}-kind`} label="Code type" value={kind}
        options={[
          { value: 'url', label: 'Link' },
          { value: 'text', label: 'Plain text' },
          { value: 'contact', label: 'Contact card' },
          { value: 'wifi', label: 'Wi-Fi network' },
        ]}
        error={err('kind')} onChange={(v) => set('kind', v)} />
      <TextArea id={`${idp}-data`} label={kind === 'url' ? 'Link' : 'Content'} value={str(cfg, 'data')}
        rows={kind === 'url' ? 2 : 4} maxLength={1200}
        hint={kind === 'url' ? 'A full https:// address.' : 'Up to 1200 characters; longer codes stop scanning reliably.'}
        error={err('data')} onChange={(v) => set('data', v)} />
      <TextField id={`${idp}-heading`} label="Heading (optional)" value={str(cfg, 'heading')} maxLength={120}
        error={err('heading')} onChange={(v) => set('heading', v)} />
      <TextField id={`${idp}-desc`} label="Description (optional)" value={str(cfg, 'description')} maxLength={200}
        error={err('description')} onChange={(v) => set('description', v)} />
      <SelectField id={`${idp}-ec`} label="Error correction" value={str(cfg, 'ec_level') || 'M'}
        options={[
          { value: 'L', label: 'Low' },
          { value: 'M', label: 'Medium' },
          { value: 'Q', label: 'Quartile' },
          { value: 'H', label: 'High (best with a logo)' },
        ]}
        error={err('ec_level')} onChange={(v) => set('ec_level', v)} />
      <ColorField id={`${idp}-fg`} label="Foreground" value={str(cfg, 'foreground')}
        error={err('foreground')} onChange={(v) => set('foreground', v)} />
      <ColorField id={`${idp}-bg`} label="Background" value={str(cfg, 'background')}
        error={err('background')} onChange={(v) => set('background', v)} />
      <CheckField id={`${idp}-logo`} label="Place the logo in the middle" checked={bool(cfg, 'with_logo')}
        onChange={(v) => set('with_logo', v)} />
    </>
  );
}

/* ---- social ------------------------------------------------------------ */

function SocialForm({ cfg, set, data, idp, err }: FormProps) {
  return (
    <>
      <FeedPicker id={`${idp}-feed`} data={data} value={num(cfg, 'feed_id')}
        onChange={(v) => set('feed_id', v)} />
      {err('feed_id') && (
        <p class="bm-field__error bm-small" role="alert">
          {err('feed_id')}
        </p>
      )}
      <SelectField id={`${idp}-tpl`} label="Template" value={str(cfg, 'template') || 'cards'}
        options={SOCIAL_TEMPLATES} error={err('template')} onChange={(v) => set('template', v)} />
      <NumberField id={`${idp}-max`} label="Maximum posts" value={num(cfg, 'max_items')} min={0} max={50}
        error={err('max_items')} onChange={(v) => set('max_items', v)} />
      <CheckField id={`${idp}-meta`} label="Show author and timestamp" checked={bool(cfg, 'show_meta')}
        onChange={(v) => set('show_meta', v)} />
      <p class="bm-small bm-muted">Only posts you have approved on the Social page are shown.</p>
    </>
  );
}

/* ---- ticker ------------------------------------------------------------ */

function TickerForm({ cfg, set, data, idp, err }: FormProps) {
  const source = str(cfg, 'source') || 'manual';
  const messages = list(cfg, 'messages');

  return (
    <>
      <SelectField id={`${idp}-source`} label="Source" value={source}
        options={[
          { value: 'manual', label: 'Messages typed here' },
          { value: 'feed', label: 'A social feed' },
          { value: 'emergency', label: 'Emergency messages only' },
        ]}
        error={err('source')} onChange={(v) => set('source', v)} />

      {source === 'manual' && (
        <fieldset class="bm-subgroup">
          <legend class="bm-label">Messages</legend>
          {err('messages') && (
            <p class="bm-field__error bm-small" role="alert">
              {err('messages')}
            </p>
          )}
          {messages.map((m, i) => (
            <div class="bm-row bm-row--gap" key={i}>
              <TextField id={`${idp}-m${i}`} label={`Message ${i + 1}`} value={m} maxLength={2000}
                error={err(`messages[${i}]`)}
                onChange={(v) => set('messages', messages.map((old, j) => (i === j ? v : old)))} />
              {messages.length > 1 && (
                <button class="bm-btn bm-btn--ghost bm-btn--sm" type="button"
                  onClick={() => set('messages', messages.filter((_, j) => j !== i))}>
                  Remove
                </button>
              )}
            </div>
          ))}
          {messages.length < 20 && (
            <button class="bm-btn bm-btn--secondary bm-btn--sm" type="button"
              onClick={() => set('messages', messages.concat(['']))}>
              Add a message
            </button>
          )}
        </fieldset>
      )}

      {source === 'feed' && (
        <FeedPicker id={`${idp}-feed`} data={data} value={num(cfg, 'feed_id')}
          onChange={(v) => set('feed_id', v)} />
      )}

      <SelectField id={`${idp}-dir`} label="Direction" value={str(cfg, 'direction') || 'left'}
        options={[
          { value: 'left', label: 'Right to left' },
          { value: 'right', label: 'Left to right' },
        ]}
        error={err('direction')} onChange={(v) => set('direction', v)} />
      <NumberField id={`${idp}-speed`} label="Speed (pixels per second)" value={num(cfg, 'speed_px_sec')}
        min={10} max={400} hint="Between 10 and 400." error={err('speed_px_sec')}
        onChange={(v) => set('speed_px_sec', v)} />
      <TextField id={`${idp}-sep`} label="Separator" value={str(cfg, 'separator')}
        onChange={(v) => set('separator', v)} />
      <ColorField id={`${idp}-color`} label="Text colour" value={str(cfg, 'text_color')}
        onChange={(v) => set('text_color', v)} />
      <ColorField id={`${idp}-bg`} label="Background" value={str(cfg, 'background')}
        onChange={(v) => set('background', v)} />
      <CheckField id={`${idp}-pause`} label="Pause for priority announcements"
        checked={bool(cfg, 'pause_on_priority')} onChange={(v) => set('pause_on_priority', v)} />
    </>
  );
}

/* ---- event ------------------------------------------------------------- */

function EventForm({ cfg, set, data, idp, err }: FormProps) {
  return (
    <>
      <TextField id={`${idp}-title`} label="Title" value={str(cfg, 'title')} maxLength={120}
        error={err('title')} onChange={(v) => set('title', v)} />
      <TextField id={`${idp}-subtitle`} label="Subtitle (optional)" value={str(cfg, 'subtitle')} maxLength={200}
        error={err('subtitle')} onChange={(v) => set('subtitle', v)} />
      <DateTimeField id={`${idp}-starts`} label="Starts at" value={toLocalInput(str(cfg, 'starts_at'))}
        error={err('starts_at')} onChange={(v) => set('starts_at', fromLocalInput(v))} />
      <TextField id={`${idp}-loc`} label="Location" value={str(cfg, 'location')} maxLength={80}
        error={err('location')} onChange={(v) => set('location', v)} />
      <MediaPicker id={`${idp}-image`} label="Image (optional)" kind="image" data={data} allowNone
        value={num(cfg, 'image_id') ? String(num(cfg, 'image_id')) : ''}
        onChange={(v) => set('image_id', Number(v) || 0)} />
      <TextField id={`${idp}-qr`} label="QR content (optional)" value={str(cfg, 'qr_data')}
        onChange={(v) => set('qr_data', v)} />
      <CheckField id={`${idp}-cd`} label="Show a countdown to the start" checked={bool(cfg, 'embed_countdown')}
        onChange={(v) => set('embed_countdown', v)} />
      <CheckField id={`${idp}-logo`} label="Show the BeerMate logo" checked={bool(cfg, 'show_logo')}
        onChange={(v) => set('show_logo', v)} />
    </>
  );
}
