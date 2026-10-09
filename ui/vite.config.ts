/// <reference types="vitest/config" />
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

// The UI build is standalone: `npm run build` type-checks then bundles the
// React app that axond will serve over TLS on the LAN (AxonWall spec,
// management plane). Vitest runs the unit tests and story smoke tests.
export default defineConfig({
  plugins: [react()],
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/test/setup.ts'],
  },
})
