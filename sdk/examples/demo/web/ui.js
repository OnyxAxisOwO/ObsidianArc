// The browser half of the demo plugin: a default export the page calls with
// what it lends a plugin, and gets the plugin's declaration back from.
export default function demo(host) {
  const s = host.strings(
    { handle: 'Handle', handleOptional: 'Handle (optional)', invalid: 'Handle must be 5–15 digits.', required: 'A handle is required.', taken: 'That handle is taken.' },
    { handle: '句柄', handleOptional: '句柄（选填）', invalid: '句柄应为 5–15 位数字。', required: '必须填写句柄。', taken: '该句柄已被占用。' },
  );
  return {
    name: 'demo',
    fields: {
      handle: {
        label: () => s('handle'),
        optionalLabel: () => s('handleOptional'),
        maxLength: 15,
        inputMode: 'numeric',
        pattern: /^[0-9]{5,15}$/,
        invalid: () => s('invalid'),
        required: () => s('required'),
        taken: () => s('taken'),
      },
    },
  };
}
