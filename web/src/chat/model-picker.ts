// The header chip that chooses a model.
//
// Same chip and same menu the standalone build had — a rounded pill with the
// current model's name and a chevron, opening a list of title-plus-subtitle
// rows. What it lists changed: the models come from the server and are only
// the ones this account may actually use, so the picker cannot offer
// something the gateway would refuse.
//
// It picks models and nothing else. The controls that shape a message rather
// than route it — thinking, attachments, allowance — live in the composer's
// own menu, next to the box they affect.

import { api } from '../api/client';
import { t } from '../i18n';
import { ICONS, el, icon } from '../ui/dom';
import { dropdown, menuItem, type Dropdown } from '../ui/menu';

export interface ModelCapabilities {
  supports_reasoning: boolean;
  supports_images: boolean;
  supports_vision: boolean;
  supports_streaming: boolean;
  supports_system_prompt: boolean;
  supports_tools: boolean;
  context_window: number;
  max_output_tokens: number;
}

export interface AvailableModel extends ModelCapabilities {
  id: string;
  display_name: string;
  description: string;
  avatar: string;
  provider_name: string;
  usable?: boolean;
}

export interface ModelPickerOptions {
  /** Restores the last choice; ignored when that model is no longer offered. */
  initialModelID?: string;
  onChange(): void;
  /** Persists a changed selection to the account. */
  onPersist?(modelID: string): void;
  /** True when extended thinking is on, for the marker on the chip. */
  reasoningActive(): boolean;
}

export interface ModelPicker {
  element: HTMLElement;
  /** Fetches the list. Resolves once the chip is showing something real. */
  load(): Promise<void>;
  models(): AvailableModel[];
  current(): AvailableModel | null;
  /** Repaints the chip after something outside it changed. */
  sync(): void;
  select(modelID: string): void;
}

export function createModelPicker(options: ModelPickerOptions): ModelPicker {
  let models: AvailableModel[] = [];
  let selectedID = options.initialModelID ?? '';

  const chip = el('button', 'oa-chip placeholder');
  chip.type = 'button';
  const chipLabel = el('span', 'oa-chip-label', t('loading'));
  chip.appendChild(chipLabel);
  chip.appendChild(icon(ICONS.chevron, 13));

  const menu: Dropdown = dropdown(chip, (panel, close) => {
    if (!models.length) {
      panel.appendChild(el('p', 'oa-menu-empty', t('modelsEmpty')));
      return;
    }

    for (const model of models) {
      const isUnusable = model.usable === false;
      const sub = isUnusable
        ? `${model.description ? model.description + ' · ' : ''}${t('modelNotAllowedGroup')}`
        : (model.description || model.provider_name);

      panel.appendChild(menuItem({
        title: model.display_name,
        sub,
        disabled: isUnusable,
        active: model.id === selectedID,
        onSelect: () => {
          if (isUnusable) return;
          close();
          select(model.id);
        },
      }));
    }
  });

  function paint(): void {
    const model = current();
    chipLabel.textContent = model ? model.display_name : t('modelNone');
    chip.classList.toggle('placeholder', !model);

    // A quiet dot on the chip when extended thinking is on, so the state is
    // visible without opening either menu.
    chip.classList.toggle('reasoning', !!model?.supports_reasoning && options.reasoningActive());
    chip.title = model ? `${model.display_name} · ${model.provider_name}` : t('modelNone');
  }

  function current(): AvailableModel | null {
    return models.find((model) => model.id === selectedID) ?? null;
  }

  function select(modelID: string): void {
    const target = models.find((model) => model.id === modelID);
    if (target && target.usable === false) return;
    if (selectedID === modelID) return;
    selectedID = modelID;
    paint();
    options.onPersist?.(selectedID);
    options.onChange();
  }

  async function load(): Promise<void> {
    try {
      const { models: list } = await api.get<{ models: AvailableModel[] }>('/api/models');
      models = list;
    } catch {
      models = [];
    }

    // The remembered model may have been disabled, deleted, taken away
    // from this group, or demoted to view-only access.
    const currentModel = models.find((model) => model.id === selectedID);
    if (!currentModel || currentModel.usable === false) {
      const firstUsable = models.find((model) => model.usable !== false);
      selectedID = firstUsable?.id ?? '';
    }
    paint();
    options.onChange();
  }

  paint();

  return {
    element: menu.group,
    load,
    models: () => models,
    current,
    sync: paint,
    select,
  };
}
