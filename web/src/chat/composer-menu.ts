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

import { fetchUsage, type UsageSummary } from '../api/usage';
import { t } from '../i18n';
import { ICONS, el, icon, iconButton } from '../ui/dom';
import { dropdown, menuItem } from '../ui/menu';
import { usageWindow } from '../ui/usage-meter';

/**
 * Which amount of thinking, by the id of the tier that names it.
 *
 * A plain string rather than the three words it used to be: a model may
 * define its own tiers, so what is valid here is decided per model — by the
 * list the reader was offered, which the gateway checks the value against.
 */
export type Effort = string;

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
      container.appendChild(usageWindow(window, usage.display ?? 'absolute'));
    }
  }

  function sync(): void {
    trigger.disabled = !options.enabled();
  }

  sync();
  return { element: menu.group, sync };
}
