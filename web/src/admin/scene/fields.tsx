/** Small controlled inputs shared by every content form. */

import type { ComponentChildren } from 'preact';

export interface FieldProps {
  id: string;
  label: string;
  error?: string;
  hint?: string;
}

function Wrap({ id, label, error, hint, children }: FieldProps & { children: ComponentChildren }) {
  return (
    <div class={`bm-field${error ? ' is-invalid' : ''}`}>
      <label class="bm-label" for={id}>
        {label}
      </label>
      {children}
      {hint && !error && <p class="bm-small bm-muted">{hint}</p>}
      {error && (
        <p class="bm-field__error bm-small" role="alert">
          {error}
        </p>
      )}
    </div>
  );
}

export function TextField({
  value,
  onChange,
  placeholder,
  maxLength,
  type,
  ...rest
}: FieldProps & {
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  maxLength?: number;
  type?: string;
}) {
  return (
    <Wrap {...rest}>
      <input
        id={rest.id}
        class="bm-input"
        type={type || 'text'}
        value={value}
        placeholder={placeholder}
        maxLength={maxLength}
        onInput={(e) => onChange((e.target as HTMLInputElement).value)}
      />
    </Wrap>
  );
}

export function TextArea({
  value,
  onChange,
  rows,
  maxLength,
  ...rest
}: FieldProps & { value: string; onChange: (v: string) => void; rows?: number; maxLength?: number }) {
  return (
    <Wrap {...rest}>
      <textarea
        id={rest.id}
        class="bm-textarea"
        rows={rows || 3}
        value={value}
        maxLength={maxLength}
        onInput={(e) => onChange((e.target as HTMLTextAreaElement).value)}
      />
    </Wrap>
  );
}

export function SelectField({
  value,
  onChange,
  options,
  ...rest
}: FieldProps & {
  value: string;
  onChange: (v: string) => void;
  options: Array<{ value: string; label: string }>;
}) {
  return (
    <Wrap {...rest}>
      <select
        id={rest.id}
        class="bm-select"
        value={value}
        onChange={(e) => onChange((e.target as HTMLSelectElement).value)}
      >
        {options.map((o) => (
          <option key={o.value} value={o.value}>
            {o.label}
          </option>
        ))}
      </select>
    </Wrap>
  );
}

export function NumberField({
  value,
  onChange,
  min,
  max,
  step,
  ...rest
}: FieldProps & {
  value: number;
  onChange: (v: number) => void;
  min?: number;
  max?: number;
  step?: number;
}) {
  return (
    <Wrap {...rest}>
      <input
        id={rest.id}
        class="bm-input"
        type="number"
        value={String(value)}
        min={min}
        max={max}
        step={step}
        onInput={(e) => {
          const raw = (e.target as HTMLInputElement).value;
          onChange(raw === '' ? 0 : Number(raw));
        }}
      />
    </Wrap>
  );
}

export function CheckField({
  id,
  label,
  checked,
  onChange,
}: {
  id: string;
  label: string;
  checked: boolean;
  onChange: (v: boolean) => void;
}) {
  return (
    <label class="bm-check" for={id}>
      <input
        id={id}
        type="checkbox"
        checked={checked}
        onChange={(e) => onChange((e.target as HTMLInputElement).checked)}
      />
      <span>{label}</span>
    </label>
  );
}

/**
 * A hex colour with a native swatch beside it.
 *
 * The text input is authoritative and may be left blank, which means "inherit".
 * A colour input alone cannot express blank, and the server only accepts
 * #rgb / #rrggbb / #rrggbbaa.
 */
export function ColorField({
  value,
  onChange,
  ...rest
}: FieldProps & { value: string; onChange: (v: string) => void }) {
  return (
    <Wrap {...rest} hint={rest.hint || 'Blank uses the theme default. Format: #E86514'}>
      <div class="bm-colorfield">
        <input
          id={rest.id}
          class="bm-input"
          type="text"
          value={value}
          placeholder="#E86514"
          onInput={(e) => onChange((e.target as HTMLInputElement).value)}
        />
        <input
          class="bm-colorfield__swatch"
          type="color"
          aria-label={`${rest.label} colour picker`}
          value={/^#[0-9a-fA-F]{6}$/.test(value) ? value : '#E86514'}
          onInput={(e) => onChange((e.target as HTMLInputElement).value)}
        />
      </div>
    </Wrap>
  );
}

/** A datetime-local input bound to an RFC3339 string. */
export function DateTimeField({
  value,
  onChange,
  ...rest
}: FieldProps & { value: string; onChange: (v: string) => void }) {
  return (
    <Wrap {...rest}>
      <input
        id={rest.id}
        class="bm-input"
        type="datetime-local"
        value={value}
        onInput={(e) => onChange((e.target as HTMLInputElement).value)}
      />
    </Wrap>
  );
}
