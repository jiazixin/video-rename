// Web standards section 3 confines color literals to the design variables in src/styles/.
export default {
  overrides: [{ files: ['**/*.vue'], customSyntax: 'postcss-html' }],
  rules: {
    'color-no-hex': true,
    'color-named': 'never',
    'function-disallowed-list': [
      'rgb',
      'rgba',
      'hsl',
      'hsla',
      'hwb',
      'lab',
      'lch',
      'oklab',
      'oklch'
    ]
  },
  ignoreFiles: ['src/styles/**']
}
