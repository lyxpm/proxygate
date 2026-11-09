import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { api } from '@/api/client'
import type { EngineStatus } from '@/api/types'

export interface SystemInfo {
  version: string
  os: string
  arch: string
  go_version: string
  uptime_seconds: number
  engine: EngineStatus
}

export const useSystemStore = defineStore('system', () => {
  const info = ref<SystemInfo>()
  let pending: Promise<SystemInfo> | null = null

  const engine = computed(() => info.value?.engine)

  async function fetchInfo(force = false): Promise<SystemInfo | undefined> {
    if (!force && info.value) return info.value
    if (pending) return pending
    pending = api.get<SystemInfo>('/system/info')
    try {
      info.value = await pending
      return info.value
    } catch {
      return undefined
    } finally {
      pending = null
    }
  }

  return { info, engine, fetchInfo }
})
