const sourceNames: Record<string, string> = {
  kw: '酷我音乐', kg: '酷狗音乐', tx: 'QQ 音乐', wy: '网易云音乐', mg: '咪咕音乐',
}

export function musicSourceName(id?: string): string {
  return id ? sourceNames[id] ?? id : '来源未知'
}

export function formatTrackDuration(value?: number): string {
  const seconds = Math.floor(Math.max(0, Number(value ?? 0)) / 1000)
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, '0')}`
}
