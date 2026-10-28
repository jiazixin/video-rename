// Web standards section 3 confines color literals to the design variables in src/styles/.
export default {
  overrides: [{ files: ['**/*.vue'], customSyntax: 'postcss-html' }],
  rules: {
    'declaration-property-value-disallowed-list': {
      '/^(margin|padding|gap|row-gap|column-gap|border.*radius|font-size|letter-spacing|line-height)/':
        [/(?<![\w-])-?(?:\d+\.?\d*|\.\d+)(?:px|rem|em|%)/],
      '/^(animation|transition)(-|$)/': [/(?<![\w-])(?:\d+\.?\d*|\.\d+)m?s\b/],
      'box-shadow': [/^(?!var\(|none$)/],
      'text-shadow': [/^(?!var\(|none$)/]
    },
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
