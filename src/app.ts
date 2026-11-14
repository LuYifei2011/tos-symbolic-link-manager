import { createTosApp } from 'tos-app-core'
import { createTosAppVue } from 'tos-app-core/vue'
import manifest from '../app.json'

export const metadata = { id: manifest.id, version: manifest.version, name: manifest.name }
export const core = createTosApp({
  id: metadata.id,
  version: metadata.version,
  // Vite has no TOS preference endpoint; installed builds use the platform readers.
  preferences: import.meta.env.DEV ? { readTheme: async () => null, readLanguage: async () => null } : {},
  api: {
    blockOnAccessFailure: true,
    onAccessFailure: error => { if (error.kind === 'session') location.assign('/tos/') },
    diagnosticPath: path => ['/info', '/echo'].includes(path) ? path : '/:endpoint',
  },
})
export const tos = createTosAppVue(core)
