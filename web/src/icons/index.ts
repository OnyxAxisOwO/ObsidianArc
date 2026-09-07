// The icon set, as components.
//
// Built on lucide-vue-next: its `createLucideIcon` factory, its `Icon`
// renderer and its default attributes (24×24 box, no fill, currentColor
// stroke, round caps and joins) are what every glyph below is drawn with, and
// `Check` and `ChevronDown` come from the library outright because their
// geometry is already exactly what this interface uses.
//
// The other thirty-six carry this project's own path data rather than
// lucide's current drawings. That is not a snub of the library — it is the
// only way to keep this port pixel-for-pixel identical to the build it
// replaces. Lucide has redrawn most of these since the set was first copied:
// its Menu is three <line>s from x=4 to x=20 where this one is three <path>s
// from x=3 to x=21, its Sun has a radius-4 circle where this one has a
// radius-5 arc, its Trash2 has a rounded bin where this one has a
// straight-sided one. Every one of those is a visible change, and "replicate
// the existing frontend exactly" and "adopt lucide's redraw" cannot both be
// true. The paths win; the library still supplies the machinery, and moving a
// glyph to lucide's own drawing is now a one-line change in this file.
//
// Sizes are given at the call site the way they were before. The wrapper
// defaults to 16 and marks every icon aria-hidden, because an icon here is
// never the accessible name of anything — the control around it carries that.

import { h, type FunctionalComponent } from 'vue';
import { Check as LucideCheck, ChevronDown as LucideChevronDown, createLucideIcon } from 'lucide-vue-next';

/** One [tag, attributes] pair, as lucide's factory takes them. */
type IconNode = Array<[string, Record<string, string>]>;

export interface IconProps {
  size?: number | string;
  strokeWidth?: number | string;
  color?: string;
}

export type OaIcon = FunctionalComponent<IconProps>;

type AnyComponent = FunctionalComponent<Record<string, unknown>>;

/**
 * A lucide component with this interface's defaults already on it.
 *
 * Wrapping rather than repeating the defaults at every call site is what
 * keeps `<IconTrash :size="13" />` the whole story at the point of use, the
 * way `icon(ICONS.trash, 13)` was.
 */
function wrap(base: AnyComponent): OaIcon {
  const icon: OaIcon = (props) => h(base, { size: 16, 'aria-hidden': 'true', ...props });
  return icon;
}

function draw(name: string, paths: readonly string[]): OaIcon {
  const node: IconNode = paths.map((d, index) => ['path', { d, key: `p${index}` }]);
  return wrap(createLucideIcon(name, node) as AnyComponent);
}

