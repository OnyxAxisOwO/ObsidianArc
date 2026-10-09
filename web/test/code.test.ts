import { describe, expect, it } from 'vitest';

import { describeCode } from '../src/chat/code';

// The sniffer is a guess and is allowed to be wrong; what it is not allowed to
// do is contradict a fence that named its own language, or hand back an
// extension that would save a file under a name nothing can open. These are
// the samples it has to get right — one per rule that earns its place.

describe('naming a fenced block', () => {
  it('takes the fence at its word, whatever the source looks like', () => {
    // Go source under a Python fence stays Python: the author said so, and a
    // sniffer that overrules a tag is a sniffer nobody can correct.
    expect(describeCode('python', 'package main\nfunc main() {}').label).toBe('Python');
    expect(describeCode('py', '').extension).toBe('py');
    expect(describeCode('TS', '').label).toBe('TypeScript');
  });

  it('shows a tag it does not know rather than nothing', () => {
    const unknown = describeCode('brainfuck', '+++');
    expect(unknown.label).toBe('BRAINFUCK');
    // Nothing is known about how to save it, so it is saved as text.
    expect(unknown.extension).toBe('txt');
  });

  const samples: ReadonlyArray<readonly [string, string, string]> = [
    ['Go', 'go', 'package main\n\nfunc main() {\n\tprintln("hi")\n}'],
    ['HTML', 'html', '<!doctype html>\n<html lang="en"><body>hi</body></html>'],
    ['JSON', 'json', '{\n  "name": "obsidian",\n  "tags": [1, 2]\n}'],
    ['Python', 'py', 'def add(a, b):\n    return a + b'],
    ['Rust', 'rs', 'fn main() {\n    let mut total = 0;\n}'],
    ['SQL', 'sql', 'select id, title\nfrom conversations\nwhere user_id = ?'],
    ['CSS', 'css', '.oa-panel {\n  border-radius: 18px;\n}'],
    ['YAML', 'yaml', 'name: ci\njobs:\n  test: ok'],
    ['Diff', 'diff', '--- a/main.go\n+++ b/main.go\n@@ -1 +1 @@'],
    ['Bash', 'sh', '#!/usr/bin/env bash\nmake test'],
    ['TypeScript', 'ts', 'interface Model {\n  id: string;\n}'],
    ['JavaScript', 'js', "import { ref } from 'vue';\nconst a = ref(1);"],
    ['Dockerfile', 'dockerfile', 'FROM golang:1.27 AS build\nRUN go build ./...'],
  ];

  for (const [label, extension, source] of samples) {
    it(`reads an untagged block of ${label}`, () => {
      const guessed = describeCode('', source);
      expect(guessed.label).toBe(label);
      expect(guessed.extension).toBe(extension);
    });
  }

  it('says nothing it cannot tell, rather than guessing at prose', () => {
    const prose = describeCode('', 'The quick brown fox jumps over the lazy dog.\n');
    expect(prose.label).toBe('TEXT');
    expect(prose.extension).toBe('txt');
    expect(describeCode('', '').label).toBe('TEXT');
  });
});

// A feedback body is drawn in the administrator's page, so what a reporter
// pastes into a fence is run in front of somebody else. The line-anchored
// rules used to cost the square of the number of blank lines: sixty thousand
// of them froze a tab for most of half a minute.
describe('sniffing a block made of blank lines', () => {
  const shapes: ReadonlyArray<readonly [string, string]> = [
    ['empty lines', '\n'.repeat(100_000) + 'x'],
    ['whitespace-only lines', '  \n'.repeat(50_000) + 'x'],
    ['lines of a tab', '\t\n'.repeat(50_000) + 'x'],
    ['blank lines between words', 'word\n\n'.repeat(30_000)],
    ['blank lines after an opening word', 'select\n' + '\n'.repeat(100_000)],
  ];

  for (const [name, source] of shapes) {
    it(`is quick on ${name}`, () => {
      const started = performance.now();
      describeCode('', source);
      // Orders of magnitude of slack: the old rules took tens of seconds, and
      // these take well under a millisecond.
      expect(performance.now() - started).toBeLessThan(1000);
    });
  }

  it('still steps over blank lines before the code', () => {
    expect(describeCode('', '\n\n\n  package main\n\nfunc main() {}').label).toBe('Go');
    // The SQL rule is not multi-line: it anchors to the start of the text.
    expect(describeCode('', '\n\n  select id\nfrom conversations').label).toBe('SQL');
  });

  it('reads only the start of a long block', () => {
    const buried = 'plain words\n'.repeat(400) + 'package main\n\nfunc main() {}';
    expect(describeCode('', buried).label).toBe('TEXT');
    expect(describeCode('', 'package main\n' + 'plain words\n'.repeat(400)).label).toBe('Go');
  });
});
