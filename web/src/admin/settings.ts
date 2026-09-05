// Instance settings.
//
// A short list, because every entry here is a decision an operator has to
// understand before they change it. Anything with a sensible answer for every
// deployment is a constant, not a setting.

import { ApiError } from '../api/client';
import { button, clear, el } from '../ui/dom';
import { numberField, section, selectField, switchField, textArea, textField } from '../ui/form';
import { adminApi } from './api';
import { failure, type AdminView } from './admin-page';

export async function renderSettings(view: AdminView): Promise<void> {
  view.setTitle('Settings');

  let data;
  try {
    data = await adminApi.settings();
  } catch (error) {
    failure(view, error);
    return;
  }

  const values = data.settings;

  const siteName = textField({
    label: 'Site name',
    value: values['site.name'] ?? '',
    hint: 'Shown in the header and on the sign-in page.',
    maxLength: 60,
  });

  const description = textArea({
    label: 'Sign-in note',
    value: values['site.description'] ?? '',
    rows: 2,
    hint: 'An optional line on the sign-in card — who this server is for, or where to ask for an account.',
  });

  const registration = switchField({
    label: 'Anyone can register',
    value: values['registration.enabled'] === 'true',
    hint: 'With this off, only an administrator can create accounts. An empty instance always accepts the first one.',
  });

  const defaultGroup = selectField({
    label: 'New accounts join',
    value: values['registration.default_group'] ?? '',
    options: [
      { value: '', label: 'The default group' },
      ...data.groups.map((group) => ({ value: group.id, label: group.name })),
    ],
  });

  const adminBypass = switchField({
    label: 'Administrators ignore usage limits',
    value: values['quota.admins_bypass'] === 'true',
    hint: 'Leave this on unless someone else operates the server: an administrator who runs out of allowance cannot raise it back.',
  });

  const systemPrompt = textArea({
    label: 'Instance system prompt',
    value: values['chat.default_system_prompt'] ?? '',
    rows: 4,
    hint: 'Prepended to every conversation on this server, for models that take one. Leave empty for none.',
  });

  const maxTurns = numberField({
    label: 'Turns re-sent per request',
    value: Number(values['chat.max_turns'] ?? 40),
    min: 2,
    max: 200,
    hint: 'How much of a conversation goes back to the provider each time. Higher remembers more and costs more, every turn.',
  });

  clear(view.actions);
  const save = button('oa-btn primary', 'Save', () => void submit());
  view.actions.appendChild(save);

  clear(view.body);
  const form = el('div', 'oa-drawer-body');
  form.style.padding = '0';
  form.style.overflow = 'visible';

  form.appendChild(section('Identity'));
  form.appendChild(siteName.element);
  form.appendChild(description.element);

  form.appendChild(section('Accounts'));
  form.appendChild(registration.element);
  form.appendChild(defaultGroup.element);

  form.appendChild(section('Chat'));
  form.appendChild(systemPrompt.element);
  form.appendChild(maxTurns.element);

  form.appendChild(section('Limits'));
  form.appendChild(adminBypass.element);

  const flash = el('p', 'oa-drawer-flash');
  form.appendChild(flash);
  view.body.appendChild(form);

  async function submit(): Promise<void> {
    save.disabled = true;
    save.textContent = 'Saving…';
    flash.classList.remove('visible');

    try {
      await adminApi.saveSettings({
        'site.name': siteName.value(),
        'site.description': description.value(),
        'registration.enabled': String(registration.value()),
        'registration.default_group': defaultGroup.value(),
        'quota.admins_bypass': String(adminBypass.value()),
        'chat.default_system_prompt': systemPrompt.value(),
        'chat.max_turns': String(maxTurns.value() ?? 40),
      });
      save.textContent = 'Saved';
      window.setTimeout(() => { save.textContent = 'Save'; }, 1500);
    } catch (error) {
      flash.textContent = error instanceof ApiError ? error.message : String(error);
      flash.classList.add('visible');
      save.textContent = 'Save';
    } finally {
      save.disabled = false;
    }
  }
}
