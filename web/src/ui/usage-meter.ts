// The allowance bars: which window, how much of it has gone, and when it
// comes back.
//
// Two screens draw them and have nothing else in common — the composer's
// menu, where someone reads their own allowance before sending, and the
// administration drawer, where someone reads another account's. The
// decisions that make the bars honest are the same in both places and are
// made here: which dimension a window is judged on, which way the bar
// travels, and how the figure beside it is phrased.

import { windowFigures, windowPressure, type UsageDisplay, type UsageWindow } from '../api/usage';
import { t } from '../i18n';
import { el } from './dom';

/**
 * One window, as a labelled figure over a bar.
 *
 * A window with no limit on any dimension gets no bar: there is nothing for
 * it to be a fraction of, and a full-width track would say "at the ceiling"
 * about an account that has none.
 */
export function usageWindow(window: UsageWindow, display: UsageDisplay = 'absolute'): HTMLElement {
  const wrap = el('div');

  const row = el('div', 'oa-usage-row');
  row.appendChild(el('span', null, t(windowLabel(window.kind))));

  const pressure = windowPressure(window);
  row.appendChild(el('span', 'oa-usage-value', usageValue(window, pressure, display)));
  wrap.appendChild(row);

  if (pressure !== null) {
    // The bar has to travel the way the figure beside it reads. A track
    // filled a tenth under the words "90% left" is two answers to one
    // question, and at a glance the shape is the one believed. So an
    // allowance phrased as what remains drains as it is spent; used, and the
    // raw figures, fill up.
    const draining = display === 'remaining';
    const meter = el('div', 'oa-meter');
    // Keyed to pressure rather than to the width: nearly gone is nearly gone
    // whichever direction the bar happens to be travelling.
    const fill = el('div', `oa-meter-fill${pressure >= 0.9 ? ' warn' : ''}`);
    fill.style.width = `${Math.round((draining ? 1 - pressure : pressure) * 100)}%`;
    meter.appendChild(fill);
    wrap.appendChild(meter);
  }

  wrap.appendChild(el('div', 'oa-usage-reset', t('quotaResets', { when: untilText(window.resets_at) })));
  return wrap;
}

/**
 * The figure beside a window's name, in whichever phrasing the instance
 * chose. A percentage needs a ratio to exist; with no limit on any dimension
 * there is nothing to be a percentage of, so those windows fall back to the
 * count regardless of the setting.
 */
function usageValue(window: UsageWindow, pressure: number | null, display: UsageDisplay): string {
  if (display !== 'absolute' && pressure !== null) {
    const percent = Math.round(pressure * 100);
    return display === 'remaining'
      ? t('quotaRemaining', { percent: Math.max(0, 100 - percent) })
      : t('quotaUsed', { percent });
  }
  const figures = windowFigures(window);
  return figures ? `${compact(figures.used)} / ${compact(figures.limit)}` : compact(window.used_requests);
}

function windowLabel(kind: UsageWindow['kind']): 'quota5h' | 'quotaWeek' | 'quotaMonth' {
  if (kind === '5h') return 'quota5h';
  if (kind === '1w') return 'quotaWeek';
  return 'quotaMonth';
}

// Thousands as "1.2k": one of these rows has no space for six digits, and the
// exact figure is not what anyone reads here.
function compact(value: number): string {
  if (value < 1000) return String(Math.round(value * 10) / 10);
  if (value < 1_000_000) return `${Math.round(value / 100) / 10}k`;
  return `${Math.round(value / 100_000) / 10}M`;
}

function untilText(at: number): string {
  const minutes = Math.max(0, Math.round((at - Date.now()) / 60000));
  if (minutes < 60) return t('inMinutes', { count: minutes });
  const hours = Math.round(minutes / 60);
  if (hours < 48) return t('inHours', { count: hours });
  return t('inDays', { count: Math.round(hours / 24) });
}
