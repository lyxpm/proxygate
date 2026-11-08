import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api } from '@/api/client'
import type { User } from '@/api/types'

export const useAuthStore = defineStore('auth', () => {
  const user = ref<User | null>(null)
  const loading = ref(false)

  async function login(username: string, password: string, totpCode = '') {
    const data = await api.post<{ user: User }>('/auth/login', { username, password, totp_code: totpCode })
    user.value = data.user
    return data.user
  }

  async function fetchMe() {
    if (user.value) return user.value
    const data = await api.get<{ user: User }>('/auth/me')
    user.value = data.user
    return data.user
  }

  async function logout() {
    try {
      await api.post('/auth/logout')
    } finally {
      user.value = null
    }
  }

  function clear() {
    user.value = null
  }

  return { user, loading, login, fetchMe, logout, clear }
})
