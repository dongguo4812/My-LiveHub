<template>
  <section class="extensions-page">
    <header class="page-heading">
      <div>
        <h2>扩展管理</h2>
        <p>管理后续接入的第三方扩展。My-LiveHub 内置功能请前往「功能模块」管理。</p>
      </div>
    </header>
    <el-empty description="暂无第三方扩展。LOL Hex 已作为内置功能模块，请前往功能模块启动和停止。" />
  </section>
</template>

<style scoped>
.extensions-page { display: flex; flex-direction: column; gap: 22px; }
.page-heading h2 { margin: 0; font-size: 24px; }
.page-heading p { margin: 9px 0 0; color: var(--el-text-color-secondary); }
</style>
