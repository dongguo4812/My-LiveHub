<script setup lang="ts">
import {onMounted,ref,watch} from 'vue'
import * as UpdateService from '../../bindings/github.com/dongguo4812/My-LiveHub/app/internal/desktop/updateservice.js'
type UpdateInfo = Awaited<ReturnType<typeof UpdateService.CheckForUpdates>>
import {appearance,appearanceBusy,appearanceError,loadAppearance,saveAppearance,logRetentionDays,logRetentionBusy,logRetentionError,saveLogRetentionDays,windowCloseBehavior,windowCloseBehaviorError,saveWindowCloseBehavior} from '../services/appearance'
const theme=ref('dark'),accentColor=ref('indigo')
const updateInfo=ref<UpdateInfo|null>(null),updateBusy=ref(false),installBusy=ref(false),updateError=ref('')
const skippedVersion=ref(localStorage.getItem('my-livehub-skipped-update-version')||''),updateDeferred=ref(false)
const showUpdateChoices=ref(false)
async function checkUpdate(){updateBusy.value=true;updateError.value='';updateDeferred.value=false;showUpdateChoices.value=false;try{updateInfo.value=await UpdateService.CheckForUpdates();showUpdateChoices.value=!!updateInfo.value?.available&&skippedVersion.value!==updateInfo.value.latest_version}catch(error){updateError.value=error instanceof Error?error.message:String(error)}finally{updateBusy.value=false}}
function deferUpdate(){updateDeferred.value=true;showUpdateChoices.value=false}
function skipUpdate(){if(!updateInfo.value?.latest_version)return;skippedVersion.value=updateInfo.value.latest_version;localStorage.setItem('my-livehub-skipped-update-version',skippedVersion.value);showUpdateChoices.value=false;updateDeferred.value=false}
async function installUpdate(){if(!updateInfo.value?.available)return;if(!window.confirm('确认下载并安装 My-LiveHub '+updateInfo.value.latest_version+'？安装时程序将自动退出并在完成后重新启动。'))return;installBusy.value=true;updateError.value='';try{await UpdateService.InstallUpdate()}catch(error){updateError.value=error instanceof Error?error.message:String(error);installBusy.value=false}}
const accents=[{value:'indigo',label:'蓝紫',color:'#7c8cff'},{value:'blue',label:'蓝色',color:'#5798e8'},{value:'violet',label:'紫色',color:'#a184e8'},{value:'teal',label:'青绿',color:'#43b9a6'},{value:'rose',label:'玫瑰',color:'#df829b'}]
watch(appearance,v=>{theme.value=v.theme;accentColor.value=v.accent_color},{immediate:true})
onMounted(()=>{void loadAppearance()})
async function apply(){await saveAppearance(theme.value,accentColor.value)}
async function saveRetention(){await saveLogRetentionDays(logRetentionDays.value)}
async function saveCloseBehavior(){await saveWindowCloseBehavior(windowCloseBehavior.value)}
</script>
<template><section class="settings-page"><div><p class="eyebrow">PERSONALIZE YOUR WORKSPACE</p><h2>系统设置</h2><p class="description">调整客户端外观和运行日志。设置保存在本地配置中，重启后继续生效。</p></div>
<el-alert v-if="appearanceError" :title="appearanceError" type="error" :closable="false" show-icon/>
<el-card shadow="never"><template #header><strong>程序更新</strong></template>
<div class="setting-row"><div><strong>当前版本：{{ updateInfo?.current_version || '尚未检查' }}</strong><p v-if="updateInfo">最新版本：{{ updateInfo.latest_version }} · {{ updateInfo.package_type==='installer'?'安装版':'免安装版' }}</p><p v-else>检查 GitHub Release，下载与当前安装形式对应的完整更新包，并在安装前校验 SHA256。</p></div><div class="retention-control"><el-button :loading="updateBusy" @click="checkUpdate">检查更新</el-button></div></div>
<el-alert v-if="updateInfo&&!updateInfo.available" title="当前已是最新版本" type="success" :closable="false" show-icon/>
<template v-if="updateInfo?.available"><el-alert v-if="showUpdateChoices" :title="'发现新版本 '+updateInfo.latest_version+'，更新包：'+updateInfo.asset_name+'（'+(updateInfo.asset_size/1024/1024).toFixed(1)+' MB）'" type="info" :closable="false" show-icon/><el-alert v-else-if="updateDeferred" title="已选择稍后更新，可再次点击“检查更新”查看。" type="info" :closable="false" show-icon/><el-alert v-else-if="skippedVersion===updateInfo.latest_version" :title="'已跳过版本 '+updateInfo.latest_version+'，有更高版本时会再次提示。'" type="info" :closable="false" show-icon/><div v-if="showUpdateChoices" class="retention-control update-actions"><el-button @click="deferUpdate">稍后更新</el-button><el-button type="primary" :loading="installBusy" @click="installUpdate">下载并安装更新</el-button><el-button type="danger" plain @click="skipUpdate">跳过此版本</el-button></div></template>
<el-alert v-if="installBusy" title="正在下载并校验更新包；如果连续 90 秒没有收到下载数据，更新器会自动中止并显示错误，不会一直卡住。" type="info" :closable="false" show-icon/><el-alert v-if="updateError" :title="updateError" type="error" :closable="false" show-icon/>
<p v-if="updateInfo?.release_notes" class="hint update-notes">{{ updateInfo.release_notes }}</p><p class="hint">更新会保留程序目录中的配置、缓存、日志、Cookie 和扩展数据。安装版沿用原安装目录；免安装版只替换程序文件，失败时会回滚。</p></el-card>

