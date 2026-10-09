// What a fenced block is written in, and what a file of it would be called.
//
// A model very often opens a fence without tagging it — ``` on its own — and
// the block then has no name to show and no extension to be saved under. The
// tag is used whenever there is one; the sniffing below only ever runs on the
// blocks that came without.
//
// It is a guess, and it is allowed to be wrong: nothing downstream depends on
// it being right. A mislabelled block still reads, still copies, and still
// downloads — under the wrong extension, which is a rename. So the rules stay
// few and cheap rather than growing into a language detector, which is a
// dependency this project is not adding.

export interface CodeLanguage {
  /** The fence's tag, or what was guessed from the source; '' when neither. */
  id: string;
  /** The name on the block's header. */
  label: string;
  /** What a downloaded file is named with, without the dot. */
  extension: string;
}

/**
 * The languages worth naming: what each is called, what it is saved as, and
 * the other spellings a fence uses for it.
 */
const KNOWN: Record<string, { label: string; extension: string; aliases?: readonly string[] }> = {
  bash: { label: 'Bash', extension: 'sh', aliases: ['sh', 'shell', 'zsh', 'console', 'terminal'] },
  c: { label: 'C', extension: 'c', aliases: ['h'] },
  cpp: { label: 'C++', extension: 'cpp', aliases: ['c++', 'cc', 'hpp', 'cxx'] },
  csharp: { label: 'C#', extension: 'cs', aliases: ['cs', 'c#'] },
  css: { label: 'CSS', extension: 'css' },
  diff: { label: 'Diff', extension: 'diff', aliases: ['patch'] },
  dockerfile: { label: 'Dockerfile', extension: 'dockerfile', aliases: ['docker'] },
  go: { label: 'Go', extension: 'go', aliases: ['golang'] },
  html: { label: 'HTML', extension: 'html', aliases: ['xhtml', 'vue', 'svelte'] },
  ini: { label: 'INI', extension: 'ini', aliases: ['toml', 'conf'] },
  java: { label: 'Java', extension: 'java' },
  javascript: { label: 'JavaScript', extension: 'js', aliases: ['js', 'mjs', 'cjs', 'jsx'] },
  json: { label: 'JSON', extension: 'json', aliases: ['jsonl', 'json5'] },
  kotlin: { label: 'Kotlin', extension: 'kt', aliases: ['kt'] },
  latex: { label: 'LaTeX', extension: 'tex', aliases: ['tex'] },
  lua: { label: 'Lua', extension: 'lua' },
  markdown: { label: 'Markdown', extension: 'md', aliases: ['md'] },
  php: { label: 'PHP', extension: 'php' },
  powershell: { label: 'PowerShell', extension: 'ps1', aliases: ['ps1', 'pwsh'] },
  python: { label: 'Python', extension: 'py', aliases: ['py', 'python3'] },
  ruby: { label: 'Ruby', extension: 'rb', aliases: ['rb'] },
  rust: { label: 'Rust', extension: 'rs', aliases: ['rs'] },
  scss: { label: 'Sass', extension: 'scss', aliases: ['sass'] },
  sql: { label: 'SQL', extension: 'sql' },
  swift: { label: 'Swift', extension: 'swift' },
  typescript: { label: 'TypeScript', extension: 'ts', aliases: ['ts', 'tsx', 'mts'] },
  xml: { label: 'XML', extension: 'xml', aliases: ['svg', 'plist'] },
  yaml: { label: 'YAML', extension: 'yaml', aliases: ['yml'] },
};

/** Every spelling that resolves to an entry above, including its own name. */
const BY_NAME = new Map<string, string>();
for (const [id, entry] of Object.entries(KNOWN)) {
  BY_NAME.set(id, id);
  for (const alias of entry.aliases ?? []) BY_NAME.set(alias, id);
}

/**
 * One rule per language: the first pattern that matches names the block.
 *
 * Ordered by how much a match is worth rather than by how common the language
 * is — `package main` can only be Go, while `import` is four languages at
 * once, so the specific ones come first and the ones that lean on punctuation
 * come last.
 */
