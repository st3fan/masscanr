import js from '@eslint/js'
import tseslint from 'typescript-eslint'

export default tseslint.config(
  { ignores: ['dist/**', 'src/api/schema.ts', 'src/components/ui/**', 'e2e/**'] },
  js.configs.recommended,
  ...tseslint.configs.recommended,
  {
    files: ['src/**/*.{ts,tsx}', '*.ts'],
    rules: { '@typescript-eslint/no-explicit-any': 'error' },
  },
)
