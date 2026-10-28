import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// build.target matches the browser support in docs/DESIGN.md.
export default defineConfig({
  plugins: [vue()],
  build: { target: ['chrome124', 'edge124', 'firefox124', 'safari17.4'], sourcemap: false },
  server: { proxy: { '/api': 'http://127.0.0.1:8790' } }
})
