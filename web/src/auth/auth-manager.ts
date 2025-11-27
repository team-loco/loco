import type { AuthManager } from './types'
import { MemoryTokenStore } from './token-store'

export const authManager: AuthManager = {
  tokenStore: MemoryTokenStore,

  login(token: string) {
    this.tokenStore.setToken(token)
  },

  logout() {
    this.tokenStore.clearToken()
  },

  getToken() {
    return this.tokenStore.getToken()
  },

  isAuthenticated() {
    return !!this.tokenStore.getToken()
  },
}
