import { useCallback, useEffect, useState } from 'preact/hooks';
import { api } from '../../lib/api';
import type { ScheduleDecision, ScheduleOverride, ScheduleRule, User } from '../../lib/types';
import { Card, ErrorNote, StatusPill, formatWhen } from '../ui';

interface ScheduleResponse {
  rules: ScheduleRule[];
  overrides: ScheduleOverride[];
  decision: ScheduleDecision;
  timezone: string;
  server_time: string;
  next_change: string;
}

const DAYS = ['Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday', 'Sunday'];

export function ScheduleView({ user, refreshKey }: { user: User; refreshKey: number }) {
  const [data, setData] = useState<ScheduleResponse | null>(null);
  const [error, setError] = useState('');
  const [saving, setSaving] = useState(false);

  const canEdit = user.role === 'editor' || user.role === 'admin';

  const load = useCallback(async (signal?: AbortSignal) => {
    try {
      setData(await api.get<ScheduleResponse>('/api/v1/schedule', signal));
      setError('');
    } catch (e) {
      if (signal?.aborted) return;
      setError(e instanceof Error ? e.message : 'Could not load the schedule');
    }
  }, []);

  useEffect(() => {
    const ctrl = new AbortController();
    void load(ctrl.signal);
    return () => ctrl.abort();
  }, [load, refreshKey]);

  const saveRule = useCallback(
    async (weekday: number, patch: Partial<ScheduleRule>) => {
      if (!data || !canEdit) return;
      const existing = data.rules.find((r) => r.kind === 'weekly' && r.weekday === weekday);
      setSaving(true);
      try {
        await api.post('/api/v1/schedule/rules', {
          id: existing?.id ?? 0,
          kind: 'weekly',
          weekday,
          enabled: patch.enabled ?? existing?.enabled ?? true,
          on_time: patch.on_time ?? existing?.on_time ?? '08:00',
          off_time: patch.off_time ?? existing?.off_time ?? '17:00',
          label: existing?.label ?? '',
        });
        await load();
      } catch (e) {
        setError(e instanceof Error ? e.message : 'Could not save the rule');
      } finally {
        setSaving(false);
      }
    },
    [data, canEdit, load],
  );

  const override = useCallback(
    async (mode: 'wake' | 'sleep', minutes: number) => {
      try {
        await api.post('/api/v1/schedule/override', { mode, duration_minutes: minutes });
        await load();
      } catch (e) {
        setError(e instanceof Error ? e.message : 'Could not set the override');
      }
    },
    [load],
  );

  const clearOverride = useCallback(async () => {
    try {
      await api.del('/api/v1/schedule/override');
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Could not clear the override');
    }
  }, [load]);

  if (error && !data) return <ErrorNote message={error} />;
  if (!data) return <p class="bm-muted">Loading schedule…</p>;

  const weekly = DAYS.map((name, i) => ({
    name,
    weekday: i,
    rule: data.rules.find((r) => r.kind === 'weekly' && r.weekday === i),
  }));

  return (
    <div class="bm-view">
      <header class="bm-view__head">
        <div>
          <h1 class="bm-view__title">Schedule</h1>
          <p class="bm-view__sub bm-muted">
            {data.timezone} · server time {formatWhen(data.server_time)}
          </p>
        </div>
      </header>

      {error && <ErrorNote message={error} />}

      <Card title="Right now">
        <div class="bm-row bm-row--gap">
          <StatusPill tone={data.decision.on ? 'ok' : 'idle'} label={data.decision.on ? 'Display awake' : 'Display in standby'} />
          <span class="bm-muted">{data.decision.reason}</span>
        </div>
        {data.next_change && (
          <p class="bm-small bm-muted">Next change {formatWhen(data.next_change)}.</p>
        )}

        {canEdit && (
          <div class="bm-row bm-row--gap bm-row--wrap" style={{ marginTop: '16px' }}>
            <button class="bm-btn bm-btn--secondary bm-btn--sm" type="button" onClick={() => override('wake', 60)}>
              Wake for 1 hour
            </button>
            <button class="bm-btn bm-btn--secondary bm-btn--sm" type="button" onClick={() => override('wake', 240)}>
              Wake for 4 hours
            </button>
            <button class="bm-btn bm-btn--secondary bm-btn--sm" type="button" onClick={() => override('sleep', 60)}>
              Sleep for 1 hour
            </button>
            {data.overrides.length > 0 && (
              <button class="bm-btn bm-btn--ghost bm-btn--sm" type="button" onClick={clearOverride}>
                Clear override
              </button>
            )}
          </div>
        )}

        {data.overrides.length > 0 && (
          <p class="bm-small bm-muted" style={{ marginTop: '12px' }}>
            Manual {data.overrides[0]?.mode} active until {formatWhen(data.overrides[0]?.expires_at ?? '')}.
            Overrides expire on their own so the screen is never left on by accident.
          </p>
        )}
      </Card>

      <Card title="Weekly schedule">
        <p class="bm-small bm-muted">
          Times are in {data.timezone} and follow daylight saving automatically. To run past midnight,
          set the off time earlier than the on time — for example 18:00 to 02:00.
        </p>

        <table class="bm-table">
          <thead>
            <tr>
              <th scope="col">Day</th>
              <th scope="col">Active</th>
              <th scope="col">On</th>
              <th scope="col">Off</th>
            </tr>
          </thead>
          <tbody>
            {weekly.map(({ name, weekday, rule }) => {
              const enabled = rule?.enabled ?? false;
              const crossesMidnight =
                enabled && rule?.on_time && rule?.off_time && rule.off_time <= rule.on_time;
              return (
                <tr key={weekday}>
                  <th scope="row">{name}</th>
                  <td>
                    <label class="bm-switch">
                      <input
                        type="checkbox"
                        checked={enabled}
                        disabled={!canEdit || saving}
                        onChange={(e) =>
                          saveRule(weekday, { enabled: (e.target as HTMLInputElement).checked })
                        }
                      />
                      <span class="bm-visually-hidden">{`Display active on ${name}`}</span>
                      <span class="bm-switch__track" aria-hidden="true" />
                    </label>
                  </td>
                  <td>
                    <input
                      class="bm-input bm-input--time"
                      type="time"
                      value={rule?.on_time ?? '08:00'}
                      disabled={!canEdit || !enabled || saving}
                      aria-label={`${name} on time`}
                      onChange={(e) =>
                        saveRule(weekday, { on_time: (e.target as HTMLInputElement).value })
                      }
                    />
                  </td>
                  <td>
                    <input
                      class="bm-input bm-input--time"
                      type="time"
                      value={rule?.off_time ?? '17:00'}
                      disabled={!canEdit || !enabled || saving}
                      aria-label={`${name} off time`}
                      onChange={(e) =>
                        saveRule(weekday, { off_time: (e.target as HTMLInputElement).value })
                      }
                    />
                    {crossesMidnight && (
                      <span class="bm-badge bm-small" title="This window continues past midnight">
                        +1 day
                      </span>
                    )}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </Card>
    </div>
  );
}
