import { defineConfig } from 'vite-plus';
import vue from '@vitejs/plugin-vue';
import { cloudflare } from '@cloudflare/vite-plugin';

export default defineConfig({
  // The Worker runtime is not needed for the logic tests, and it clashes with Vitest's server.
  plugins: [vue(), ...(process.env.VITEST ? [] : [cloudflare()])],
  test: {
    include: ['worker/**/*.test.ts'],
  },
  lint: {
    ignorePatterns: ['dist/**', '.wrangler/**'],
  },
  fmt: {
    singleQuote: true,
    printWidth: 120,
  },
});
