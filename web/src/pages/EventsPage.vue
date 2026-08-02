<script setup lang="ts">
import { useDashboardContext } from '../dashboardContext'
import { formatRelativeTime } from '../utils'

const { events } = useDashboardContext()
</script>

<template>
  <div class="page-stack">
    <header class="page-heading"><div><p class="eyebrow">EVENT STREAM</p><h1>事件记录</h1><p>查看映射发现、Cloudflare 同步和异常状态。</p></div><span class="count-chip">{{ events.length }}</span></header>
    <section class="panel event-panel">
      <div class="timeline">
        <div v-for="event in events" :key="event.id" class="timeline-item" :data-level="event.level">
          <i /><div><strong>{{ event.type }}</strong><p>{{ event.message }}</p></div><time>{{ formatRelativeTime(event.createdAt) }}</time>
        </div>
        <div v-if="events.length === 0" class="empty-state compact"><p>事件将在服务启动后出现在这里。</p></div>
      </div>
    </section>
  </div>
</template>
