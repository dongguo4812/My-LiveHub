import { ref } from 'vue'
import * as DanmakuDisplayService from '../../bindings/github.com/dongguo4812/My-LiveHub/app/internal/desktop/danmakudisplayservice.js'

export type DanmakuDisplaySettings = Awaited<ReturnType<typeof DanmakuDisplayService.GetSettings>>
export type DanmakuWindowState = Awaited<ReturnType<typeof DanmakuDisplayService.GetWindowState>>
export type DanmakuDisplayEvent = Awaited<ReturnType<typeof DanmakuDisplayService.GetDisplayEvents>>[number]
export const danmakuDisplaySettings = ref<DanmakuDisplaySettings | null>(null)
export const danmakuDisplayError = ref('')
export const danmakuDisplayBusy = ref(false)
export const danmakuWindowState = ref<DanmakuWindowState>({ is_open: false, is_pinned: false })

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

export async function loadDanmakuWindowState() {
  try {
    danmakuWindowState.value = await DanmakuDisplayService.GetWindowState()
  } catch (error) {
    danmakuDisplayError.value = error instanceof Error ? error.message : String(error)
  }
}

export async function openDanmakuWindow() {
  try {
    await DanmakuDisplayService.OpenWindow()
    await loadDanmakuWindowState()
    danmakuDisplayError.value = ''
    return true
  } catch (error) {
    danmakuDisplayError.value = error instanceof Error ? error.message : String(error)
    return false
  }
}

export async function closeDanmakuWindow() {
  try {
    await DanmakuDisplayService.CloseWindow()
    await loadDanmakuWindowState()
    danmakuDisplayError.value = ''
    return true
  } catch (error) {
    danmakuDisplayError.value = error instanceof Error ? error.message : String(error)
    return false
  }
}

export async function setDanmakuWindowPinned(pinned: boolean) {
  try {
    await DanmakuDisplayService.SetWindowPinned(pinned)
    await loadDanmakuWindowState()
    danmakuDisplayError.value = ''
    return true
  } catch (error) {
    danmakuDisplayError.value = error instanceof Error ? error.message : String(error)
    await loadDanmakuWindowState()
    return false
  }
}
