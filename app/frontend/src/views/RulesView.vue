<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import * as RuleService from '../../bindings/github.com/dongguo4812/My-LiveHub/app/internal/desktop/interactionruleservice.js'
type Rule = Awaited<ReturnType<typeof RuleService.GetRules>>[number]
const rules = ref<Rule[]>([])
const status = ref<Awaited<ReturnType<typeof RuleService.GetStatus>>>()
const loading = ref(false), saving = ref(false), error = ref('')
const dialogVisible = ref(false), editingIndex = ref(-1)
const eventOptions = [
 {label:'弹幕',value:'danmaku'},{label:'礼物',value:'gift'},{label:'醒目留言 SC',value:'super_chat'},{label:'舰队',value:'guard'},
 {label:'关注',value:'follow'},{label:'进入直播间',value:'welcome'},{label:'分享',value:'share'},{label:'点赞',value:'like'}
]
const eventNames = Object.fromEntries(eventOptions.map(item=>[item.value,item.label]))
const form = ref<any>(emptyRule())
const enabledCount = computed(()=>rules.value.filter(rule=>rule.enabled).length)
function emptyRule(){return {id:'',name:'',enabled:true,event_types:['gift'],source:'all',keyword:'',min_amount:0,action:'voice',text_template:'感谢 {user} 的支持！',cooldown_seconds:10}}
async function refresh(){
 loading.value=true
 try{const [items,current]=await Promise.all([RuleService.GetRules(),RuleService.GetStatus()]);rules.value=items||[];status.value=current;error.value=''}
 catch(cause){error.value=cause instanceof Error?cause.message:String(cause)}
 finally{loading.value=false}
}
function createRule(){editingIndex.value=-1;form.value=emptyRule();dialogVisible.value=true}
function editRule(index:number){editingIndex.value=index;form.value={...rules.value[index],event_types:[...rules.value[index].event_types]};dialogVisible.value=true}
async function saveRule(){
 if(!form.value.name.trim()||!form.value.event_types.length||!form.value.text_template.trim())return
 saving.value=true
 try{
  const next=[...rules.value],value={...form.value,id:form.value.id||globalThis.crypto?.randomUUID?.()||'rule-'+Date.now()}
  if(editingIndex.value<0)next.push(value);else next[editingIndex.value]=value
  await RuleService.SaveRules(next);dialogVisible.value=false;await refresh()
 }catch(cause){error.value=cause instanceof Error?cause.message:String(cause)}
 finally{saving.value=false}
}
async function deleteRule(index:number){try{await RuleService.SaveRules(rules.value.filter((_,i)=>i!==index));await refresh()}catch(cause){error.value=cause instanceof Error?cause.message:String(cause)}}
async function toggleRule(index:number,enabled:boolean){try{await RuleService.SaveRules(rules.value.map((rule,i)=>i===index?{...rule,enabled}:rule));await refresh()}catch(cause){error.value=cause instanceof Error?cause.message:String(cause);await refresh()}}
function describeEvents(values:string[]){return values.map(value=>eventNames[value]||value).join('、')}
onMounted(()=>{void refresh()})
</script>
<template>
<section class="rules-page">
 <header class="page-heading"><div><p class="eyebrow">EVENT AUTOMATION</p><h2>互动规则</h2><p class="muted">根据直播事件匹配条件，自动执行语音播报。规则保存在本地，重启后继续生效。</p></div><div class="heading-actions"><el-button @click="refresh" :loading="loading">刷新</el-button><el-button type="primary" @click="createRule">新建规则</el-button></div></header>
 <el-alert v-if="error" :title="'互动规则操作失败：'+error" type="error" :closable="false" show-icon/>
 <div class="stats-grid"><el-card shadow="never"><span>规则总数</span><strong>{{rules.length}}</strong></el-card><el-card shadow="never"><span>已启用</span><strong>{{enabledCount}}</strong></el-card><el-card shadow="never"><span>匹配事件</span><strong>{{status?.triggered??0}}</strong></el-card><el-card shadow="never"><span>成功执行</span><strong>{{status?.executed??0}}</strong></el-card></div>
 <el-alert type="info" :closable="false" show-icon title="当前支持的动作：语音播报。语音模块必须处于运行且启用状态，规则才会执行；冷却期间或语音模块不可用时会跳过，不会阻塞直播事件。"/>
 <el-card shadow="never"><el-table :data="rules" v-loading="loading" row-key="id" empty-text="还没有互动规则，点击右上角新建规则">
  <el-table-column label="规则名称" min-width="150"><template #default="{row}"><strong>{{row.name}}</strong><small class="subline">{{row.source==='all'?'全部来源':row.source==='live'?'真实事件':'模拟事件'}}</small></template></el-table-column>
  <el-table-column label="触发事件" min-width="180"><template #default="{row}">{{describeEvents(row.event_types)}}</template></el-table-column>
  <el-table-column label="附加条件" min-width="170"><template #default="{row}"><span v-if="row.keyword">关键词：{{row.keyword}}</span><span v-if="row.min_amount>0">金额 ≥ ¥{{Number(row.min_amount).toFixed(2)}}</span><span v-if="!row.keyword&&!row.min_amount">无</span></template></el-table-column>
  <el-table-column label="动作" width="110">语音播报</el-table-column><el-table-column label="冷却" width="90"><template #default="{row}">{{row.cooldown_seconds}} 秒</template></el-table-column>
  <el-table-column label="启用" width="90"><template #default="{row,$index}"><el-switch :model-value="row.enabled" @change="(value:boolean)=>toggleRule($index,value)"/></template></el-table-column>
  <el-table-column label="操作" width="140" fixed="right"><template #default="{ $index }"><el-button link type="primary" @click="editRule($index)">编辑</el-button><el-button link type="danger" @click="deleteRule($index)">删除</el-button></template></el-table-column>
 </el-table></el-card>
 <el-card shadow="never"><template #header><strong>执行状态</strong></template><div class="execution-info"><span>跳过次数：{{status?.skipped??0}}</span><span>最近执行：{{status?.last_rule||'暂无'}}</span><span v-if="status?.last_error" class="error-text">最近原因：{{status.last_error}}</span></div></el-card>
 <el-dialog v-model="dialogVisible" :title="editingIndex<0?'新建互动规则':'编辑互动规则'" width="min(650px, 94vw)" destroy-on-close>
  <el-form label-position="top">
   <el-form-item label="规则名称" required><el-input v-model="form.name" maxlength="40" show-word-limit placeholder="例如：高额礼物感谢"/></el-form-item>
   <el-form-item label="触发事件" required><el-checkbox-group v-model="form.event_types"><el-checkbox v-for="item in eventOptions" :key="item.value" :label="item.value">{{item.label}}</el-checkbox></el-checkbox-group></el-form-item>
   <el-form-item label="事件来源"><el-radio-group v-model="form.source"><el-radio value="all">全部</el-radio><el-radio value="live">真实事件</el-radio><el-radio value="simulation">模拟事件</el-radio></el-radio-group></el-form-item>
   <el-form-item label="关键词（选填）"><el-input v-model="form.keyword" placeholder="匹配用户昵称、弹幕内容或礼物名称"/></el-form-item>
   <el-form-item label="最低金额（选填）"><el-input-number v-model="form.min_amount" :min="0" :max="100000000" :precision="2" :step="1"/></el-form-item>
   <el-form-item label="执行动作"><el-input model-value="语音播报" disabled/></el-form-item>
   <el-form-item label="播报文本" required><el-input v-model="form.text_template" type="textarea" :rows="3" maxlength="200" show-word-limit/><div class="field-hint">变量：{user} 用户昵称、{content} 事件内容、{gift} 礼物名称、{amount} 金额、{platform} 平台标识。</div></el-form-item>
   <el-form-item label="冷却时间（秒）"><el-input-number v-model="form.cooldown_seconds" :min="0" :max="86400"/><div class="field-hint">同一条规则在冷却时间内不会重复执行；0 表示不限制。</div></el-form-item>
   <el-form-item label="启用规则"><el-switch v-model="form.enabled"/></el-form-item>
  </el-form>
  <template #footer><el-button @click="dialogVisible=false">取消</el-button><el-button type="primary" :loading="saving" :disabled="!form.name.trim()||!form.event_types.length||!form.text_template.trim()" @click="saveRule">保存规则</el-button></template>
 </el-dialog>
</section>
</template>
<style scoped>
.rules-page{display:flex;flex-direction:column;gap:18px;min-width:0}.page-heading{display:flex;justify-content:space-between;align-items:flex-start;gap:18px}.eyebrow{margin:0 0 8px;color:var(--el-color-primary);font-size:11px;letter-spacing:.15em}.page-heading h2{margin:0;font-size:25px}.muted{margin:8px 0 0;color:var(--el-text-color-secondary);font-size:13px}.heading-actions{display:flex;gap:8px}.stats-grid{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:12px}.stats-grid span{display:block;color:var(--el-text-color-secondary);font-size:12px}.stats-grid strong{display:block;margin-top:10px;font-size:24px;font-variant-numeric:tabular-nums}.subline,.field-hint{display:block;margin-top:5px;color:var(--el-text-color-secondary);font-size:11px}.execution-info{display:flex;flex-wrap:wrap;gap:20px;color:var(--el-text-color-secondary);font-size:13px}.error-text{color:var(--el-color-danger)}@media(max-width:800px){.stats-grid{grid-template-columns:repeat(2,minmax(0,1fr))}.page-heading{flex-direction:column}}@media(max-width:520px){.stats-grid{grid-template-columns:1fr 1fr}}
</style>