import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';

const source = await readFile(new URL('../hooks/register.js', import.meta.url), 'utf8');

// This conservative text guard catches known regressions, not the complete native grammar.
function literalNativeCalls(text) {
  assert.doesNotMatch(text, /\$\s*(?:\?\.|\[)/, 'computed or optional access on $');
  assert.doesNotMatch(text, /\$\s*\.\s*[A-Za-z_$][\w$]*\s*(?:\?\.|\[)/,
    'computed or optional native API access');
  for (const match of text.matchAll(/\$\s*\.\s*[A-Za-z_$][\w$]*\s*\.\s*[A-Za-z_$][\w$]*/g)) {
    assert.ok(text.slice(match.index + match[0].length).trimStart().startsWith('('),
      'native methods must be immediate literal calls');
  }
}

test('the shipped module retains literal native API calls', () => literalNativeCalls(source));

test('the source guard catches the original loader failure and method reflection', () => {
  for (const change of [
    'typeof $?.[group]?.[member]', '$[group][member]()', '$ ["prompt"].submit({})',
    '$.ui?.toast("notice")', '$ . ui ["toast"]("notice")', '$?.prompt.submit({})',
    'typeof $.session.id', 'const submit = $.prompt.submit;', '$.prompt.submit?.({})',
    '$\n.\nprompt\n?.submit({})',
  ]) {
    assert.throws(() => literalNativeCalls(`${source}\n${change}`), assert.AssertionError, change);
  }
});

test('ordinary optional access and literal native calls remain valid', () => {
  literalNativeCalls('runtime?.active; e.origin?.kind; $.session.id(); $ . clock . sleep (1000);');
});
