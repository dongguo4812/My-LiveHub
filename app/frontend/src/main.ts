import '@wailsio/runtime'
import {WriteFrontendLog} from '../bindings/github.com/dongguo4812/My-LiveHub/app/internal/desktop/monitorservice.js'
import {createApp} from 'vue'
import ElementPlus from 'element-plus'
import 'element-plus/dist/index.css'
import 'element-plus/theme-chalk/dark/css-vars.css'
import App from './App.vue'
import router from './router'
import {loadAppearance} from './services/appearance'
import './styles.css'
import { disposeHTMLAudioForShutdown } from './services/htmlAudio'
import { disposeVoiceAudio } from './services/voiceAudio'
import { stopLive2DMicrophone } from './services/live2dMicrophone'

let audioResourcesDisposed = false
function disposeAudioResources(reason: string) {
  if (audioResourcesDisposed) return
  audioResourcesDisposed = true
  console.info('audio lifecycle cleanup started:', reason)
  disposeHTMLAudioForShutdown()
  disposeVoiceAudio()
  stopLive2DMicrophone()
}
window.addEventListener('pagehide', () => disposeAudioResources('pagehide'), { once: true })
window.addEventListener('beforeunload', () => disposeAudioResources('beforeunload'), { once: true })

function formatLogValue(value: unknown): string {
  if (value instanceof Error) return value.stack || value.message
  if (typeof value === 'string') return value
  try {
    const serialized = JSON.stringify(value)
    return serialized === undefined ? String(value) : serialized
  } catch {
    return String(value)
  }
}

function forwardFrontendLog(level: string, values: unknown[]) {
  const message = values.map(formatLogValue).join(' ')
  if (!message) return
  void WriteFrontendLog(level, message).catch(() => {
    // The WebView may already be shutting down; never recurse through console.
  })
}

// Keep the WebView console quiet while retaining application console output in
// the daily application log file.
const mutableConsole = console as unknown as Record<
  'log' | 'info' | 'debug' | 'warn' | 'error' | 'trace',
  (...values: unknown[]) => void
>
for (const [method, level] of [
  ['log', 'INFO'],
  ['info', 'INFO'],
  ['debug', 'DEBUG'],
  ['warn', 'WARN'],
  ['error', 'ERROR'],
  ['trace', 'DEBUG'],
] as const) {
  mutableConsole[method] = (...values: unknown[]) => forwardFrontendLog(level, values)
}
console.assert = ((condition?: boolean, ...values: unknown[]) => {
  if (!condition) forwardFrontendLog('ERROR', ['Assertion failed:', ...values])
}) as typeof console.assert

window.addEventListener('error', event => {
  forwardFrontendLog('ERROR', [
    event.message,
    event.filename ? `(${event.filename}:${event.lineno}:${event.colno})` : '',
    event.error instanceof Error ? event.error.stack || event.error.message : '',
  ])
})
window.addEventListener('unhandledrejection', event => {
  forwardFrontendLog('ERROR', ['Unhandled promise rejection:', event.reason])
})

const app=createApp(App)
app.use(router).use(ElementPlus)
void router.isReady().then(() => {
  if (!router.currentRoute.value.path.startsWith('/scene/')) void loadAppearance()
  app.mount('#app')
})
