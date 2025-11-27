import type { TokenStore } from './types'

let _token: string | null = null

export const MemoryTokenStore: TokenStore = {
  getToken() {
    return _token
  },
  setToken(token: string | null) {
    _token = token
  },
  clearToken() {
    _token = null
  },
}
