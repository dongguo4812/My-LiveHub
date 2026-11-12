<script setup lang="ts">
import {onMounted,ref,watch} from 'vue'
import {appearance,appearanceBusy,appearanceError,loadAppearance,saveAppearance} from '../services/appearance'
const theme=ref('dark'),accentColor=ref('indigo')
const accents=[{value:'indigo',label:'蓝紫',color:'#7c8cff'},{value:'blue',label:'蓝色',color:'#5798e8'},{value:'violet',label:'紫色',color:'#a184e8'},{value:'teal',label:'青绿',color:'#43b9a6'},{value:'rose',label:'玫瑰',color:'#df829b'}]
watch(appearance,v=>{theme.value=v.theme;accentColor.value=v.accent_color},{immediate:true})
onMounted(()=>{void loadAppearance()})
async function apply(){await saveAppearance(theme.value,accentColor.value)}
</script>
<template><section class="settings-page"><div><p class="eyebrow">PERSONALIZE YOUR WORKSPACE</p><h2>系统设置</h2><p class="description">调整客户端外观。设置保存在本地配置中，重启后继续生效。</p></div>
<el-alert v-if="appearanceError" :title="appearanceError" type="error" :closable="false" show-icon/>
<el-card shadow="never"><template #header><strong>外观与皮肤</strong></template>
<div class="setting-row"><div><strong>界面主题</strong><p>选择客户端整体明暗风格。</p></div><el-radio-group v-model="theme" @change="apply"><el-radio-button value="dark">深色</el-radio-button><el-radio-button value="light">浅色</el-radio-button></el-radio-group></div>
<el-divider/><div class="setting-row"><div><strong>强调色</strong><p>用于主要按钮、选中导航、链接和重点图标。</p></div><div class="accent-options"><button v-for="item in accents" :key="item.value" class="accent-option" :class="{selected:accentColor===item.value}" :aria-label="item.label" :title="item.label" @click="accentColor=item.value;apply()"><span :style="{background:item.color}"/><small>{{item.label}}</small></button></div></div>
<div class="actions"><el-button type="primary" :loading="appearanceBusy" @click="apply">保存外观设置</el-button></div></el-card>

<el-card shadow="never"><template #header><strong>颜色语义</strong></template><div class="semantic-list"><div><i class="success"/>成功 / 正常</div><div><i class="warning"/>警告 / 等待</div><div><i class="danger"/>危险 / 失败</div><div><i class="info"/>普通 / 辅助</div></div><p class="hint">状态颜色保持固定语义；深浅主题会自动调整背景、文字、边框和控件对比度。</p></el-card></section></template>
<style scoped>
.settings-page{display:flex;flex-direction:column;gap:20px}.settings-page h2{margin:0;font-size:25px}.eyebrow{margin:0 0 9px;color:var(--app-accent);font-size:11px;letter-spacing:.15em}.description,.setting-row p,.hint{color:var(--el-text-color-secondary);font-size:13px}.setting-row{display:flex;align-items:center;justify-content:space-between;gap:24px}.setting-row p{margin:7px 0 0}.accent-options{display:flex;gap:12px}.accent-option{display:flex;align-items:center;gap:7px;padding:7px 9px;border:1px solid var(--app-border);border-radius:8px;background:var(--app-bg-control);color:var(--el-text-color-regular);cursor:pointer}.accent-option.selected{border-color:var(--app-accent);background:var(--app-bg-selected)}.accent-option span{width:15px;height:15px;border-radius:50%}.actions{display:flex;justify-content:flex-end;margin-top:22px}.semantic-list{display:flex;flex-wrap:wrap;gap:26px}.semantic-list div{display:flex;align-items:center;gap:8px;color:var(--el-text-color-regular);font-size:13px}.semantic-list i{width:10px;height:10px;border-radius:50%}.success{background:var(--app-success)}.warning{background:var(--app-warning)}.danger{background:var(--app-danger)}.info{background:var(--app-info)}.hint{margin:18px 0 0}
</style>
