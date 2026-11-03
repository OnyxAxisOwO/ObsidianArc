import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, ref, type App } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import * as auth from '../src/api/auth';
import * as oauth from '../src/api/oauth';
import OaLegalDocuments from '../src/components/OaLegalDocuments.vue';
import AuthView from '../src/views/AuthView.vue';
import CompleteSignupView from '../src/views/CompleteSignupView.vue';
import { site, siteInfo, forget } from '../src/stores/session';
import { changeLanguage, t } from '../src/composables/useI18n';
import { ApiError } from '../src/api/client';
import { refusalText } from '../src/lib/refusal';
import { settle } from './helpers';

const documents: auth.LegalDocument[] = [
  { id: 'terms', title: 'Community terms', body: '**Read carefully**\n\n<script>alert(1)</script>\n\n[bad](javascript:alert(1))', required: true },
  { id: 'privacy', title: '', body: '# Privacy', required: false },
];
let app: App | undefined;
let host: HTMLElement;
beforeEach(async () => {
  await changeLanguage('en');
  forget();
  site.value = { ...siteInfo.value, setup_required: false, registration_enabled: true, legal_documents: documents };
  host = document.createElement('div');
  document.body.append(host);
});
afterEach(() => {
  app?.unmount();
  app = undefined;
  host.remove();
  site.value = null;
  forget();
  vi.restoreAllMocks();
});
async function mount(component: typeof AuthView | typeof CompleteSignupView, path: string): Promise<void> {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path, component, props: { mode: 'register' } }] });
  await router.push(path);
  app = createApp({ render: () => h(component, { mode: 'register' }) });
  app.use(router);
  app.mount(host);
  await settle();
}
function fill(selector: string, value: string): void {
  const input = host.querySelector<HTMLInputElement>(selector)!;
  input.value = value;
  input.dispatchEvent(new Event('input', { bubbles: true }));
}
function agree(): void {
  const checkbox = host.querySelector<HTMLInputElement>('.oa-legal-document input')!;
  checkbox.checked = true;
  checkbox.dispatchEvent(new Event('change', { bubbles: true }));
}
async function submit(): Promise<void> {
  host.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
  await settle();
}

describe('registration legal documents', () => {
  it('renders safe Markdown, default titles and only the required checkbox', async () => {
    const accepted = ref<Record<string, boolean>>({});
    app = createApp({ render: () => h(OaLegalDocuments, { documents, modelValue: accepted.value, 'onUpdate:modelValue': (value) => { accepted.value = value; } }) });
    app.mount(host);
    await settle();
    expect(host.querySelector('strong')?.textContent).toBe('Read carefully');
    expect(host.textContent).toContain('Privacy policy');
    expect(host.querySelectorAll('input[type="checkbox"]')).toHaveLength(1);
    expect(host.querySelector('script')).toBeNull();
    expect(host.querySelector('a[href^="javascript:"]')).toBeNull();
    agree();
    expect(accepted.value).toEqual({ terms: true });
  });

  it('blocks password registration locally, then sends explicit agreement', async () => {
    const registration = vi.spyOn(auth, 'register').mockRejectedValue(new ApiError(409, 'username_taken', 'Taken'));
    await mount(AuthView, '/register');
    fill('input[autocomplete="username"]', 'member');
    fill('input[type="password"]', 'a-good-password');
    await submit();
    expect(registration).not.toHaveBeenCalled();
    expect(host.textContent).toContain(t('legalAcceptanceRequired'));
    agree();
    await submit();
    expect(registration).toHaveBeenCalledWith(expect.objectContaining({ legalAccepted: { terms: true } }));
  });

  it('enforces agreement on the third-party completion form too', async () => {
    vi.spyOn(oauth, 'fetchPendingSignup').mockResolvedValue({
      provider: 'github', provider_name: 'GitHub', login: 'member', email: '', needs: { email: false },
      needs_password: false, email_domains: [], verify_email: false, legal_documents: documents,
    });
    const complete = vi.spyOn(oauth, 'completeSignup').mockRejectedValue(new ApiError(409, 'username_taken', 'Taken'));
    await mount(CompleteSignupView, '/oauth/complete');
    await submit();
    expect(complete).not.toHaveBeenCalled();
    expect(host.textContent).toContain(t('legalAcceptanceRequired'));
    agree();
    await submit();
    expect(complete).toHaveBeenCalledWith(expect.objectContaining({ legalAccepted: { terms: true } }));
  });

  it('translates a server refusal on a stale or tampered form', async () => {
    await changeLanguage('zh');
    expect(refusalText(new ApiError(400, 'legal_acceptance_required', 'English fallback'))).toBe(t('legalAcceptanceRequired'));
  });
});
