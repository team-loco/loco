export interface TokenStore {
  getToken(): string | null
  setToken(token: string | null): void
  clearToken(): void
}

export interface AuthManager {
  tokenStore: TokenStore
  login(token: string): void
  logout(): void
  getToken(): string | null
  isAuthenticated(): boolean
}
