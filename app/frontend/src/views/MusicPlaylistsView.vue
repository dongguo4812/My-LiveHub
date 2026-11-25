<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import * as MusicService from '../../bindings/github.com/dongguo4812/My-LiveHub/app/internal/desktop/musicservice.js'

type Playlist = Awaited<ReturnType<typeof MusicService.ListPlaylists>>[number]
type PlaylistTrack = Playlist['tracks'][number]
const router = useRouter()
const route = useRoute()
const playlists = ref<Playlist[]>([])
const selectedPlaylistID = ref('')
const selectedPlaylist = computed(() => playlists.value.find(item => item.id === selectedPlaylistID.value) ?? null)
const importing = ref(false)
const error = ref('')
const success = ref('')
const fileInput = ref<HTMLInputElement | null>(null)
const searchQuery = ref('')
const filteredPlaylistTracks = computed(() => {
  const tracks = selectedPlaylist.value?.tracks ?? []
  const terms = searchQuery.value.trim().toLocaleLowerCase().split(/\s+/).filter(Boolean)
  if (!terms.length) return tracks
  return tracks.filter((track: PlaylistTrack) => {
    const searchable = [track.title, track.artist, track.album, track.source_id].filter(Boolean).join(' ').toLocaleLowerCase()
    return terms.every(term => searchable.includes(term))
  })
})
const sourceNames: Record<string,string> = { tx:'QQ 音乐',kw:'酷我音乐',kg:'酷狗音乐',wy:'网易云音乐',mg:'咪咕音乐' }
function sourceName(id: string) { return sourceNames[id] ?? id }
function duration(value?: number) {
  const seconds = Math.floor(Math.max(0, Number(value ?? 0)) / 1000)
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2,'0')}`
}
async function refreshPlaylists(preferredID = selectedPlaylistID.value) {
  try {
    playlists.value = await MusicService.ListPlaylists()
    if (preferredID && playlists.value.some(item => item.id === preferredID)) selectedPlaylistID.value = preferredID
    else selectedPlaylistID.value = playlists.value[0]?.id ?? ''
  } catch (reason) { error.value = reason instanceof Error ? reason.message : String(reason) }
}
async function createPlaylist() {
  const name = window.prompt('请输入新歌单名称')
  if (name == null) return
  error.value = ''
  try {
    const created = await MusicService.CreatePlaylist(name)
    await refreshPlaylists(created.id)
    success.value = `已创建歌单：${created.name}`
  } catch (reason) { error.value = reason instanceof Error ? reason.message : String(reason) }
}
async function renamePlaylist(playlist: Playlist) {
  const name = window.prompt('修改歌单名称', playlist.name)
  if (name == null || name.trim() === playlist.name) return
  error.value = ''
  try {
    await MusicService.RenamePlaylist(playlist.id, name)
    await refreshPlaylists(playlist.id)
    success.value = '歌单名称已更新'
  } catch (reason) { error.value = reason instanceof Error ? reason.message : String(reason) }
}
async function deletePlaylist(playlist: Playlist) {
  if (!window.confirm(`确定删除歌单“${playlist.name}”及其中的 ${playlist.tracks.length} 首歌曲吗？`)) return
  error.value = ''
  try {
    await MusicService.DeletePlaylist(playlist.id)
    await refreshPlaylists('')
    success.value = `已删除歌单：${playlist.name}`
  } catch (reason) { error.value = reason instanceof Error ? reason.message : String(reason) }
}
function chooseImportFile() { fileInput.value?.click() }
async function importLXPlaylist(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  if (!file) return
  importing.value = true
  error.value = ''
  success.value = ''
  try {
    if (file.size > 16 * 1024 * 1024) throw new Error('洛雪歌单文件不能超过 16 MiB')
    const bytes = new Uint8Array(await file.arrayBuffer())
    let binary = ''
    for (let offset = 0; offset < bytes.length; offset += 0x8000) {
      binary += String.fromCharCode(...bytes.subarray(offset, Math.min(offset + 0x8000, bytes.length)))
    }
    const raw = `base64:${btoa(binary)}`
    const imported = await MusicService.ImportLXPlaylists(raw)
    await refreshPlaylists(imported[0]?.id ?? '')
    const totalTracks = imported.reduce((sum, playlist) => sum + playlist.tracks.length, 0)
    success.value = `已导入 ${imported.length} 个洛雪歌单，共 ${totalTracks} 首歌曲`
  } catch (reason) { error.value = reason instanceof Error ? reason.message : String(reason) }
  finally { importing.value = false; input.value = '' }
}
async function playPlaylist(startTrackID = '') {
  if (!selectedPlaylist.value) return
  error.value = ''
  try {
    await MusicService.PlayPlaylist(selectedPlaylist.value.id, startTrackID)
    success.value = startTrackID ? '已从选中歌曲开始播放歌单' : `开始播放歌单：${selectedPlaylist.value.name}`
  } catch (reason) { error.value = reason instanceof Error ? reason.message : String(reason) }
}
async function playTrack(track: Track) {
  await playPlaylist(track.id)
}
async function removeTrack(track: Track) {
  if (!selectedPlaylist.value) return
  error.value = ''
  try {
    await MusicService.RemovePlaylistTrack(selectedPlaylist.value.id, track.id)
    await refreshPlaylists(selectedPlaylist.value.id)
    success.value = `已从歌单移除：${track.title}`
  } catch (reason) { error.value = reason instanceof Error ? reason.message : String(reason) }
}
async function moveTrack(track: Track, direction: -1 | 1) {
  if (!selectedPlaylist.value) return
  const index = selectedPlaylist.value.tracks.findIndex(item => item.id === track.id)
  const target = index + direction
  if (index < 0 || target < 0 || target >= selectedPlaylist.value.tracks.length) return
  error.value = ''
  try {
    await MusicService.MovePlaylistTrack(selectedPlaylist.value.id, track.id, target)
    await refreshPlaylists(selectedPlaylist.value.id)
  } catch (reason) { error.value = reason instanceof Error ? reason.message : String(reason) }
}
function openReplace(track: Track) {
  if (!selectedPlaylist.value) return
  void router.push({ path:'/music/replace', query:{ playlist:selectedPlaylist.value.id, track:track.id, source:track.source_id } })
}
function goMusicTab(tab: string) { void router.push({ path:'/music', query:{ tab } }) }
onMounted(() => { void refreshPlaylists(String(route.query.id ?? '')) })
</script>

<template>
  <section class="music-page player-page">
    <header class="page-heading player-heading">
      <div><p class="eyebrow">MUSIC LIBRARY</p><h2>我的列表</h2><p class="description">保存个人歌单、导入洛雪歌单，并随时为歌曲更换可用音源。</p></div>
      <div class="header-actions"><el-button type="primary" @click="createPlaylist">新建歌单</el-button><el-button plain :loading="importing" @click="chooseImportFile">导入洛雪歌单</el-button><input ref="fileInput" class="hidden-file-input" type="file" accept=".lxmc,.json,application/json" @change="importLXPlaylist" /></div>
    </header>
    <nav class="music-tabs player-tabs" aria-label="音乐页面导航">
      <button @click="goMusicTab('now-playing')">正在播放</button><button @click="goMusicTab('search')">歌曲搜索</button><button @click="goMusicTab('visitor-requests')">观众点播</button><button class="active" aria-current="page">我的列表</button><button @click="goMusicTab('settings')">音乐设置</button><button @click="router.push('/music/sources')">JS 音源脚本管理</button>
    </nav>
    <el-alert v-if="error" :title="error" type="error" :closable="true" show-icon @close="error = ''" />
    <el-alert v-if="success" :title="success" type="success" :closable="true" show-icon @close="success = ''" />
    <div class="library-layout">
      <el-card class="player-card playlist-sidebar" shadow="never">
        <template #header><div class="card-heading"><strong>歌单</strong><el-tag effect="plain">{{ playlists.length }}</el-tag></div></template>
        <button v-for="playlist in playlists" :key="playlist.id" class="playlist-entry" :class="{active:selectedPlaylistID === playlist.id}" @click="selectedPlaylistID = playlist.id">
          <span class="playlist-icon">♫</span><span class="playlist-entry-copy"><strong>{{ playlist.name }}</strong><small>{{ playlist.tracks.length }} 首歌曲</small></span>
        </button>
        <el-empty v-if="!playlists.length" description="还没有歌单" :image-size="68" />
      </el-card>
      <div class="library-main">
        <el-card v-if="selectedPlaylist" class="player-card" shadow="never">
          <template #header><div class="playlist-heading"><div><strong>{{ selectedPlaylist.name }}</strong><p>{{ selectedPlaylist.tracks.length }} 首歌曲 · 最近更新 {{ new Date(selectedPlaylist.updated_at).toLocaleString() }}</p></div><div class="header-actions"><el-button size="small" type="success" :disabled="!selectedPlaylist.tracks.length" @click="playPlaylist()">播放全部</el-button><el-button size="small" plain @click="renamePlaylist(selectedPlaylist)">重命名</el-button><el-button size="small" type="danger" plain @click="deletePlaylist(selectedPlaylist)">删除歌单</el-button></div></div></template>
          <div class="playlist-search">
            <el-input
              v-model="searchQuery"
              clearable
              placeholder="搜索当前歌单中的歌曲名、歌手或专辑"
              aria-label="搜索当前歌单中的歌曲"
            />
            <span class="search-summary">显示 {{ filteredPlaylistTracks.length }} / {{ selectedPlaylist.tracks.length }} 首歌曲</span>
          </div>
          <el-divider />
          <el-table :data="filteredPlaylistTracks" row-key="id" style="width:100%">
            <el-table-column type="index" label="#" width="54" />
            <el-table-column label="歌曲" min-width="190"><template #default="scope"><div class="song-cell"><strong>{{ scope.row.title }}</strong><small>{{ scope.row.artist || '未知歌手' }}<span v-if="scope.row.album"> · {{ scope.row.album }}</span></small></div></template></el-table-column>
            <el-table-column label="音源" width="112"><template #default="scope">{{ sourceName(scope.row.source_id) }}</template></el-table-column>
            <el-table-column label="时长" width="82"><template #default="scope">{{ duration(scope.row.duration_ms) }}</template></el-table-column>
            <el-table-column label="操作" min-width="230" fixed="right"><template #default="scope"><div class="row-actions"><el-button link type="success" @click="playTrack(scope.row)">播放</el-button><el-button link type="primary" @click="openReplace(scope.row)">换源</el-button><el-button link @click="moveTrack(scope.row,-1)">上移</el-button><el-button link @click="moveTrack(scope.row,1)">下移</el-button><el-button link type="danger" @click="removeTrack(scope.row)">删除</el-button></div></template></el-table-column>
          </el-table>
          <el-empty v-if="!selectedPlaylist.tracks.length" description="歌单为空" :image-size="68" />
          <el-empty v-else-if="!filteredPlaylistTracks.length" description="当前歌单中没有匹配的歌曲" :image-size="68" />
        </el-card>
        <el-card v-else class="player-card" shadow="never"><el-empty description="请选择歌单，或先新建/导入一个歌单" /></el-card>
      </div>
    </div>
  </section>
</template>

<style scoped>
.music-page{display:flex;flex-direction:column;gap:20px;min-height:calc(100vh - 146px)}.page-heading{display:flex;align-items:flex-start;justify-content:space-between;gap:20px}.page-heading h2{margin:0;font-size:25px}.eyebrow{margin:0 0 9px;color:var(--app-accent);font-size:11px;letter-spacing:.15em}.description,.section-description{color:var(--el-text-color-secondary);font-size:13px}.header-actions{display:flex;gap:8px;align-items:center;flex-wrap:wrap}.hidden-file-input{display:none}.library-layout{display:grid;grid-template-columns:minmax(210px,260px) minmax(0,1fr);gap:16px;align-items:start}.playlist-sidebar{position:sticky;top:12px}.playlist-entry{display:flex;align-items:center;gap:10px;width:100%;padding:11px;border:1px solid transparent;border-radius:8px;background:transparent;color:var(--el-text-color-primary);text-align:left;cursor:pointer}.playlist-entry:hover,.playlist-entry.active{border-color:var(--app-accent);background:var(--app-bg-control)}.playlist-icon{font-size:22px;color:var(--app-accent)}.playlist-entry-copy,.song-cell{display:flex;flex-direction:column;gap:4px;min-width:0}.playlist-entry-copy small,.song-cell small,.playlist-heading p{color:var(--el-text-color-secondary);font-size:12px}.playlist-entry-copy strong,.song-cell strong{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.library-main{display:flex;flex-direction:column;gap:16px;min-width:0}.playlist-heading{display:flex;justify-content:space-between;align-items:center;gap:14px}.playlist-heading p{margin:5px 0 0}.row-actions{display:flex;align-items:center;gap:3px;flex-wrap:wrap}.search-row{display:flex;gap:10px}.playlist-search{display:flex;flex-direction:column;gap:10px;margin-bottom:8px}.playlist-search-heading{display:flex;flex-direction:column;gap:4px}.playlist-search-heading span,.search-summary{color:var(--el-text-color-secondary);font-size:12px}.search-pagination{display:flex;justify-content:center;margin-top:12px}.music-tabs{display:flex;align-items:center;gap:6px;overflow-x:auto;padding:0 2px 8px;border-bottom:1px solid var(--app-border)}.music-tabs button{white-space:nowrap;border:0;border-bottom:2px solid transparent;background:transparent;color:var(--el-text-color-secondary);padding:11px 14px;margin-bottom:-1px;font:inherit;font-size:14px;cursor:pointer}.music-tabs button.active{color:var(--app-accent);border-bottom-color:var(--app-accent);font-weight:600}.card-heading{display:flex;align-items:center;justify-content:space-between}.section-description{margin:0 0 14px}@media(max-width:900px){.library-layout{grid-template-columns:1fr}.playlist-sidebar{position:static}.page-heading,.playlist-heading{align-items:flex-start;flex-direction:column}.search-row{flex-direction:column}}
</style>
