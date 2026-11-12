import * as Live2DService from '../../bindings/github.com/dongguo4812/My-LiveHub/app/internal/desktop/live2dservice.js'

export interface Live2DConfig {
  enabled: boolean
  runtime_version: 'moc2' | 'moc3'
  default_model_moc2: string
  default_model_moc3: string
  microphone_enabled: boolean
  microphone_device_id: string
  microphone_sensitivity: number
  microphone_smoothing: number
}

export interface Live2DModel {
  id: string
  name: string
  format: 'moc2' | 'moc3'
  directory: string
  entry_file: string
  model_url: string
  runtime_available: boolean
  capability_status: string
}

export const getLive2DSettings = () => Live2DService.GetSettings() as Promise<Live2DConfig>
export const updateLive2DSettings = (value: Live2DConfig) => Live2DService.UpdateSettings(value) as Promise<Live2DConfig>
export const listLive2DModels = () => Live2DService.ListModels() as Promise<Live2DModel[]>