export const IconMenu = draw('Menu', ['M3 6h18', 'M3 12h18', 'M3 18h18']);
export const IconPlus = draw('Plus', ['M12 5v14', 'M5 12h14']);
export const IconClose = draw('X', ['M18 6L6 18', 'M6 6l12 12']);
export const IconBell = draw('Bell', [
  'M18 8a6 6 0 1 0-12 0c0 7-3 9-3 9h18s-3-2-3-9',
  'M13.7 21a2 2 0 0 1-3.4 0',
]);
export const IconInfo = draw('Info', ['M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18Z', 'M12 16v-5', 'M12 8h.01']);
// Zero-length segments, drawn as dots by the set's round linecap.
export const IconDots = draw('MoreHorizontal', ['M5 12h.01', 'M12 12h.01', 'M19 12h.01']);
export const IconExpand = draw('Maximize', [
  'M8 3H5a2 2 0 0 0-2 2v3',
  'M16 3h3a2 2 0 0 1 2 2v3',
  'M8 21H5a2 2 0 0 1-2-2v-3',
  'M16 21h3a2 2 0 0 0 2-2v-3',
]);
export const IconCollapse = draw('Minimize', [
  'M3 8h3a2 2 0 0 0 2-2V3',
  'M21 8h-3a2 2 0 0 1-2-2V3',
  'M3 16h3a2 2 0 0 1 2 2v3',
  'M21 16h-3a2 2 0 0 0-2 2v3',
]);
// Identical to lucide's own drawing, so it is lucide's own drawing.
export const IconCheck = wrap(LucideCheck as unknown as AnyComponent);
export const IconChevron = wrap(LucideChevronDown as unknown as AnyComponent);
export const IconChevronRight = draw('ChevronRight', ['m9 6 6 6-6 6']);
export const IconTrash = draw('Trash', ['M3 6h18', 'M8 6V4h8v2', 'M19 6l-1 14H6L5 6', 'M10 11v6', 'M14 11v6']);
export const IconKey = draw('Key', [
  'M15 7a5 5 0 1 1-4.5 7.2L9 15.7V18H6.5v2.5H3v-3.2l6.5-6.5A5 5 0 0 1 15 7Z',
  'M16.5 10.5h.01',
]);
export const IconLock = draw('Lock', [
  'M19 11H5a2 2 0 0 0-2 2v7a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7a2 2 0 0 0-2-2Z',
  'M7 11V7a5 5 0 0 1 10 0v4',
]);
export const IconCopy = draw('Copy', [
  'M9 9h10v10a2 2 0 0 1-2 2H9a2 2 0 0 1-2-2V9Z',
  'M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1',
]);
export const IconPaperclip = draw('Paperclip', [
  'M21.44 11.05l-9.19 9.19a6 6 0 0 1-8.49-8.49l9.19-9.19a4 4 0 0 1 5.66 5.66l-9.2 9.19a2 2 0 0 1-2.83-2.83l8.49-8.48',
]);
export const IconSend = draw('ArrowUp', ['M12 19V5', 'M5 12l7-7 7 7']);
export const IconStop = draw('Square', ['M7 7h10v10H7z']);
export const IconPlay = draw('Play', ['M5 3l14 9-14 9V3z']);
export const IconPause = draw('Pause', ['M6 4h4v16H6z', 'M14 4h4v16h-4z']);
export const IconSun = draw('Sun', [
  'M12 17a5 5 0 1 0 0-10 5 5 0 0 0 0 10Z', 'M12 1v2', 'M12 21v2',
  'M4.22 4.22l1.42 1.42', 'M18.36 18.36l1.42 1.42', 'M1 12h2', 'M21 12h2',
  'M4.22 19.78l1.42-1.42', 'M18.36 5.64l1.42-1.42',
]);
export const IconMoon = draw('Moon', ['M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79Z']);
export const IconAuto = draw('Monitor', ['M3 5h18v11H3z', 'M8 20h8', 'M12 16v4']);
export const IconGear = draw('Settings', [
  'M12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6Z',
  'M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09a1.65 1.65 0 0 0-1.08-1.51 1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09a1.65 1.65 0 0 0 1.51-1.08 1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1Z',
]);
export const IconEye = draw('Eye', ['M1 12s4-7 11-7 11 7 11 7-4 7-11 7-11-7-11-7Z', 'M12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6Z']);
export const IconEyeOff = draw('EyeOff', [
  'M17.94 17.94A10.94 10.94 0 0 1 12 19c-7 0-11-7-11-7a18.4 18.4 0 0 1 4.22-5.06',
  'M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 7 11 7a18.5 18.5 0 0 1-2.16 3.19',
  'M14.12 14.12a3 3 0 1 1-4.24-4.24', 'M1 1l22 22',
]);
export const IconUser = draw('User', ['M20 21v-2a4 4 0 0 0-4-4H8a4 4 0 0 0-4 4v2', 'M12 11a4 4 0 1 0 0-8 4 4 0 0 0 0 8Z']);
export const IconUsers = draw('Users', [
  'M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2', 'M9 11a4 4 0 1 0 0-8 4 4 0 0 0 0 8Z',
  'M23 21v-2a4 4 0 0 0-3-3.87', 'M16 3.13a4 4 0 0 1 0 7.75',
]);
export const IconLayers = draw('Layers', ['M12 2 2 7l10 5 10-5-10-5Z', 'M2 17l10 5 10-5', 'M2 12l10 5 10-5']);
export const IconServer = draw('Server', ['M2 3h20v6H2z', 'M2 15h20v6H2z', 'M6 6h.01', 'M6 18h.01']);
export const IconChart = draw('BarChart', ['M3 3v18h18', 'M7 15l4-4 3 3 5-6']);
export const IconPulse = draw('Activity', ['M3 12h4l3 8 4-16 3 8h4']);
export const IconSliders = draw('SlidersHorizontal', [
  'M4 21v-7', 'M4 10V3', 'M12 21v-9', 'M12 8V3', 'M20 21v-5', 'M20 12V3', 'M1 14h6', 'M9 8h6', 'M17 16h6',
]);
export const IconLogout = draw('LogOut', ['M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4', 'M16 17l5-5-5-5', 'M21 12H9']);
export const IconHome = draw('Home', ['M3 10.5 12 3l9 7.5', 'M5 9.5V21h14V9.5']);
export const IconImage = draw('Image', ['M3 5h18v14H3z', 'M3 16l5-5 4 4 3-3 6 6']);
export const IconFile = draw('FileText', [
  'M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8z',
  'M14 3v5h5', 'M9 13h6', 'M9 17h4',
]);
export const IconSpark = draw('Sparkles', [
  'M12 3v4', 'M12 17v4', 'M3 12h4', 'M17 12h4',
  'M5.6 5.6l2.8 2.8', 'M15.6 15.6l2.8 2.8', 'M18.4 5.6l-2.8 2.8', 'M8.4 15.6l-2.8 2.8',
]);
