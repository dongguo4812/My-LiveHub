<script setup lang="ts">
import { useRoute } from 'vue-router'
import { computed } from 'vue'
const route = useRoute()
const title = computed(() => String(route.meta.title ?? '功能页面'))
const description = computed(() => String(route.meta.description ?? ''))
</script>

<template>
  <section class="placeholder-page">
    <div>
      <h2>{{ title }}</h2>
      <p>{{ description }}</p>
    </div>
    <el-card shadow="never">
      <el-empty description="页面入口已建立，业务功能将在后续阶段逐项接入" :image-size="100" />
    </el-card>
  </section>
</template>

<style scoped>
.placeholder-page { display: flex; flex-direction: column; gap: 22px; }
h2 { margin: 0; font-size: 24px; }
p { margin: 9px 0 0; color: var(--el-text-color-secondary); }
</style>
