import { ref } from 'vue'
import * as CoreService from '../../bindings/github.com/dongguo4812/My-LiveHub/app/internal/desktop/coreservice.js'

// Use generated Wails return types directly so frontend state cannot drift
// from the Go service models.
export type CoreAppSnapshot = Awaited<ReturnType<typeof CoreService.GetStatus>>
export type ModuleSnapshot = Awaited<ReturnType<typeof CoreService.GetModules>>[number]

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
