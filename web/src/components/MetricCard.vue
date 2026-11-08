<template>
  <div class="metric-card" :class="`metric-card-${tone}`">
    <div class="metric-card-head">
      <span class="metric-card-label">{{ label }}</span>
      <slot name="icon" />
    </div>
    <div class="metric-card-value">
      <slot name="value">{{ value }}</slot>
      <span v-if="unit" class="metric-card-unit">{{ unit }}</span>
    </div>
    <div class="metric-card-foot">
      <slot name="foot">{{ helper }}</slot>
    </div>
  </div>
</template>

<script setup lang="ts">
withDefaults(
  defineProps<{
    label: string
    value?: string
    unit?: string
    helper?: string
    tone?: 'default' | 'danger' | 'warning'
  }>(),
  { value: '', helper: '', tone: 'default' },
)
</script>
