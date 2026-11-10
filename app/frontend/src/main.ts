import '@wailsio/runtime'
import {createApp} from 'vue'
import ElementPlus from 'element-plus'
import 'element-plus/dist/index.css'
import 'element-plus/theme-chalk/dark/css-vars.css'
import App from './App.vue'
import router from './router'
import {loadAppearance} from './services/appearance'
import './styles.css'
const app=createApp(App)
app.use(router).use(ElementPlus).mount('#app')
void loadAppearance()
