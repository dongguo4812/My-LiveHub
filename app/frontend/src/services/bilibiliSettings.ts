import { ref } from 'vue'
import * as BilibiliSettingsService from '../../bindings/github.com/dongguo4812/My-LiveHub/app/internal/desktop/bilibilisettingsservice.js'

export type BilibiliSettings = Awaited<ReturnType<typeof BilibiliSettingsService.GetSettings>>
export const bilibiliSettings = ref<BilibiliSettings>({ room_id: 0, video_requests_enabled: true, video_request_prefix: '点播', video_request_cooldown_seconds: 60, video_queue_limit: 30, video_max_duration_seconds: 300, video_require_fan_medal: false, video_fan_medal_level: 1, video_queue_display_limit: 10, video_skip_command_enabled: true, video_skip_command: '下一个', video_window_x: 0, video_window_y: 0, video_window_width: 1280, video_window_height: 800, video_window_bounds_saved: false })
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
