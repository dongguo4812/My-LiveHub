<template>
  <section class="extensions-page">
    <header class="page-heading">
      <div>
        <h2>扩展管理</h2>
        <p>管理后续接入的第三方扩展。内置功能请前往「功能模块」管理。</p>
      </div>
      <el-tag type="info" effect="plain">待开发</el-tag>
    </header>
    <el-empty description="扩展中心功能待开发">
      <template #default>
        <p class="empty-hint">第三方扩展的发现、安装、更新与权限管理将在后续版本实现。LOL Hex 已迁移至「功能模块」，可在那里启动和停止。</p>
      </template>
    </el-empty>
  </section>
</template>

<style scoped>
.extensions-page { display: flex; flex-direction: column; gap: 22px; }
.page-heading { display: flex; align-items: center; justify-content: space-between; gap: 16px; }
.page-heading h2 { margin: 0; font-size: 24px; }
.page-heading p { margin: 9px 0 0; color: var(--el-text-color-secondary); }
.empty-hint { max-width: 520px; color: var(--el-text-color-secondary); line-height: 1.7; text-align: center; }
</style>
