import { readFileSync } from 'node:fs';
import { defineConfig } from 'vite';

// React, React DOM and Scheduler ship under the same MIT license.
const license = readFileSync(new URL('./node_modules/react/LICENSE', import.meta.url), 'utf8');

export default defineConfig({
  base: './',
  esbuild: { legalComments: 'eof' },
  build: {
    outDir: '../internal/console/static',
    emptyOutDir: true,
    modulePreload: false,
    rollupOptions: {
      output: {
        banner: '/*! React, React DOM and Scheduler\n' + license + '\n*/',
        entryFileNames: 'console.js',
        assetFileNames: 'console.[ext]'
      }
    }
  }
});
