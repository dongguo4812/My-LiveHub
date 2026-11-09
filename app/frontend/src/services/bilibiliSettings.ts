import { ref } from 'vue'
import * as BilibiliSettingsService from '../../bindings/github.com/dongguo4812/My-LiveHub/app/internal/desktop/bilibilisettingsservice.js'

export type BilibiliSettings = Awaited<ReturnType<typeof BilibiliSettingsService.GetSettings>>
export const bilibiliSettings = ref<BilibiliSettings>({ room_id: 0 })
export const bilibiliSettingsBusy = ref(false)
export const bilibiliSettingsError = ref('')

export async function loadBilibiliSettings() {
  try {
    bilibiliSettings.value = await BilibiliSettingsService.GetSettings()
    bilibiliSettingsError.value = ''
  } catch (error) {
    bilibiliSettingsError.value = error instanceof Error ? error.message : String(error)
  }
}

export async function saveBilibiliRoomID(roomID: number) {
  bilibiliSettingsBusy.value = true
  bilibiliSettingsError.value = ''
  try {
    bilibiliSettings.value = await BilibiliSettingsService.UpdateRoomID(roomID)
    return true
  } catch (error) {
    bilibiliSettingsError.value = error instanceof Error ? error.message : String(error)
    return false
  } finally {
    bilibiliSettingsBusy.value = false
  }
}
