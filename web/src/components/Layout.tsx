import type { ReactNode } from 'react'
import { Navbar } from './Navbar'
import type { User } from '@/gen/user/v1/user_pb'

interface LayoutProps {
  children: ReactNode
  user: User | null
}

export function Layout({ children, user }: LayoutProps) {
  return (
    <div className="flex flex-col min-h-screen bg-background">
      <Navbar user={user} />
      <main className="flex-1 max-w-7xl w-full mx-auto px-6 py-8">{children}</main>
    </div>
  )
}
