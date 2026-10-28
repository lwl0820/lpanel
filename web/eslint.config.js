import js from '@eslint/js'
import ts from 'typescript-eslint'
import vue from 'eslint-plugin-vue'
import vueParser from 'vue-eslint-parser'
export default [
  { ignores: ['dist/**', 'node_modules/**'] },
  js.configs.recommended,
  { files: ['src/**/*.{ts,vue}'], languageOptions: { globals: { window: 'readonly', document: 'readonly', setTimeout: 'readonly', clearTimeout: 'readonly', sessionStorage: 'readonly' } } },
  ...ts.configs.recommended,
  ...vue.configs['flat/essential'],
  { files: ['**/*.vue'], languageOptions: { parser: vueParser, parserOptions: { parser: ts.parser, extraFileExtensions: ['.vue'] } } },
]
