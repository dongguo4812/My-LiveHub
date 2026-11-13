import * as SceneService from '../../bindings/github.com/dongguo4812/My-LiveHub/app/internal/desktop/sceneservice.js'

export type LayoutScene = Awaited<ReturnType<typeof SceneService.GetScenes>>[number]
export type LayoutLayer = LayoutScene['layers'][number]

export async function getScenes(): Promise<LayoutScene[]> {
  return SceneService.GetScenes()
}
export async function saveScene(scene: LayoutScene): Promise<LayoutScene> {
  return SceneService.SaveScene(scene)
}
export async function deleteScene(id: string): Promise<void> {
  await SceneService.DeleteScene(id)
}
export async function getBrowserSourceURL(id: string): Promise<string> {
  return SceneService.GetBrowserSourceURL(id)
}
