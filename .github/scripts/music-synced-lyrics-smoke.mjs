import assert from 'node:assert/strict'
import { getActiveSyncedLyric, parseSyncedLyrics } from '../app/frontend/src/utils/syncedLyrics.ts'

const lyrics = parseSyncedLyrics([
  '[00:02.00]第二句',
  '[00:01.00]第一句',
  '[00:01.00]First line',
  '[00:01.00]//',
  '[00:03.50]Third line',
].join('\n'))

assert.equal(lyrics.length, 3, 'same-timestamp translation should merge and // placeholders should be ignored')
assert.deepEqual(lyrics[0], { time: 1000, text: '第一句', translation: 'First line' })
assert.equal(lyrics[1].time, 2000, 'lyrics should be sorted by timestamp')
assert.equal(getActiveSyncedLyric(lyrics, 999), null, 'lyrics must not appear before the first timestamp')
assert.deepEqual(getActiveSyncedLyric(lyrics, 1500), lyrics[0])
assert.deepEqual(getActiveSyncedLyric(lyrics, 2500), lyrics[1])

console.log('SYNCED_LYRICS_PARSER_PASS', JSON.stringify(lyrics))
