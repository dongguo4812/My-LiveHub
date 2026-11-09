import { ref } from 'vue'
import * as DanmakuDisplayService from '../../bindings/github.com/dongguo4812/My-LiveHub/app/internal/desktop/danmakudisplayservice.js'

export type DanmakuDisplaySettings = Awaited<ReturnType<typeof DanmakuDisplayService.GetSettings>>
export type DanmakuDisplayEvent = Awaited<ReturnType<typeof DanmakuDisplayService.GetDisplayEvents>>[number]
export const danmakuDisplaySettings = ref<DanmakuDisplaySettings | null>(null)
export const danmakuDisplayError = ref('')
export const danmakuDisplayBusy = ref(false)

export async function loadDanmakuDisplaySettings() {
  try {
    danmakuDisplaySettings.value = await DanmakuDisplayService.GetSettings()
    danmakuDisplayError.value = ''
  } catch (error) {
    danmakuDisplayError.value = error instanceof Error ? error.message : String(error)
  }
}

export async function saveDanmakuDisplaySettings(value: DanmakuDisplaySettings) {
  danmakuDisplayBusy.value = true
  try {
    danmakuDisplaySettings.value = await DanmakuDisplayService.UpdateSettings(value)
    danmakuDisplayError.value = ''
    return true
  } catch (error) {
    danmakuDisplayError.value = error instanceof Error ? error.message : String(error)
    return false
  } finally {
    danmakuDisplayBusy.value = false
  }
}

export async function loadDanmakuDisplayEvents(): Promise<DanmakuDisplayEvent[]> {
  try {
    return await DanmakuDisplayService.GetDisplayEvents()
  } catch (error) {
    danmakuDisplayError.value = error instanceof Error ? error.message : String(error)
    return []
  }
}
