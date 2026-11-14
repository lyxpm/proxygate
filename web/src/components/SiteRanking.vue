<template>
  <div class="site-ranking">
    <el-table
      v-if="items.length"
      :data="items.slice(0, limit)"
      size="small"
      style="width: 100%"
      row-class-name="site-ranking-row"
      @row-click="openSite"
    >
      <el-table-column prop="site_name" label="站点" min-width="130" show-overflow-tooltip />
      <el-table-column label="请求" width="90" align="right">
        <template #default="{ row }">{{ formatNumber(row.requests) }}</template>
      </el-table-column>
      <el-table-column label="入站" width="90" align="right">
        <template #default="{ row }">{{ formatBytes(row.inbound_bytes) }}</template>
      </el-table-column>
      <el-table-column label="出站" width="90" align="right">
        <template #default="{ row }">{{ formatBytes(row.outbound_bytes) }}</template>
      </el-table-column>
    </el-table>
    <div v-else class="empty site-ranking-empty">所选时间范围内暂无站点流量</div>
  </div>
</template>

<script setup lang="ts">
import { useRouter } from 'vue-router'
import { formatBytes, formatNumber } from '@/api/client'
import type { SiteTrafficStat } from '@/api/types'

withDefaults(defineProps<{ items: SiteTrafficStat[]; limit?: number }>(), { limit: 10 })

const router = useRouter()
function openSite(row: SiteTrafficStat) {
  router.push({ path: '/statistics', query: { site_code: row.site_code } })
}
</script>