<el-card shadow="never"><template #header><strong>外观与皮肤</strong></template>
<div class="setting-row"><div><strong>界面主题</strong><p>选择客户端整体明暗风格。</p></div><el-radio-group v-model="theme" @change="apply"><el-radio-button value="dark">深色</el-radio-button><el-radio-button value="light">浅色</el-radio-button></el-radio-group></div>
<el-divider/><div class="setting-row"><div><strong>强调色</strong><p>用于主要按钮、选中导航、链接和重点图标。</p></div><div class="accent-options"><button v-for="item in accents" :key="item.value" class="accent-option" :class="{selected:accentColor===item.value}" :aria-label="item.label" :title="item.label" @click="accentColor=item.value;apply()"><span :style="{background:item.color}"/><small>{{item.label}}</small></button></div></div>
<div class="actions"><el-button type="primary" :loading="appearanceBusy" @click="apply">保存外观设置</el-button></div></el-card>

<el-card shadow="never"><template #header><strong>运行日志</strong></template>
<div class="setting-row"><div><strong>日志保留天数</strong><p>按日期分别保存日志，超过保留期限的日志会在启动时和修改设置后自动清理。</p></div><div class="retention-control"><el-input-number v-model="logRetentionDays" :min="1" :max="365" :step="1" controls-position="right"/><span>天</span><el-button type="primary" :loading="logRetentionBusy" @click="saveRetention">保存</el-button></div></div>
<el-alert v-if="logRetentionError" :title="logRetentionError" type="error" :closable="false" show-icon/><p class="hint">默认保留 14 天，可设置 1～365 天。当天日志文件会持续追加记录。</p></el-card>

<el-card shadow="never"><template #header><strong>窗口与系统托盘</strong></template>
<div class="setting-row"><div><strong>关闭主窗口时</strong><p>选择点击窗口关闭按钮后的行为。最小化到托盘后，后台直播连接和播放服务会继续运行。</p></div><div class="retention-control"><el-radio-group v-model="windowCloseBehavior" @change="saveCloseBehavior"><el-radio-button value="minimize_to_tray">最小化到系统托盘</el-radio-button><el-radio-button value="exit">退出程序</el-radio-button></el-radio-group></div></div>
<el-alert v-if="windowCloseBehaviorError" :title="windowCloseBehaviorError" type="error" :closable="false" show-icon/><p class="hint">程序最小化到托盘后，可通过托盘图标重新打开主界面，或在托盘菜单中选择退出。</p></el-card>

<el-card shadow="never"><template #header><strong>颜色语义</strong></template><div class="semantic-list"><div><i class="success"/>成功 / 正常</div><div><i class="warning"/>警告 / 等待</div><div><i class="danger"/>危险 / 失败</div><div><i class="info"/>普通 / 辅助</div></div><p class="hint">状态颜色保持固定语义；深浅主题会自动调整背景、文字、边框和控件对比度。</p></el-card></section></template>
<style scoped>
.settings-page{display:flex;flex-direction:column;gap:20px}.settings-page h2{margin:0;font-size:25px}.eyebrow{margin:0 0 9px;color:var(--app-accent);font-size:11px;letter-spacing:.15em}.description,.setting-row p,.hint{color:var(--el-text-color-secondary);font-size:13px}.setting-row{display:flex;align-items:center;justify-content:space-between;gap:24px}.setting-row p{margin:7px 0 0}.accent-options{display:flex;gap:12px}.accent-option{display:flex;align-items:center;gap:7px;padding:7px 9px;border:1px solid var(--app-border);border-radius:8px;background:var(--app-bg-control);color:var(--el-text-color-regular);cursor:pointer}.accent-option.selected{border-color:var(--app-accent);background:var(--app-bg-selected)}.accent-option span{width:15px;height:15px;border-radius:50%}.actions{display:flex;justify-content:flex-end;margin-top:22px}.retention-control{display:flex;align-items:center;gap:10px;flex-shrink:0}.retention-control>span{color:var(--el-text-color-secondary);font-size:13px}.semantic-list{display:flex;flex-wrap:wrap;gap:26px}.semantic-list div{display:flex;align-items:center;gap:8px;color:var(--el-text-color-regular);font-size:13px}.semantic-list i{width:10px;height:10px;border-radius:50%}.success{background:var(--app-success)}.warning{background:var(--app-warning)}.danger{background:var(--app-danger)}.info{background:var(--app-info)}.hint{margin:18px 0 0}.update-notes{white-space:pre-wrap;max-height:180px;overflow:auto}
</style>
