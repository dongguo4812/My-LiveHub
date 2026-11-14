import * as LOLHexService from '../../bindings/github.com/dongguo4812/My-LiveHub/app/internal/desktop/lolhexservice.js'

export interface LOLHexStatus {
  state: 'stopped' | 'starting' | 'running' | 'unavailable'
  running: boolean
  owned: boolean
  external: boolean
  port: number
  base_url: string
  pid?: number
  error?: string
}

export const getLOLHexStatus = () => LOLHexService.GetStatus() as Promise<LOLHexStatus>
export const startLOLHex = () => LOLHexService.Start() as Promise<LOLHexStatus>
export const stopLOLHex = () => LOLHexService.Stop() as Promise<LOLHexStatus>
