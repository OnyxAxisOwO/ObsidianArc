// The types of a plugin package's browser half — the declaration web/ui.js
// returns, and the host it is handed. Kept beside the SDK so an author gets
// completion without the server's source; the server's own copy is
// web/src/plugins/types.ts, and a test there holds the two together.


/** A sentence in the reader's language, resolved when it is drawn. */
export type Text = () => string;

/** Where a /api/site plugin block arrives: whatever the server's Extend said. */
export type PluginConfig = Record<string, unknown>;

/**
 * An account column the server-side plugin defined (user.DefineField). The
 * server says whether a form asks for it — the "fields" block of /api/site —
 * and this says how the field looks and what it accepts.
 */
export interface AccountFieldSpec {
  label: Text;
  /** Shown instead of label where the field may be left empty. */
  optionalLabel: Text;
  placeholder?: Text;
  hint?: Text;
  maxLength?: number;
  inputMode?: 'text' | 'numeric' | 'email';
  /** Checked before submitting, so the answer is immediate. The server decides. */
  pattern?: RegExp;
  /** What to say when the pattern refuses, or the server answers invalid_<key>. */
  invalid: Text;
  /** The server's <key>_required. */
  required: Text;
  /** The server's <key>_taken. */
  taken: Text;
}

export type GuardAction = 'register' | 'login';

/**
 * A check the server-side plugin stands in front of sign-up or sign-in
 * (auth.Service.AddGuard). The token it produces rides in the request's
 * "guards" object under the guard's name.
 */
export interface GuardSpec {
  /** The name the server's guard reads its token under. */
  name: string;
  /** Whether this door asks for a token now, from the plugin's site block. */
  active(action: GuardAction, config: PluginConfig): boolean;
  /**
   * Called as soon as the card opens on a door that is active, so a service
   * that scores behaviour has something to score by submit time.
   */
  prepare?(action: GuardAction, config: PluginConfig): void;
  /** The token for one submission. Rejects when none can be had. */
  token(action: GuardAction, config: PluginConfig, form?: HTMLFormElement): Promise<string>;
  /** What the submit button says while token() runs. */
  checking: Text;
  /** What the card says when token() rejects. */
  /** What the card says when token() rejects; the rejection is passed for a guard with more than one way to fail. */
  failed: (failure?: unknown) => string;
}

/** One control in a settings section. */
export type SettingControl =
  | { kind: 'text'; key: string; label: Text; hint?: Text; placeholder?: string }
  /** Write-only: shown empty with the stored mask as its placeholder, and sent only when typed in. */
  | { kind: 'secret'; key: string; label: Text; hint?: Text; placeholder?: string }
  | { kind: 'switch'; key: string; label: Text; hint?: Text }
  | { kind: 'select'; key: string; label: Text; hint?: Text; options: Array<{ value: string; label: Text }> };

/**
 * Where a card or a list goes: one of the core pages that has room for them,
 * or the slug of one of the plugin's own pages (see AdminPluginPage).
 */
export type PluginPlacement = 'security' | 'invites' | `plugin:${string}`;

/**
 * A card of settings on one of the backoffice's workbench pages. The page
 * loads and saves these keys with its own; the section is only a layout.
 */
export interface SettingsSection {
  /** The card's anchor, unique across the page. */
  id: string;
  page: PluginPlacement;
  /** The workbench category it is listed under (a group id on that page). */
  category: string;
  /** Which of the page's two columns, where it has two. */
  column: 0 | 1;
  title: Text;
  hint?: Text;
  icon?: OaIcon;
  /** Extra words the page's search should find this card by. */
  keywords?: string[];
  controls: SettingControl[];
  /** Default for each key, used when the server sends none. */
  defaults: Record<string, string>;
}

/** One control in an action card. */
export type ActionControl =
  | { kind: 'text'; key: string; label: Text; hint?: Text; placeholder?: string; required?: boolean }
  | { kind: 'select'; key: string; label: Text; hint?: Text; options: Array<{ value: string; label: Text }> }
  | { kind: 'datetime'; key: string; label: Text; hint?: Text; presets?: boolean; required?: boolean };

/**
 * An action card on a workbench page: inputs and an action button that executes
 * an operation (such as a mass card grant or batch task) and reports back.
 */
export interface ActionCardSpec {
  id: string;
  page: PluginPlacement;
  title: Text;
  hint?: Text;
  icon?: OaIcon;
  keywords?: string[];
  controls: ActionControl[];
  defaults?: Record<string, string>;
  button: {
    label: Text;
    /** Optional confirmation question or title. */
    confirm?: (draft: Record<string, string>) => string;
    /** Optional label shown while the button is armed. */
    armedLabel?: Text;
    danger?: boolean;
    run(draft: Record<string, string>): Promise<string>;
  };
}

/** What a list cell shows. Masked values are passed through the backoffice's safe mode. */
export interface ListCell {
  title: string;
  sub?: string;
  /** Mask title and sub as account identifiers while safe mode is on. */
  mask?: boolean;
  badge?: { tone: 'default' | 'muted' | 'danger' };
}

