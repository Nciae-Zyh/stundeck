<script setup lang="ts">
import SecurityPanel from '../components/SecurityPanel.vue'
</script>

<template>
  <div class="page-stack narrow-page">
    <header class="page-heading"><div><p class="eyebrow">SECURITY</p><h1>安全设置</h1><p>控制访问边界、允许的主机名与双因素认证。</p></div></header>
    <SecurityPanel />
  </div>
</template>
