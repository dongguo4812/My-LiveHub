import {createRouter,createWebHashHistory} from 'vue-router'
import DashboardView from './views/DashboardView.vue'
import PlatformsView from './views/PlatformsView.vue'
import SettingsView from './views/SettingsView.vue'
import DanmakuView from './views/DanmakuView.vue'
import DanmakuOverlayView from './views/DanmakuOverlayView.vue'
import SimulatorView from './views/SimulatorView.vue'
import PlaceholderView from './views/PlaceholderView.vue'
const routes=[
{path:'/',name:'overview',component:DashboardView,meta:{title:'总览',section:'工作台'}},
{path:'/monitor',component:PlaceholderView,meta:{title:'实时监控',section:'工作台',description:'集中查看直播连接、事件流和模块运行状态。'}},
{path:'/platforms',component:PlatformsView,meta:{title:'平台管理',section:'直播接入',description:'管理直播平台适配器、账号授权和直播间连接。'}},
{path:'/simulator/bilibili',name:'bilibili-simulator',component:SimulatorView,meta:{title:'B站互动模拟器',standalone:true}},
{path:'/events',component:PlaceholderView,meta:{title:'事件中心',section:'互动中心',description:'汇总多平台事件，同时保留各平台原生事件信息。'}},
{path:'/users',component:PlaceholderView,meta:{title:'用户管理',section:'互动中心',description:'查看跨平台用户身份、档案和互动记录。'}},
{path:'/rules',component:PlaceholderView,meta:{title:'互动规则',section:'互动中心',description:'配置事件触发条件、动作和自动化流程。'}},
{path:'/modules',component:PlaceholderView,meta:{title:'功能模块',section:'功能模块',description:'管理弹幕、语音、音乐、视频等内置模块。'}},
{path:'/layouts',component:PlaceholderView,meta:{title:'综合展示',section:'展示管理',description:'编辑多个独立展示布局，并管理对应 OBS 浏览器地址。'}},
{path:'/obs',component:PlaceholderView,meta:{title:'OBS 管理',section:'展示管理',description:'由核心统一管理 OBS WebSocket 连接和操作。'}},
{path:'/extensions',component:PlaceholderView,meta:{title:'扩展管理',section:'扩展中心',description:'管理官方与第三方扩展、权限和独立进程状态。'}},
{path:'/danmaku',component:DanmakuView,meta:{title:'弹幕显示',section:'功能模块'}},
{path:'/danmaku/overlay',component:DanmakuOverlayView,meta:{title:'弹幕展示',standalone:true}},
{path:'/settings',component:SettingsView,meta:{title:'系统设置',section:'系统管理'}},
{path:'/logs',component:PlaceholderView,meta:{title:'日志诊断',section:'系统管理',description:'查看核心、平台适配器和扩展的运行日志。'}}]
export default createRouter({history:createWebHashHistory(),routes})
