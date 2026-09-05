// The chat screen: the shell, the model chip in its header, and the chat
// surface underneath.
//
// This is the host chat.ts talks to. The standalone build had the same seam —
// the chat component asked its host for a status and a way to send a turn,
// and knew nothing about providers. It still does; the host is just a
// different one.

import { renderShell } from '../app/shell';
import { navigate } from '../router';
import { currentPreferences, isAdmin, syncPreferences } from '../session';
import { t } from '../i18n';
import { ICONS, iconButton } from '../ui/dom';
import { attachResizer } from '../ui/resizer';
import { mountChat, type ChatHandle, type ChatStatus } from './chat';
import type { Effort, ReasoningState } from './composer-menu';
import { createModelPicker } from './model-picker';

const RAIL_COLLAPSED_KEY = 'obsidian-arc-rail-collapsed';

let live: ChatHandle | null = null;

/**
 * Draws the chat screen and returns the flex row it lives in, so a caller can
 * open a side panel as a column beside it — which is how /settings is drawn.
 */
export function renderChatPage(root: HTMLElement): HTMLElement {
  // Leaving the page mid-generation aborts the turn; the server still saves
  // whatever streamed before that.
  live?.destroy();
  live = null;

  const shell = renderShell(root);
  const preferences = currentPreferences();

  // Reasoning is the page's state rather than either menu's: the composer
  // menu edits it, the model chip displays it, and the model in play decides
  // whether it applies at all.
  let reasoning: ReasoningState = readReasoning(preferences);

  const picker = createModelPicker({
    ...(typeof preferences['default_model_id'] === 'string'
      ? { initialModelID: preferences['default_model_id'] }
      : {}),
    reasoningActive: () => reasoning.enabled,
    onChange: () => chat?.refreshStatus(),
    onPersist: (modelID) => syncPreferences({ default_model_id: modelID }),
  });

  shell.headerSlot.appendChild(picker.element);

  // Two affordances on one button: on a wide screen the rail is a permanent
  // column and this slides it away; below the breakpoint where the rail
  // becomes an overlay, sliding it would do nothing useful, so it defers to
  // the chat's own overlay toggle.
  const railToggle = iconButton('oa-icon-btn', ICONS.menu, t('railToggle'), () => {
    if (window.matchMedia('(max-width: 900px)').matches) {
      chat?.toggleHistory();
      return;
    }
    const collapsed = shell.body.classList.toggle('rail-collapsed');
    try {
      localStorage.setItem(RAIL_COLLAPSED_KEY, collapsed ? '1' : '');
    } catch {
      // Best effort; the rail still moved.
    }
  }, 17);
  shell.leadingSlot.appendChild(railToggle);

  try {
    if (localStorage.getItem(RAIL_COLLAPSED_KEY) === '1') shell.body.classList.add('rail-collapsed');
  } catch {
    // Storage disabled: the rail starts open.
  }

  const status = (): ChatStatus => {
    const model = picker.current();
    // A model that cannot reason must not carry a stale toggle: the gateway
    // would drop it, and the chip would be claiming something untrue.
    const canReason = !!model?.supports_reasoning;
    return {
      configured: model !== null,
      vision: !!model?.supports_images,
      reasoningAvailable: canReason,
      reasoningEnabled: canReason && reasoning.enabled,
      reasoningEffort: reasoning.effort,
      modelID: model?.id ?? '',
      canAdminister: isAdmin(),
    };
  };

  const chat = mountChat({
    root: shell.body,
    getStatus: status,
    onOpenSetup: () => navigate('/admin/providers'),
    onReasoningChange: (next) => {
      reasoning = next;
      picker.sync();
      syncPreferences({ reasoning_enabled: next.enabled, reasoning_effort: next.effort });
    },
  });
  live = chat;

  // The rail's width drives its own collapsed margin as well as its size, so
  // the handle writes the custom property rather than a width.
  const rail = shell.body.querySelector<HTMLElement>('.ai-chat-sidebar');
  if (rail) {
    attachResizer({
      target: rail,
      edge: 'right',
      cssVariable: '--ai-rail-width',
      styleTarget: shell.body,
      storageKey: 'obsidian-arc-rail-width',
      min: 190,
      max: 460,
      fallback: 260,
      label: t('resizeRail'),
    });
  }

  void picker.load().then(() => {
    chat.refreshStatus();
    chat.focus();
  });

  return shell.body;
}

function readReasoning(preferences: Record<string, unknown>): ReasoningState {
  const effort = preferences['reasoning_effort'];
  return {
    enabled: preferences['reasoning_enabled'] === true,
    effort: effort === 'low' || effort === 'high' ? (effort as Effort) : 'medium',
  };
}
