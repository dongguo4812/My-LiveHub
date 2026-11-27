<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import * as MusicService from '../../bindings/github.com/dongguo4812/My-LiveHub/app/internal/desktop/musicservice.js'
const router = useRouter()
type LXScriptSummary = Awaited<ReturnType<typeof MusicService.ListLXScripts>>[number]
const lxScripts = ref<LXScriptSummary[]>([])
const lxScriptName = ref('')
const lxScriptSource = ref('')
const lxScriptFileInput = ref<HTMLInputElement | null>(null)
const lxScriptImportMode = ref<'online' | 'local'>('online')
const lxScriptURL = ref('')
const lxScriptBusy = ref(false)
const lxScriptFeedback = ref('')
const testingLXScriptID = ref('')
const error = ref('')
const success = ref('')
async function refreshLXScripts() {
  try { lxScripts.value = await MusicService.ListLXScripts() }
  catch (reason) { error.value = reason instanceof Error ? reason.message : String(reason) }
}
function chooseLXScriptFile() { lxScriptFileInput.value?.click() }
function lxActionNames(actions: string[] = []) {
  const names: Record<string, string> = { search: '搜索', detail: '详情', lyric: '歌词', pic: '封面' }
  return actions.map(action => names[action] ?? action).join('、') || '未识别到社区扩展能力'
}
async function importLXScriptFromURL() {
  const rawURL = lxScriptURL.value.trim()
  if (!rawURL) { error.value = '请输入在线 JS 音源脚本 URL'; return }
  let parsedURL: URL
  try { parsedURL = new URL(rawURL) } catch { error.value = '在线脚本 URL 格式无效'; return }
  if (!['http:', 'https:'].includes(parsedURL.protocol) || parsedURL.username || parsedURL.password) {
    error.value = '在线脚本只允许使用不包含账号密码的 HTTP(S) URL'
    return
  }
  lxScriptBusy.value = true
  error.value = ''
  success.value = ''
  const controller = new AbortController()
  const timeout = window.setTimeout(() => controller.abort(), 15000)
  try {
    const response = await fetch(parsedURL.href, { method: 'GET', credentials: 'omit', cache: 'no-store', redirect: 'error', referrerPolicy: 'no-referrer', signal: controller.signal })
    if (!response.ok) throw new Error(`在线脚本下载失败：HTTP ${response.status}`)
    const declaredLength = Number(response.headers.get('content-length') ?? 0)
    if (declaredLength > 2 * 1024 * 1024) throw new Error('在线脚本超过 2 MiB，已拒绝导入')
    const source = await response.text()
    if (!source.trim()) throw new Error('在线脚本内容为空')
    if (new Blob([source]).size > 2 * 1024 * 1024) throw new Error('在线脚本超过 2 MiB，已拒绝导入')
    lxScriptSource.value = source
    if (!lxScriptName.value.trim()) {
      const filename = decodeURIComponent(parsedURL.pathname.split('/').filter(Boolean).pop() ?? '')
      lxScriptName.value = filename.toLowerCase().endsWith('.js') ? filename : (filename || parsedURL.hostname)
    }
    success.value = '在线脚本已获取，请核对脚本名称和源码后导入'
  } catch (reason) {
    error.value = reason instanceof Error
      ? (reason.name === 'AbortError' ? '在线脚本下载超时（15 秒）' : reason.message.includes('Failed to fetch') ? '在线脚本获取失败：目标站点可能不允许跨域读取（CORS），请改用本地导入' : reason.message)
      : String(reason)
  } finally { window.clearTimeout(timeout); lxScriptBusy.value = false }
}
async function readLXScriptFile(event: Event) {
  const file = (event.target as HTMLInputElement).files?.[0]
  if (!file) return
  try {
    lxScriptSource.value = await file.text()
    if (!lxScriptName.value.trim()) lxScriptName.value = file.name
    success.value = `已读取脚本文件：${file.name}`
  } catch (reason) { error.value = reason instanceof Error ? reason.message : String(reason) }
  finally { ;(event.target as HTMLInputElement).value = '' }
}
async function importLXScript() {
  if (lxScriptBusy.value) return
  if (!lxScriptName.value.trim() || !lxScriptSource.value.trim()) {
    const message = '请填写脚本名称并选择或粘贴 JavaScript 源码'
    error.value = message
    lxScriptFeedback.value = `导入未开始：${message}`
    return
  }
  lxScriptBusy.value = true
  error.value = ''
  success.value = ''
  lxScriptFeedback.value = '正在调用桌面端 QuickJS 导入接口，请稍候……'
  try {
    if (typeof MusicService.ImportLXScript !== 'function') throw new Error('当前程序未生成 ImportLXScript 前端绑定，请重新生成 Wails bindings 并重新构建')
    const added = await MusicService.ImportLXScript(lxScriptName.value.trim(), lxScriptSource.value)
    await refreshLXScripts()
    if (!lxScripts.value.some(script => script.id === added.id)) throw new Error('后端返回导入成功，但刷新脚本列表后未找到该脚本，请检查本地脚本存储状态')
    lxScriptName.value = ''
    lxScriptSource.value = ''
    success.value = `已导入并启用音源脚本：${added.name}；社区扩展 action：${lxActionNames(added.extension_actions ?? [])}；洛雪原生 action：${(added.native_actions ?? []).join('、') || '未声明'}；支持音源：${(added.supported_sources ?? []).join('、')}`
    lxScriptFeedback.value = success.value
  } catch (reason) {
    const message = reason instanceof Error ? reason.message : String(reason)
    error.value = message
    lxScriptFeedback.value = `导入失败：${message}`
  } finally { lxScriptBusy.value = false }
}
async function toggleLXScript(script: LXScriptSummary, enabled: boolean) {
  lxScriptBusy.value = true
  error.value = ''
  try { await MusicService.SetLXScriptEnabled(script.id, enabled); await refreshLXScripts() }
  catch (reason) { error.value = reason instanceof Error ? reason.message : String(reason) }
  finally { lxScriptBusy.value = false }
}
async function testLXScript(script: LXScriptSummary) {
  testingLXScriptID.value = script.id
  lxScriptBusy.value = true
  error.value = ''
  try {
    await MusicService.TestLXScript(script.id)
    await refreshLXScripts()
    const latest = lxScripts.value.find(item => item.id === script.id)?.last_test
    const passed = latest?.platforms?.filter(item => item.status === 'passed').length ?? 0
    const total = latest?.platforms?.length ?? 0
    success.value = latest?.message || `音源测试完成：${script.name}，${passed}/${total} 个平台通过`
  } catch (reason) { error.value = reason instanceof Error ? reason.message : String(reason) }
  finally { testingLXScriptID.value = ''; lxScriptBusy.value = false }
}
async function deleteLXScript(script: LXScriptSummary) {
  if (!window.confirm(`确定删除音源脚本“${script.name}”吗？此操作不可撤销。`)) return
  lxScriptBusy.value = true
  error.value = ''
  try { await MusicService.DeleteLXScript(script.id); await refreshLXScripts(); success.value = `已删除音源脚本：${script.name}` }
  catch (reason) { error.value = reason instanceof Error ? reason.message : String(reason) }
  finally { lxScriptBusy.value = false }
}
function goMusicTab(tab: string) { void router.push({ path: '/music', query: { tab } }) }
onMounted(() => { void refreshLXScripts() })
</script>
<template>
  <section class="music-page player-page">
    <header class="page-heading player-heading"><div><p class="eyebrow">MUSIC PLAYER</p><h2>JS 音源脚本管理</h2><p class="description">独立管理洛雪 JavaScript 音源脚本的导入、启停、检测和删除。</p></div><el-tag effect="plain">{{ lxScripts.length }} 个脚本</el-tag></header>
    <nav class="music-tabs player-tabs" aria-label="音乐页面导航">
      <button @click="goMusicTab('now-playing')">正在播放</button><button @click="goMusicTab('search')">歌曲搜索</button><button @click="goMusicTab('visitor-requests')">观众点播</button><button @click="router.push('/music/playlists')">我的列表</button><button @click="goMusicTab('settings')">音乐设置</button><button class="active" aria-current="page">JS 音源脚本管理</button>
    </nav>
    <el-alert v-if="error" :title="error" type="error" :closable="true" show-icon @close="error = ''" />
    <el-alert v-if="success" :title="success" type="success" :closable="true" show-icon @close="success = ''" />
    <el-card class="player-card player-settings-card" shadow="never">
      <template #header><div class="card-heading"><strong>导入 JS 音源脚本</strong><el-tag effect="plain">{{ lxScripts.length }} 个脚本</el-tag></div></template>
      <p class="section-description">脚本源码保存在本机 config/local，重启后自动恢复；脚本执行受 QuickJS 宿主能力和网络安全策略限制。</p>
      <div class="setting-list">
        <div class="setting-row"><div><strong>脚本名称</strong><p>用于脚本列表显示，可填写来源或版本说明。</p></div><el-input v-model="lxScriptName" placeholder="例如：我的音乐音源" style="max-width:360px" /></div>
        <div class="setting-row lx-import-mode-row"><div><strong>JS 文件导入方式</strong><p>在线导入通过 URL 获取脚本；本地导入从电脑选择 .js 文件。</p></div><el-radio-group v-model="lxScriptImportMode" size="default"><el-radio-button label="online">在线导入</el-radio-button><el-radio-button label="local">本地导入</el-radio-button></el-radio-group></div>
        <div v-if="lxScriptImportMode === 'online'" class="setting-row"><div><strong>在线 JS 地址</strong><p>输入可公开访问的 HTTP(S) 脚本地址，获取后可在下方检查源码。</p></div><div class="lx-import-row"><el-input v-model="lxScriptURL" placeholder="https://example.com/music-source.js" clearable /><el-button type="primary" plain :loading="lxScriptBusy" @click="importLXScriptFromURL">获取脚本</el-button></div></div>
        <div v-else class="setting-row"><div><strong>本地 JS 文件</strong><p>从电脑选择一个 .js 音源脚本文件。</p></div><div class="lx-import-row"><input ref="lxScriptFileInput" class="hidden-file-input" type="file" accept=".js,text/javascript,application/javascript" @change="readLXScriptFile" /><el-button plain @click="chooseLXScriptFile">选择本地文件</el-button></div></div>
        <p class="section-description">导入时自动执行脚本初始化，并根据洛雪 inited.sources.actions 识别搜索、详情、歌词和封面能力；musicUrl 不作为社区扩展能力，仍按洛雪原生 actions 声明处理，无需手动选择。</p>
        <el-input v-model="lxScriptSource" type="textarea" :rows="8" resize="vertical" placeholder="粘贴完整的洛雪 JS 音源脚本源码……" />
        <div class="card-actions"><el-button type="primary" :loading="lxScriptBusy" :disabled="!lxScriptName.trim() || !lxScriptSource.trim()" @click="importLXScript">导入并启用脚本</el-button></div>
        <el-alert v-if="lxScriptFeedback" :title="lxScriptFeedback" :type="lxScriptFeedback.startsWith('导入失败') || lxScriptFeedback.startsWith('导入未开始') ? 'error' : lxScriptFeedback.startsWith('正在') ? 'info' : 'success'" :closable="false" show-icon />
      </div>
    </el-card>
    <el-card class="player-card player-settings-card" shadow="never">
      <template #header><div class="card-heading"><strong>已导入脚本</strong><el-tag effect="plain">{{ lxScripts.length }} 个</el-tag></div></template>
      <div v-if="lxScripts.length" class="lx-source-list">
        <div v-for="script in lxScripts" :key="script.id" class="lx-source-row">
          <div class="source-copy"><strong>{{ script.name }}</strong><span v-if="script.version || script.author">{{ [script.version, script.author].filter(Boolean).join(' · ') }}</span><span v-if="script.description">{{ script.description }}</span><span>社区扩展 action：{{ lxActionNames(script.extension_actions ?? []) }}</span><span>洛雪原生 action：{{ (script.native_actions ?? []).join('、') || '未声明' }}</span><span v-if="script.supported_sources?.length">支持音源：{{ script.supported_sources.join('、') }}</span><span>SHA-256：{{ script.id }}</span>
            <div v-if="script.last_test" class="lx-source-test-results"><div v-for="item in script.last_test.platforms" :key="item.platform" class="lx-source-test-result"><strong>{{ item.name || item.platform }}</strong><el-tag size="small" :type="item.status === 'passed' ? 'success' : item.status === 'unsupported' ? 'info' : 'danger'">{{ item.status === 'passed' ? '可播放' : item.status === 'unsupported' ? '不支持' : '失败' }}</el-tag><span>{{ item.message }}</span><small v-if="item.song">{{ item.song }}</small></div><small>最近检测：{{ new Date(script.last_test.tested_at).toLocaleString() }}</small></div>
            <div v-if="script.enabled" class="lx-init-status">
              <el-tag size="small" :type="script.initialization_status === 'ready' ? 'success' : script.initialization_status === 'failed' ? 'danger' : 'warning'">{{ script.initialization_status === 'ready' ? '启动初始化成功' : script.initialization_status === 'failed' ? '启动初始化失败' : '等待启动初始化' }}</el-tag>
              <span v-if="script.initialization_stage">阶段：{{ ({ create_runtime: '创建运行环境', initialize_script: '执行脚本初始化', read_capabilities: '读取平台能力', ready: '完成' } as Record<string, string>)[script.initialization_stage] ?? script.initialization_stage }}</span>
              <small v-if="script.initialization_at">最近初始化：{{ new Date(script.initialization_at).toLocaleString() }}</small>
            </div>
            <el-text v-if="script.last_error" type="danger" size="small">{{ script.last_error }}</el-text>
          </div>
          <div class="lx-source-actions"><el-switch :model-value="script.enabled" :disabled="lxScriptBusy" active-text="启用" inactive-text="停用" @change="toggleLXScript(script, Boolean($event))" /><el-button link type="success" :loading="testingLXScriptID === script.id" :disabled="!!testingLXScriptID || lxScriptBusy" @click="testLXScript(script)">测试音源</el-button><el-button link type="danger" :disabled="lxScriptBusy" @click="deleteLXScript(script)">删除</el-button></div>
        </div>
      </div>
      <el-empty v-else description="尚未导入 JS 音源脚本" :image-size="64" />
      <p class="hint">安全提示：只导入可信来源的脚本。脚本会在 QuickJS 隔离运行时中执行，但其允许的网络请求仍受宿主安全策略约束。</p>
    </el-card>
  </section>
