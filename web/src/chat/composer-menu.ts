// The composer's `+` menu.
//
// What goes with the next message: what to attach, and how much allowance is
// left to send it with.
//
// Reasoning used to live here too, which was the wrong place for it. How hard
// a model thinks is part of choosing the model, not part of attaching a file,
// and having it here meant the state was set in one menu and displayed in a
// chip on the other side of the screen. It moved into the model control,
// beside send.

import { fetchUsage, windowFigures, windowPressure, type UsageSummary, type UsageWindow } from '../api/usage';
import { t } from '../i18n';
import { ICONS, el, icon, iconButton } from '../ui/dom';
import { dropdown, menuItem } from '../ui/menu';

export type Effort = 'low' | 'medium' | 'high';

export interface ReasoningState {
  enabled: boolean;
  effort: Effort;
}

export interface ComposerMenuOptions {
  onPickImages(): void;
  onPickFiles(): void;
  /** False while a turn is running, or before a model is available. */
  enabled(): boolean;
  imagesAvailable(): boolean;
}

export interface ComposerMenu {
  element: HTMLElement;
  /** Re-reads whether the composer is accepting input. */
  sync(): void;
}

export function createComposerMenu(options: ComposerMenuOptions): ComposerMenu {
  const trigger = iconButton('ai-chat-plus', ICONS.plus, t('composerMenu'), undefined, 18);

  // Usage is fetched when the menu opens rather than on a timer: it is only
  // ever read while the panel is on screen, and polling it would be a request
  // per user per interval for a number nobody is looking at.
  let usage: UsageSummary | null = null;
  let usageLoaded = false;

  const menu = dropdown(trigger, (panel, close) => {
    panel.appendChild(menuItem({
      title: t('addImage'),
      leading: icon(ICONS.image, 14),
      onSelect: () => {
        close();
        options.onPickImages();
      },
    }));

    panel.appendChild(menuItem({
      title: t('addFile'),
      sub: t('addFileHint'),
      leading: icon(ICONS.file, 14),
      onSelect: () => {
        close();
        options.onPickFiles();
      },
    }));

    const quota = el('div', 'oa-menu-quota');
    panel.appendChild(quota);
    paintQuota(quota);

    if (!usageLoaded) {
      usageLoaded = true;
      void fetchUsage()
        .then((summary) => {
          usage = summary;
          paintQuota(quota);
        })
        .catch(() => {
          // Usage is a courtesy; a failure just leaves the row out.
          paintQuota(quota);
        });
    }
  }, { groupClass: 'oa-composer-menu', menuClass: 'oa-menu-up' });

  function paintQuota(container: HTMLElement): void {
    container.textContent = '';

    if (!usageLoaded) {
      container.appendChild(el('span', 'oa-menu-quota-reset', t('loading')));
      return;
    }
    if (!usage) {
      // This build reports no usage; showing an empty frame would be worse
      // than showing nothing.
      container.remove();
      return;
    }
    if (usage.unlimited || !usage.windows.some((entry) => entry.enforced)) {
      container.appendChild(el('span', 'oa-menu-quota-reset', t('quotaUnlimited')));
      return;
    }

    for (const window of usage.windows) {
      if (!window.enforced) continue;
      container.appendChild(quotaRow(window));
    }
  }

  function quotaRow(window: UsageWindow): HTMLElement {
    const wrap = el('div');

    const row = el('div', 'oa-menu-quota-row');
    row.appendChild(el('span', null, t(windowLabel(window.kind))));

    const pressure = windowPressure(window);
    row.appendChild(el('span', 'oa-menu-quota-value', quotaValue(window, pressure)));
    wrap.appendChild(row);

    if (pressure !== null) {
      // The bar has to travel the way the figure beside it reads. A track
      // filled a tenth under the words "90% left" is two answers to one
      // question, and at a glance the shape is the one believed. So an
      // allowance phrased as what remains drains as it is spent; used, and the
      // raw figures, fill up.
      const draining = (usage?.display ?? 'absolute') === 'remaining';
      const meter = el('div', 'oa-meter');
      // Keyed to pressure rather than to the width: nearly gone is nearly gone
      // whichever direction the bar happens to be travelling.
      const fill = el('div', `oa-meter-fill${pressure >= 0.9 ? ' warn' : ''}`);
      fill.style.width = `${Math.round((draining ? 1 - pressure : pressure) * 100)}%`;
      meter.appendChild(fill);
      wrap.appendChild(meter);
    }

    wrap.appendChild(el('div', 'oa-menu-quota-reset', t('quotaResets', { when: untilText(window.resets_at) })));
    return wrap;
  }

  /**
   * The figure beside a window's name, in whichever phrasing the instance
   * chose. A percentage needs a ratio to exist; with no limit on any
   * dimension there is nothing to be a percentage of, so those windows fall
   * back to the count regardless of the setting.
   */
  function quotaValue(window: UsageWindow, pressure: number | null): string {
    const mode = usage?.display ?? 'absolute';
    if (mode !== 'absolute' && pressure !== null) {
      const percent = Math.round(pressure * 100);
      return mode === 'remaining'
        ? t('quotaRemaining', { percent: Math.max(0, 100 - percent) })
        : t('quotaUsed', { percent });
    }
    const figures = windowFigures(window);
    return figures ? `${compact(figures.used)} / ${compact(figures.limit)}` : compact(window.used_requests);
  }

  function sync(): void {
    trigger.disabled = !options.enabled();
  }

  sync();
  return { element: menu.group, sync };
}

function windowLabel(kind: UsageWindow['kind']): 'quota5h' | 'quotaWeek' | 'quotaMonth' {
  if (kind === '5h') return 'quota5h';
  if (kind === '1w') return 'quotaWeek';
  return 'quotaMonth';
}

// Thousands as "1.2k": a menu row has no space for six digits, and the exact
// figure is not what anyone reads here.
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
