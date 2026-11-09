import { ref } from 'vue'
import * as PlatformService from '../../bindings/github.com/dongguo4812/My-LiveHub/app/internal/desktop/platformservice.js'

export type PlatformSnapshot = Awaited<ReturnType<typeof PlatformService.GetPlatforms>>[number]

export const platforms = ref<PlatformSnapshot[]>([])
export const platformsError = ref('')
export const platformActionError = ref('')

let refreshing = false

export async function refreshPlatforms() {
  if (refreshing) return
  refreshing = true
  try {
    platforms.value = await PlatformService.GetPlatforms()
    platformsError.value = ''
  } catch (error) {
    platformsError.value = error instanceof Error ? error.message : String(error)
  } finally {
    refreshing = false
  }
}

export async function connectPlatform(platformID: string) {
  platformActionError.value = ''
  try {
    await PlatformService.ConnectPlatform(platformID)
    await refreshPlatforms()
  } catch (error) {
    platformActionError.value = error instanceof Error ? error.message : String(error)
    await refreshPlatforms()
  }
}

export async function disconnectPlatform(platformID: string) {
  platformActionError.value = ''
  try {
    await PlatformService.DisconnectPlatform(platformID)
    await refreshPlatforms()
  } catch (error) {
    platformActionError.value = error instanceof Error ? error.message : String(error)
    await refreshPlatforms()
  }
}
