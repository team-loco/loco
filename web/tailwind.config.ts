import type { Config } from 'tailwindcss'

export default {
  content: [
    './index.html',
    './src/**/*.{js,ts,jsx,tsx}',
  ],
  theme: {
    extend: {
      colors: {
        main: 'var(--color-main)',
        background: 'var(--color-background)',
        'secondary-background': 'var(--color-secondary-background)',
        foreground: 'var(--color-foreground)',
        'main-foreground': 'var(--color-main-foreground)',
        border: 'var(--color-border)',
        overlay: 'var(--color-overlay)',
        ring: 'var(--color-ring)',
        destructive: 'var(--color-error-text)',
        'error-bg': 'var(--color-error-bg)',
        'error-border': 'var(--color-error-border)',
        'error-text': 'var(--color-error-text)',
        'success-bg': 'var(--color-success-bg)',
        'success-border': 'var(--color-success-border)',
        'success-text': 'var(--color-success-text)',
        'warning-bg': 'var(--color-warning-bg)',
        'warning-border': 'var(--color-warning-border)',
        'warning-text': 'var(--color-warning-text)',
        'chart-1': 'var(--color-chart-1)',
        'chart-2': 'var(--color-chart-2)',
        'chart-3': 'var(--color-chart-3)',
        'chart-4': 'var(--color-chart-4)',
        'chart-5': 'var(--color-chart-5)',
      },
      boxShadow: {
        neo: 'var(--shadow-shadow)',
      },
      borderRadius: {
        neo: 'var(--radius-base)',
      },
      fontWeight: {
        base: 'var(--font-weight-base)',
        heading: 'var(--font-weight-heading)',
      },
    },
  },
  plugins: [],
} satisfies Config