const RULES: ReadonlyArray<readonly [string, RegExp]> = [
  ['diff', /^(diff --git |@@ -\d|[+-]{3} [ab/])/m],
  ['html', /^[ \t]*<(!doctype html|html|head|body|div|span|p|section|template|script|style)\b/i],
  ['go', /^[ \t]*package\s+\w+\s*$|^[ \t]*func\s+(\w+\s*)?\(|:=/m],
  ['rust', /^[ \t]*(fn\s+\w+|impl\s+\w+|use\s+\w+::)|let\s+mut\s/m],
  ['python', /^[ \t]*(def|class)\s+\w+.*:\s*$|^[ \t]*(from\s+[\w.]+\s+)?import\s+\w+\s*$/m],
  ['sql', /^[ \t]*(select\s+[\s\S]+\bfrom\b|insert\s+into|create\s+table|update\s+\w+\s+set)/i],
  // Case-sensitive on purpose: `FROM` is the Dockerfile convention, and the
  // insensitive spelling of this rule read a SQL query's `from` as a build
  // stage.
  ['dockerfile', /^FROM\s+\S+(\s+AS\s+\S+)?\s*$/m],
  ['php', /<\?php\b/],
  ['ruby', /^[ \t]*(require\s+'|def\s+\w+[\s\S]*?\bend\b|puts\s+)/m],
  ['java', /^[ \t]*(public|private)\s+(static\s+)?(final\s+)?(class|void|int|String)\b/m],
  ['csharp', /^[ \t]*using\s+System\b|\bnamespace\s+\w+/m],
  ['swift', /^[ \t]*(import\s+(Foundation|SwiftUI|UIKit)|func\s+\w+\([^)]*\)\s*->)/m],
  ['kotlin', /^[ \t]*fun\s+\w+\s*\(|\bval\s+\w+\s*(:|=)/m],
  ['powershell', /^[ \t]*(\$\w+\s*=|Get-|Set-|New-|Write-Host)\w*/m],
  ['bash', /^[ \t]*(#!.*\b(ba)?sh\b|\$ |sudo |apt |npm |npx |yarn |git |cd |echo |curl |docker |make )/m],
  ['typescript', /^[ \t]*(interface\s+\w+|type\s+\w+\s*=|enum\s+\w+)\s*[{=]|:\s*(string|number|boolean)\b/m],
  ['javascript', /^[ \t]*(import\s+[\s\S]*?from\s+['"]|export\s+(default|const|function)|const\s+\w+\s*=|function\s+\w+\s*\()/m],
  ['scss', /^[ \t]*[$@][\w-]+\s*[:(]|&:[\w-]+\s*\{/m],
  ['css', /^[ \t]*[.#]?[\w-]+[^{}]*\{[^{}]*[\w-]+\s*:[^{};]+;/m],
  ['yaml', /^[\w-]+:\s*($|[^:\s].*$)/m],
  ['markdown', /^(#{1,6}\s+\S|[-*]\s+\S[\s\S]*^[-*]\s+\S)/m],
];

/**
 * Whether a run of text parses as JSON.
 *
 * Sniffed by parsing rather than by pattern, because the punctuation that
 * opens a JSON document opens a JavaScript object literal just as well, and
 * the parser is the only thing that can tell them apart.
 */
function isJSON(text: string): boolean {
  const trimmed = text.trim();
  if (!/^[[{]/.test(trimmed) || trimmed.length > 200_000) return false;
  try {
    JSON.parse(trimmed);
    return true;
  } catch {
    return false;
  }
}

/**
 * How much of a block the rules read. A language shows itself in its first
 * lines, and the rules are line-anchored scans whose cost grows with the text
 * — on a block that is mostly blank lines it grew with the square of it. A
 * fence of sixty thousand of them froze a tab for most of half a minute, and
 * feedback bodies are drawn in the administrator's own page, so what a reporter
 * pastes is run in front of somebody else.
 */
const SNIFF_CHARS = 2000;

function guess(text: string): string {
  if (!text.trim()) return '';
  if (isJSON(text)) return 'json';
  // Leading blank lines are dropped first, since the rules that are not
  // multi-line anchor to the start of the text and used to step over them.
  const sample = text.trimStart().slice(0, SNIFF_CHARS);
  for (const [id, pattern] of RULES) {
    if (pattern.test(sample)) return id;
  }
  return '';
}

/** What to call a block, from its fence tag if it has one and its source if not. */
export function describeCode(hint: string, text: string): CodeLanguage {
  const tag = hint.trim().toLowerCase();
  const id = BY_NAME.get(tag) ?? (tag ? '' : BY_NAME.get(guess(text)) ?? '');
  const entry = id ? KNOWN[id] : undefined;
  if (!entry) {
    // A tag this table does not know is still the author's own word for the
    // block, and showing it is better than showing nothing — it just cannot
    // say what the file should be called, so it is saved as plain text.
    return { id: tag, label: tag ? tag.toUpperCase() : 'TEXT', extension: 'txt' };
  }
  return { id, label: entry.label, extension: entry.extension };
}
