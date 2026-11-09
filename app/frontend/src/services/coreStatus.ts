import { ref } from 'vue'
import * as CoreService from '../../bindings/github.com/dongguo4812/My-LiveHub/app/internal/desktop/coreservice.js'

export interface CoreAppSnapshot {
  status: 'created' | 'starting' | 'running' | 'stopping' | 'stopped' | 'failed'
  config_version: number
}

export interface ModuleSnapshot {
  name: string
  status: 'registered' | 'starting' | 'running' | 'stopping' | 'stopped' | 'failed'
  error?: string
}

export const coreStatus = ref<CoreAppSnapshot | null>(null)
export const coreModules = ref<ModuleSnapshot[]>([])
export const coreStatusError = ref('')

let refreshing = false

export async function refreshCoreStatus() {
  if (refreshing) return
  refreshing = true
  try {
    const [status, modules] = await Promise.all([
      CoreService.GetStatus(),
      CoreService.GetModules(),
    ])
    coreStatus.value = status
    coreModules.value = modules
    coreStatusError.value = ''
  } catch (error) {
    coreStatusError.value = error instanceof Error ? error.message : String(error)
  } finally {
    refreshing = false
  }
}
