export type SyncedLyricLine = { time: number; text: string; translation?: string }

// 洛雪的双语歌词可能用 "//" 表示该时间点没有翻译。
// 这类占位符不能进入歌词列表，否则会被误显示为第二列歌词。
function isLyricPlaceholder(text: string): boolean {
  const normalized = text.trim().replace(/[\s\u200b\ufeff]/g, '')
  return normalized === '' || /^\/{2,}$/.test(normalized)
}

export function parseSyncedLyrics(lrc: string): SyncedLyricLine[] {
  const parsed: Array<{ time: number; text: string }> = []
  for (const line of lrc.split(/\r?\n/)) {
    const match = line.match(/^((?:\[\d{1,2}:\d{2}(?:[.:]\d{1,3})?\])+)(.*)$/)
    if (!match) continue
    const text = match[2].trim()
    if (isLyricPlaceholder(text)) continue
    const timestamps = match[1].matchAll(/\[(\d{1,2}):(\d{2})(?:[.:](\d{1,3}))?\]/g)
    for (const timestamp of timestamps) {
      const fraction = timestamp[3] ?? '0'
      const milliseconds = Number(timestamp[1]) * 60000 + Number(timestamp[2]) * 1000 + Number(fraction.padEnd(3, '0').slice(0, 3))
      parsed.push({ time: milliseconds, text })
    }
  }
  parsed.sort((a, b) => a.time - b.time)
  const lines: SyncedLyricLine[] = []
  for (const line of parsed) {
    const previous = lines[lines.length - 1]
    if (previous?.time === line.time) {
      if (line.text !== previous.text && !previous.translation && !isLyricPlaceholder(line.text)) {
        previous.translation = line.text
      }
      continue
    }
    lines.push({ ...line })
  }
  return lines
}

export function getActiveSyncedLyric(lines: SyncedLyricLine[], positionMS: number): SyncedLyricLine | null {
  if (lines.length === 0) return null
  if (positionMS < lines[0].time) return null
  let active = lines[0]
  for (const line of lines) {
    if (line.time > positionMS) break
    active = line
  }
  return active
}
