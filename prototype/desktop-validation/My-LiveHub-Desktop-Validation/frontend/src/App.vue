<script setup lang="ts">
import { ref } from 'vue'
import HelloWorld from './components/HelloWorld.vue'
import { WindowService } from '../bindings/github.com/dongguo4812/My-LiveHub/prototype/desktop-validation'

const isSecondary = new URLSearchParams(window.location.search).get('window') === 'secondary'
const windowStatus = ref('尚未打开子窗口')

async function openSecondaryWindow() {
  try {
    windowStatus.value = await WindowService.OpenSecondaryWindow()
  } catch (error) {
    windowStatus.value = `打开失败：${String(error)}`
  }
}
</script>

<template>
  <main v-if="isSecondary" class="secondary">
    <p class="eyebrow">WINDOW LIFECYCLE VALIDATION</p>
    <h1>My-LiveHub 子窗口</h1>
    <p>这是独立于主窗口的第二个 Wails 窗口。</p>
    <p>请关闭此窗口，再从主窗口重新打开，验证窗口实例能否正确释放和重建。</p>
  </main>

  <main v-else class="main">
    <HelloWorld />
    <section class="window-test">
      <h2>多窗口生命周期验证</h2>
      <button type="button" @click="openSecondaryWindow">打开 / 恢复子窗口</button>
      <p role="status">{{ windowStatus }}</p>
      <small>重复点击应聚焦已有子窗口，而不是创建多个窗口。关闭子窗口后可再次创建。</small>
    </section>
  </main>
</template>

<style scoped>
.main {
  min-height: 100vh;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 20px;
  padding: 24px;
  box-sizing: border-box;
}
.window-test {
  width: min(620px, 100%);
  padding: 20px;
  border: 1px solid #34394b;
  border-radius: 12px;
  background: #12141e;
  color: #e8eaf2;
}
.window-test h2 {
  margin: 0 0 14px;
  font-size: 18px;
}
.window-test button {
  padding: 9px 16px;
  border: 0;
  border-radius: 7px;
  background: #5268d8;
  color: white;
  cursor: pointer;
}
.window-test p {
  min-height: 1.4em;
  margin: 12px 0 6px;
}
.window-test small {
  color: #a5abc0;
}
.secondary {
  min-height: 100vh;
  display: flex;
  flex-direction: column;
  justify-content: center;
  padding: 48px;
  box-sizing: border-box;
  background: #12141e;
  color: #e8eaf2;
}
.secondary .eyebrow {
  color: #8d9cff;
  letter-spacing: .12em;
  font-size: 12px;
}
.secondary h1 {
  font-size: 30px;
}
.secondary p {
  max-width: 560px;
  line-height: 1.7;
  color: #b9bfd2;
}
</style>
