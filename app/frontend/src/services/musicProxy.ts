import * as MusicService from '../../bindings/github.com/dongguo4812/My-LiveHub/app/internal/desktop/musicservice.js'

export type LXUserSourceProxy = Awaited<ReturnType<typeof MusicService.GetLXUserSourceProxy>>

export function getLXUserSourceProxy(): Promise<LXUserSourceProxy> {
  return MusicService.GetLXUserSourceProxy()
}

export function updateLXUserSourceProxy(enabled: boolean, host: string, port: string): Promise<LXUserSourceProxy> {
  return MusicService.UpdateLXUserSourceProxy(enabled, host, port)
}
