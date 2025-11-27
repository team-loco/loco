import { createContext, useContext, useState, type ReactNode } from 'react'
import { authManager } from './auth-manager'

interface AuthContextType {
  token: string | null
  login: (token: string) => void
  logout: () => void
  isAuthenticated: boolean
}

const AuthContext = createContext<AuthContextType | null>(null)

export function AuthProvider({ children }: { children: ReactNode }) {
  const [token, setTokenState] = useState(authManager.getToken())

  const login = (newToken: string) => {
    authManager.login(newToken)
    setTokenState(newToken)
  }

  const logout = () => {
    authManager.logout()
    setTokenState(null)
  }

  return (
    <AuthContext.Provider
      value={{
        token,
        login,
        logout,
        isAuthenticated: !!token,
      }}
    >
      {children}
    </AuthContext.Provider>
  )
}

export function useAuth() {
  const ctx = useContext(AuthContext)
  if (!ctx) {
    throw new Error('useAuth must be used inside AuthProvider')
  }
  return ctx
}
