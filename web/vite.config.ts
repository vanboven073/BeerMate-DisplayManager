import { defineConfig } from 'vite';
import preact from '@preact/preset-vite';
import { resolve } from 'node:path';

/**
 * Build configuration for the BeerMate Display Manager frontend.
 *
 * Output goes straight into internal/web/dist so `go:embed` picks it up and the
 * production binary stays self-contained. The Jetson never needs Node.js.
 */
export default defineConfig({
  plugins: [preact()],

  // The production display runs Chromium ~97 (January 2022). Anything newer in
  // the output silently breaks the screen with no console anyone will read, so
  // the target is pinned rather than left to browserslist defaults.
  //
  // Notably absent from Chromium 97 and therefore never emitted:
  //   structuredClone (98), :has() (105), container queries (105),
  //   Array.findLast (97 is borderline), CSS nesting (112).
  build: {
    target: ['chrome97', 'es2019'],
    outDir: resolve(__dirname, '../internal/web/dist'),
    emptyOutDir: true,
    assetsDir: 'assets',
    // Inlining below this threshold avoids extra round trips on a device that
    // serves everything from loopback anyway.
    assetsInlineLimit: 2048,
    cssCodeSplit: false,
    sourcemap: false,
    reportCompressedSize: true,
    rollupOptions: {
      output: {
        // Content-hashed names let the server mark /assets immutable.
        entryFileNames: 'assets/[name]-[hash].js',
        chunkFileNames: 'assets/[name]-[hash].js',
        assetFileNames: 'assets/[name]-[hash][extname]',
        manualChunks(id) {
          if (id.includes('node_modules')) return 'vendor';
          return undefined;
        },
      },
    },
    // A signage bundle that grows past this is a regression worth investigating
    // on a 4 GB device.
    chunkSizeWarningLimit: 300,
  },

  server: {
    port: 5173,
    strictPort: true,
    proxy: {
      '/api': { target: 'http://127.0.0.1:8080', changeOrigin: false },
      '/media': { target: 'http://127.0.0.1:8080', changeOrigin: false },
      '/health': { target: 'http://127.0.0.1:8080', changeOrigin: false },
    },
  },

  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: ['./src/test/setup.ts'],
    include: ['src/**/*.test.{ts,tsx}'],
  },
});