/** A read-only, paged table on a backoffice page. */
export interface AdminListSpec {
  id: string;
  page: PluginPlacement;
  title: Text;
  hint?: Text;
  icon?: OaIcon;
  empty: Text;
  keywords?: string[];
  columns: Array<{ key: string; header: Text; width?: string; secondary?: boolean }>;
  load(offset: number, limit: number): Promise<{ rows: Array<Record<string, unknown>>; total: number }>;
  cell(key: string, row: Record<string, unknown>): ListCell;
}

/** A group of buttons in the backoffice's account panel. */
export interface UserActionSpec {
  id: string;
  title: Text;
  hint?: Text;
  buttons: Array<{
    label: Text;
    /** The in-place confirmation, given the account's (masked) name. */
    confirm: (name: string) => string;
    danger?: boolean;
    /** Resolves to what the panel says afterwards. */
    run(userId: string): Promise<string>;
    /** The account is gone afterwards; close the panel. */
    closesPanel?: boolean;
  }>;
}

/**
 * A page of the plugin's own in the backoffice's rail, at /admin/<slug>.
 * The core draws it: the settings sections and lists placed on
 * `plugin:<slug>`, loaded and saved the way a core page's are. A plugin that
 * only lends a card to an existing page needs none.
 */
export interface AdminPluginPage {
  /** The path segment; conventionally the plugin's name. */
  slug: string;
  title: Text;
  hint?: Text;
  icon?: OaIcon;
  /** The grant, or comma-separated grants any one of which opens it. */
  permission: string;
  /** Extra words the backoffice's search should find the page by. */
  keywords?: string[];
}

/**
 * Something the account must type before a key is made. The word is drawn
 * rather than written into the page, so it cannot be selected and copied — it
 * has to be read and typed, which is the whole of what this asks. The prompt
 * does not contain the word: the word is drawn under it.
 */
export interface KeyConfirmation {
  title: Text;
  body: Text;
  prompt: Text;
  /** What has to be typed, compared without regard to case or surrounding space. */
  word: Text;
  /** The button that goes on, once the word has been typed. */
  proceed: Text;
}

/**
 * What a plugin adds to the screen where API keys are made (views/KeysPanel.vue).
 * The confirmation is asked before every key, not once: nothing is remembered.
 */
export interface KeyIssuingSpec {
  /** A line at the top of the screen. */
  notice?: Text;
  confirmation?: KeyConfirmation;
}

/** A bell notification, worded from its kind and params. */
export interface NotificationText {
  title: string;
  body: string;
}

/** A drawing component from the page's icon set; pass it to a spec's `icon`. */
export type OaIcon = (props: { size?: number }) => unknown;

/** What the API client answers with when a request fails. */
export declare class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly details: Record<string, unknown>;
}

/**
 * What the page lends a plugin when it calls the default export of its
 * web/ui.js: the whole of what a package's browser half can reach. Nothing
 * else is importable — it is a file the page fetches at run time.
 */
export interface PluginHost {
  api: {
    get<T>(path: string): Promise<T>;
    post<T>(path: string, body?: unknown): Promise<T>;
    put<T>(path: string, body?: unknown): Promise<T>;
    patch<T>(path: string, body?: unknown): Promise<T>;
    delete<T>(path: string): Promise<T>;
  };
  ApiError: typeof ApiError;
  /** The icon set, by export name: `host.icons.IconUsers`. */
  icons: Record<string, OaIcon>;
  /** A two-language dictionary: `strings({ hello: 'Hello' }, { hello: '你好' })` returns a lookup. */
  strings<const E extends Record<string, string>>(
    en: E,
    zh: Record<keyof E, string>,
  ): (key: keyof E, vars?: Record<string, string | number>) => string;
  format: { absoluteTime(ms: number): string };
  language(): 'en' | 'zh';
}

/** What web/ui.js's default export is. */
export type PluginFactory = (host: PluginHost) => ArcPlugin | Promise<ArcPlugin>;

export interface ArcPlugin {
  name: string;
  /** Drawn on its card on the plugins screen. */
  icon?: OaIcon;
  fields?: Record<string, AccountFieldSpec>;
  guards?: GuardSpec[];
  /** Extra values for registration.captcha_mode (settings.AddCaptchaMode). */
  captchaModes?: Array<{ value: string; label: Text }>;
  /** API error codes this plugin's endpoints and guards answer with. */
  refusals?: Record<string, Text>;
  /** ?oauth_error= codes a sign-in can come back with. */
  oauthErrors?: Record<string, Text>;
  notifications?: Record<string, (params: Record<string, unknown>) => NotificationText>;
  /** Security log event names, and the reasons recorded beside them. */
  securityEvents?: Record<string, Text>;
  securityReasons?: Record<string, Text>;
  /** A line under an invitee in an inviter's own list, or null. */
  inviteeNote?: (invitee: Record<string, unknown>) => string | null;
  /**
   * Sign-in providers whose connection this plugin makes permanent
   * (oauth.Service.BindSubject), by provider id: why the settings screen
   * draws no remove button, and what a refused removal says.
   */
  pinnedProviders?: Record<string, { hint: Text; refused: Text }>;
  settings?: SettingsSection[];
  actionCards?: ActionCardSpec[];
  lists?: AdminListSpec[];
  userActions?: UserActionSpec[];
  adminPages?: AdminPluginPage[];
  /** A notice and a confirmation on the API keys screen. */
  keyIssuing?: KeyIssuingSpec;
}
