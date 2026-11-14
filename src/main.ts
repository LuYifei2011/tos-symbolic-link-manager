import { createApp } from 'vue'
import { tos, metadata } from './app.ts'
import App from './App.vue'
import 'tos-app-ui-kit/tokens.css'
import 'tos-app-ui-kit/base.css'
import 'tos-app-ui-kit/style.css'
import './style.css'

tos.start({ title: locale => metadata.name[locale] })
const app = createApp(App).use(tos)
app.mount('#app')
if (import.meta.hot) import.meta.hot.dispose(() => app.unmount())
