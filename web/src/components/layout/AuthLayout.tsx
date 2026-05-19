import { ThemeToggle } from '@/components/ui/ThemeToggle'

export function AuthLayout({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex min-h-screen items-center justify-center bg-gray-50 dark:bg-gray-950 px-4">
      <div className="w-full max-w-sm rounded-xl bg-white dark:bg-gray-900 p-8 shadow-md dark:shadow-gray-800/30">
        <div className="mb-6 flex justify-end">
          <ThemeToggle />
        </div>
        {children}
      </div>
    </div>
  )
}
