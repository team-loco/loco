import { reactRouter } from '@react-router/dev/vite';
import tailwindcss from '@tailwindcss/vite';
import { defineConfig } from 'vite';
import { fumadocsMdx } from 'fumadocs-mdx/vite';

export default defineConfig({
  base: process.env.DOCS_BASE_PATH || '/',
  plugins: [fumadocsMdx(), tailwindcss(), reactRouter()],
  resolve: {
    tsconfigPaths: true,
  },
});