</template>
<style scoped>
.music-page{display:flex;flex-direction:column;gap:20px;min-height:calc(100vh - 146px)}.page-heading{display:flex;align-items:flex-start;justify-content:space-between;gap:20px}.page-heading h2{margin:0;font-size:25px}.eyebrow{margin:0 0 9px;color:var(--app-accent);font-size:11px;letter-spacing:.15em}.description,.section-description,.source-copy span,.setting-row p,.hint{color:var(--el-text-color-secondary);font-size:13px}.section-description{margin:0 0 16px}.card-heading{display:flex;align-items:center;justify-content:space-between}.setting-list{display:flex;flex-direction:column}.setting-row{display:flex;align-items:center;justify-content:space-between;gap:24px;padding:14px 0;border-bottom:1px solid var(--app-border)}.setting-row p{margin:6px 0 0}.card-actions{display:flex;justify-content:flex-end;gap:10px;margin-top:20px}.hint{margin:12px 0 0}.lx-import-row{display:flex;gap:10px;align-items:center;min-width:min(520px,55%)}.lx-import-row :deep(.el-input){flex:1;min-width:180px}.lx-import-mode-row :deep(.el-radio-group){flex-shrink:0}.hidden-file-input{display:none}.lx-source-list{display:flex;flex-direction:column;margin-top:8px}.lx-source-row{display:flex;align-items:flex-start;justify-content:space-between;gap:16px;padding:12px 0;border-top:1px solid var(--app-border)}.lx-source-row .source-copy{min-width:0}.lx-source-row .source-copy span{overflow-wrap:anywhere}.source-copy{display:flex;flex-direction:column;gap:5px}.source-copy span{font-size:12px;color:var(--el-text-color-secondary)}.lx-source-actions{display:flex;align-items:center;gap:8px;flex-shrink:0}.lx-source-test-results{display:flex;flex-direction:column;gap:6px;width:100%;padding:8px 0 4px;border-top:1px dashed var(--app-border)}.lx-source-test-result{display:flex;align-items:center;gap:9px;flex-wrap:wrap;font-size:12px}.lx-source-test-result>span,.lx-source-test-result small{color:var(--el-text-color-secondary)}.lx-init-status{display:flex;align-items:center;gap:8px;flex-wrap:wrap;font-size:12px;color:var(--el-text-color-secondary)}@media(max-width:800px){.setting-row,.lx-source-row{align-items:flex-start;flex-direction:column}.lx-import-row{flex-direction:column;align-items:stretch}}
</style>
