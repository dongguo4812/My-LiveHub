export type SyncedLyricLine = { time: number; text: string; translation?: string }

export function parseSyncedLyrics(lrc: string): SyncedLyricLine[] {
  const parsed: Array<{ time: number; text: string }> = []
  for (const line of lrc.split(/\r?\n/)) {
    const match = line.match(/^\[(\d{1,2}):(\d{2})(?:[.:](\d{1,3}))?\](.*)$/)
    if (!match) continue
    const fraction = match[3] ?? '0'
    const milliseconds = Number(match[1]) * 60000 + Number(match[2]) * 1000 + Number(fraction.padEnd(3, '0').slice(0, 3))
    const text = match[4].trim()
    if (text) parsed.push({ time: milliseconds, text })
  }
  parsed.sort((a, b) => a.time - b.time)
  const lines: SyncedLyricLine[] = []
  for (const line of parsed) {
    const previous = lines[lines.length - 1]
    if (previous?.time === line.time) {
      if (line.text !== previous.text && !previous.translation) previous.translation = line.text
      continue
    }
    lines.push({ ...line })
  }
  return lines
}

export function getActiveSyncedLyric(lines: SyncedLyricLine[], positionMS: number): SyncedLyricLine | null {
  if (lines.length === 0) return null
  let active = lines[0]
  for (const line of lines) {
    if (line.time > positionMS) break
    active = line
  }
  return active
}
