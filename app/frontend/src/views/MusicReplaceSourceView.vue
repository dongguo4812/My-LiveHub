<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { Loading } from '@element-plus/icons-vue'
import { useRoute, useRouter } from 'vue-router'
import * as MusicService from '../../bindings/github.com/dongguo4812/My-LiveHub/app/internal/desktop/musicservice.js'

type Playlist = Awaited<ReturnType<typeof MusicService.GetPlaylist>>
type Track = Playlist['tracks'][number]
const route = useRoute()
const router = useRouter()
const playlist = ref<Playlist | null>(null)
const original = ref<Track | null>(null)
const candidates = ref<Track[]>([])
const selectedCandidate = ref<Track | null>(null)
const errors = ref<Record<string,string>>({})
const searching = ref(false)
const saving = ref(false)
const activeSource = ref('tx')
const error = ref('')
const success = ref('')
const sources = [{id:'tx',name:'QQ 音乐'},{id:'kw',name:'酷我音乐'},{id:'kg',name:'酷狗音乐'},{id:'wy',name:'网易云音乐'},{id:'mg',name:'咪咕音乐'}]
const visibleCandidates = computed(() => candidates.value.filter(item => item.source_id === activeSource.value))
const activeSourceError = computed(() => errors.value[activeSource.value] ?? '')
const selectedSourceName = computed(() => sources.find(item => item.id === activeSource.value)?.name ?? activeSource.value)
function sourceName(id: string) { return sources.find(item => item.id === id)?.name ?? id }
function duration(value?: number) {
  const seconds = Math.floor(Math.max(0, Number(value ?? 0)) / 1000)
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2,'0')}`
}
async function loadOriginal() {
  const playlistID = String(route.query.playlist ?? '')
  const trackID = String(route.query.track ?? '')
  const sourceID = String(route.query.source ?? '')
  if (!playlistID || !trackID) { error.value = '缺少歌单或歌曲标识'; return }
  try {
    playlist.value = await MusicService.GetPlaylist(playlistID)
    original.value = playlist.value.tracks.find(item => item.id === trackID && (!sourceID || item.source_id === sourceID)) ?? null
    if (!original.value) error.value = '原歌单中已找不到这首歌曲，请返回我的列表刷新后重试'
    else activeSource.value = sources.find(item => item.id !== original.value?.source_id)?.id ?? 'tx'
  } catch (reason) { error.value = reason instanceof Error ? reason.message : String(reason) }
}
async function searchAllSources() {
  if (!original.value) return
  searching.value = true
  error.value = ''
  success.value = ''
  selectedCandidate.value = null
  errors.value = {}
  candidates.value = []
  try {
    const keyword = `${original.value.title} ${original.value.artist}`.trim()
    const response = await MusicService.SearchAllSourcesPage(keyword, 1)
    candidates.value = (response.tracks ?? []).filter(item => item.source_id !== original.value?.source_id)
    errors.value = Object.fromEntries(Object.entries(response.errors ?? {}).filter((entry): entry is [string, string] => typeof entry[1] === 'string'))
    const first = sources.find(source => candidates.value.some(item => item.source_id === source.id))
    if (first) activeSource.value = first.id
    if (!candidates.value.length && !Object.keys(errors.value).length) success.value = '其他平台均未找到匹配歌曲'
  } catch (reason) { error.value = reason instanceof Error ? reason.message : String(reason) }
  finally { searching.value = false }
}
function selectCandidate(track: Track | null) { if (track) selectedCandidate.value = track }
function candidateKey(track: Track) { return `${track.source_id}:${track.id}` }
function qualityNames(track: Track) { return (track.qualities ?? []).map(item => item.type).join(' / ') || '未知' }
async function previewCandidate(track: Track) {
  try { await MusicService.PlayTrackNow(track); success.value = `试听中：${track.title} - ${track.artist}` }
  catch (reason) { error.value = reason instanceof Error ? reason.message : String(reason) }
}
async function confirmReplace() {
  if (!playlist.value || !original.value || !selectedCandidate.value) return
  saving.value = true
  error.value = ''
  try {
    await MusicService.ReplacePlaylistTrack(playlist.value.id, original.value.id, selectedCandidate.value)
    success.value = `已将《${original.value.title}》换为 ${sourceName(selectedCandidate.value.source_id)} 音源`
    await router.push({ path:'/music/playlists', query:{ id:playlist.value.id } })
  } catch (reason) { error.value = reason instanceof Error ? reason.message : String(reason) }
  finally { saving.value = false }
}
function backToPlaylist() {
  void router.push({ path:'/music/playlists', query:{ id:playlist.value?.id ?? String(route.query.playlist ?? '') } })
}
onMounted(async () => { await loadOriginal(); if (original.value) await searchAllSources() })
</script>

<template>
  <section class="music-page player-page">
    <header class="page-heading player-heading">
      <div><p class="eyebrow">CHANGE SOURCE</p><h2>歌曲换源</h2><p class="description">根据原歌曲信息搜索其他音乐平台，选择匹配版本后替换歌单中的原曲。</p></div>
      <el-button plain @click="backToPlaylist">返回我的列表</el-button>
    </header>
    <nav class="music-tabs player-tabs" aria-label="音乐页面导航"><button @click="router.push('/music')">正在播放</button><button @click="router.push('/music?tab=search')">歌曲搜索</button><button class="active">歌曲换源</button><button @click="router.push('/music/playlists')">我的列表</button></nav>
    <el-alert v-if="error" :title="error" type="error" :closable="true" show-icon @close="error = ''" />
    <el-alert v-if="success" :title="success" type="success" :closable="true" show-icon @close="success = ''" />
    <el-card v-if="original" class="player-card" shadow="never">
      <template #header><strong>当前歌单歌曲</strong></template>
      <div class="track-compare">
        <div><small>原歌曲</small><strong>{{ original.title }}</strong><span>{{ original.artist || '未知歌手' }} · {{ original.album || '未知专辑' }}</span><el-tag effect="plain">{{ sourceName(original.source_id) }}</el-tag></div>
        <div class="compare-arrow">→</div>
        <div v-if="selectedCandidate"><small>准备替换为</small><strong>{{ selectedCandidate.title }}</strong><span>{{ selectedCandidate.artist || '未知歌手' }} · {{ selectedCandidate.album || '未知专辑' }}</span><el-tag type="success">{{ sourceName(selectedCandidate.source_id) }}</el-tag></div>
        <div v-else class="candidate-placeholder"><small>尚未选择新音源</small><span>从下方平台搜索结果中选择一首歌曲</span></div>
      </div>
      <div class="action-row"><el-button plain :loading="searching" @click="searchAllSources">重新搜索所有平台</el-button><el-button type="primary" :loading="saving" :disabled="!selectedCandidate" @click="confirmReplace">确认换源</el-button></div>
    </el-card>
    <el-card class="player-card" shadow="never">
      <template #header><div class="card-heading"><strong>各平台搜索结果</strong><el-tag effect="plain">{{ candidates.length }} 个候选</el-tag></div></template>
      <div class="source-tabs">
        <button v-for="source in sources" :key="source.id" :class="{active:activeSource === source.id}" @click="activeSource = source.id">
          {{ source.name }} <span>{{ candidates.filter(item => item.source_id === source.id).length }}</span>
        </button>
      </div>
      <el-alert v-if="activeSourceError" :title="`${selectedSourceName}搜索失败：${activeSourceError}`" type="warning" :closable="false" show-icon />
      <el-table v-if="visibleCandidates.length" :data="visibleCandidates"  :row-key="candidateKey" highlight-current-row @current-change="selectCandidate" style="width:100%;margin-top:12px">
        <el-table-column label="选择" width="62"><template #default="scope"><el-button size="small" :type="selectedCandidate?.id === scope.row.id && selectedCandidate?.source_id === scope.row.source_id ? 'primary' : 'default'" @click="selectCandidate(scope.row)">选择</el-button></template></el-table-column>
        <el-table-column prop="title" label="歌曲" min-width="170" /><el-table-column prop="artist" label="歌手" min-width="130" /><el-table-column prop="album" label="专辑" min-width="130" /><el-table-column label="音质" min-width="130"><template #default="scope">{{ qualityNames(scope.row) }}</template></el-table-column><el-table-column label="时长" width="82"><template #default="scope">{{ duration(scope.row.duration_ms) }}</template></el-table-column>
        <el-table-column label="操作" width="90"><template #default="scope"><el-button link type="success" @click="previewCandidate(scope.row)">试听</el-button></template></el-table-column>
      </el-table>
      <el-empty v-else-if="!searching" :description="activeSourceError ? '该平台暂时无法返回结果' : '该平台没有找到候选歌曲'" :image-size="68" />
      <div v-if="searching" class="searching-note"><el-icon class="is-loading"><Loading /></el-icon> 正在搜索各个平台…</div>
    </el-card>
    <p class="hint">换源只替换当前歌单中的这首歌曲，并保留它在歌单中的位置；不会自动更改正在播放的歌曲或观众点播队列。</p>
  </section>
</template>


<style scoped>
.music-page{display:flex;flex-direction:column;gap:18px;min-height:calc(100vh - 146px)}.page-heading{display:flex;align-items:flex-start;justify-content:space-between;gap:20px}.page-heading h2{margin:0;font-size:25px}.eyebrow{margin:0 0 9px;color:var(--app-accent);font-size:11px;letter-spacing:.15em}.description,.hint{color:var(--el-text-color-secondary);font-size:13px}.track-compare{display:grid;grid-template-columns:minmax(0,1fr) 40px minmax(0,1fr);align-items:center;gap:14px}.track-compare>div:not(.compare-arrow){display:flex;flex-direction:column;align-items:flex-start;gap:7px;min-width:0;padding:13px;border:1px solid var(--app-border);border-radius:9px;background:var(--el-fill-color-lighter)}.track-compare small,.track-compare span{font-size:12px;color:var(--el-text-color-secondary)}.track-compare strong{font-size:16px}.compare-arrow{text-align:center;font-size:24px;color:var(--app-accent)}.candidate-placeholder{min-height:105px;justify-content:center}.action-row{display:flex;justify-content:flex-end;gap:8px;margin-top:16px}.source-tabs{display:flex;gap:6px;overflow-x:auto;border-bottom:1px solid var(--app-border)}.source-tabs button{padding:10px 12px;border:0;border-bottom:2px solid transparent;background:transparent;color:var(--el-text-color-secondary);white-space:nowrap;cursor:pointer}.source-tabs button.active{color:var(--app-accent);border-bottom-color:var(--app-accent)}.source-tabs span{margin-left:5px;font-size:11px}.card-heading{display:flex;justify-content:space-between;align-items:center}.searching-note{padding:28px;text-align:center;color:var(--el-text-color-secondary)}.hint{margin:0}@media(max-width:760px){.track-compare{grid-template-columns:1fr}.compare-arrow{transform:rotate(90deg)}.page-heading{align-items:flex-start;flex-direction:column}}
</style>
