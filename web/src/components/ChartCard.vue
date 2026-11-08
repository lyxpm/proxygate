<template>
  <div class="card chart-card">
    <div class="card-body">
      <div class="chart-card-head">
        <div class="chart-card-heading">
          <div class="chart-card-title">{{ title }}</div>
          <div v-if="subtitle" class="chart-card-subtitle">{{ subtitle }}</div>
        </div>
        <div class="chart-card-actions">
          <slot name="actions" />
        </div>
      </div>

      <div class="chart-card-body">
        <div v-show="!empty" class="chart-card-canvas" v-loading="loading">
          <slot />
        </div>

        <div v-if="empty" class="chart-empty">
          <div class="chart-empty-icon">∅</div>
          <div>{{ emptyText }}</div>
        </div>

        <div v-if="error" class="chart-error" role="alert">
          <span>{{ error }}</span>
          <button type="button" class="chart-error-retry" @click="$emit('retry')">重试</button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
withDefaults(
  defineProps<{
    title: string
    subtitle?: string
    loading?: boolean
    empty?: boolean
    error?: string
    emptyText?: string
  }>(),
  {
    subtitle: '',
    loading: false,
    empty: false,
    error: '',
    emptyText: '所选时间范围内暂无流量数据',
  },
)

defineEmits<{ retry: [] }>()
</script>
