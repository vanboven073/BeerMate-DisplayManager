/** One zone: what it shows, and how the zone itself is framed. */

import type { ContentType, Rect, ZoneStyle } from '../../lib/types';
import { CheckField, ColorField, NumberField, SelectField, TextField } from './fields';
import { ContentForm } from './contentForms';
import type { RefData } from './pickers';
import { CONTENT_LABEL, SELECTABLE_TYPES, defaultConfig, type DraftZone, type ZoneConfig } from './model';

export function ZoneForm({
  zone,
  index,
  slotLabel,
  custom,
  data,
  fieldError,
  onChange,
  onRemove,
}: {
  zone: DraftZone;
  index: number;
  slotLabel: string;
  custom: boolean;
  data: RefData;
  fieldError: (path: string) => string | undefined;
  onChange: (next: DraftZone) => void;
  onRemove?: () => void;
}) {
  const idp = `zone-${index}`;
  const patch = (next: Partial<DraftZone>) => onChange({ ...zone, ...next });

  // Switching content type replaces the configuration wholesale. Keeping the
  // previous type's keys would send fields the new type's Go struct does not
  // declare, and the strict decoder rejects the whole scene.
  const changeType = (t: ContentType) => {
    onChange({ ...zone, content_type: t, content_ref: '', config: defaultConfig(t) });
  };

  const setCfg = (key: string, value: unknown) => {
    const next: ZoneConfig = { ...zone.config };
    next[key] = value;
    patch({ config: next });
  };

  const setStyle = (key: keyof ZoneStyle, value: unknown) => {
    const next = { ...zone.style } as Record<string, unknown>;
    if (value === '' || value === 0 || value === false) delete next[key];
    else next[key] = value;
    patch({ style: next as ZoneStyle });
  };

  const setRect = (key: keyof Rect, value: number) => {
    patch({ rect: { ...zone.rect, [key]: value } });
  };

  const err = (field: string) => fieldError(`zones[${zone.slot}].${field}`);
  const style = zone.style || {};

  return (
    <section class="bm-zoneform">
      <header class="bm-zoneform__head">
        <h3 class="bm-zoneform__title">{slotLabel || `Zone ${index + 1}`}</h3>
        {custom && onRemove && (
          <button class="bm-btn bm-btn--ghost bm-btn--sm" type="button" onClick={onRemove}>
            Remove zone
          </button>
        )}
      </header>

      {fieldError(`zones[${zone.slot}]`) && (
        <p class="bm-field__error bm-small" role="alert">
          {fieldError(`zones[${zone.slot}]`)}
        </p>
      )}

      <SelectField
        id={`${idp}-type`}
        label="Content"
        value={zone.content_type}
        options={SELECTABLE_TYPES.map((t) => ({ value: t, label: CONTENT_LABEL[t] || t }))}
        error={err('content_type')}
        onChange={(v) => changeType(v as ContentType)}
      />
      {err('content_ref') && (
        <p class="bm-field__error bm-small" role="alert">
          {err('content_ref')}
        </p>
      )}

      <ContentForm
        type={zone.content_type}
        cfg={zone.config}
        set={setCfg}
        contentRef={zone.content_ref}
        setRef={(v) => patch({ content_ref: v })}
        data={data}
        idp={idp}
        err={err}
      />

      {custom && (
        <fieldset class="bm-subgroup">
          <legend class="bm-label">Position (percent of the screen)</legend>
          <div class="bm-rect">
            <NumberField id={`${idp}-x`} label="Left" value={zone.rect.x} min={0} max={100}
              onChange={(v) => setRect('x', v)} />
            <NumberField id={`${idp}-y`} label="Top" value={zone.rect.y} min={0} max={100}
              onChange={(v) => setRect('y', v)} />
            <NumberField id={`${idp}-w`} label="Width" value={zone.rect.w} min={1} max={100}
              onChange={(v) => setRect('w', v)} />
            <NumberField id={`${idp}-h`} label="Height" value={zone.rect.h} min={1} max={100}
              onChange={(v) => setRect('h', v)} />
          </div>
        </fieldset>
      )}

      <details class="bm-details">
        <summary>Appearance</summary>
        <div class="bm-details__body">
          <TextField id={`${idp}-label`} label="Zone label" value={zone.label} maxLength={80}
            hint="Shown on screen only when the label option below is on."
            error={err('label')} onChange={(v) => patch({ label: v })} />
          <ColorField id={`${idp}-sbg`} label="Zone background" value={style.background || ''}
            error={err('style.background')} onChange={(v) => setStyle('background', v)} />
          <NumberField id={`${idp}-pad`} label="Padding (%)" value={style.padding || 0} min={0} max={20}
            error={err('style.padding')} onChange={(v) => setStyle('padding', v)} />
          <NumberField id={`${idp}-bw`} label="Border width (px)" value={style.border_width || 0} min={0} max={40}
            error={err('style.border_width')} onChange={(v) => setStyle('border_width', v)} />
          <ColorField id={`${idp}-bc`} label="Border colour" value={style.border_color || ''}
            error={err('style.border_color')} onChange={(v) => setStyle('border_color', v)} />
          <NumberField id={`${idp}-br`} label="Corner radius (px)" value={style.border_radius || 0} min={0} max={200}
            error={err('style.border_radius')} onChange={(v) => setStyle('border_radius', v)} />
          <SelectField id={`${idp}-of`} label="Overflow" value={style.overflow || ''}
            options={[
              { value: '', label: 'Default' },
              { value: 'hidden', label: 'Hidden' },
              { value: 'visible', label: 'Visible' },
              { value: 'scroll', label: 'Scroll' },
            ]}
            error={err('style.overflow')} onChange={(v) => setStyle('overflow', v)} />
          <CheckField id={`${idp}-sl`} label="Show the zone label on screen" checked={style.show_label === true}
            onChange={(v) => setStyle('show_label', v)} />
        </div>
      </details>
    </section>
  );
}
