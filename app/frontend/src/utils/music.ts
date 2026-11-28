export function formatTrackDuration(value?: number): string {
  const seconds = Math.floor(Math.max(0, Number(value ?? 0)) / 1000)
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, '0')}`
}
