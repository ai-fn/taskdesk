const UNITS = ['B', 'KB', 'MB', 'GB', 'TB'] as const

export function formatBytes(bytes: number, locale: string): string {
  if (bytes === 0) return `0 B`
  const i = Math.min(Math.floor(Math.log2(bytes) / 10), UNITS.length - 1)
  const value = bytes / Math.pow(1024, i)
  return (
    new Intl.NumberFormat(locale, { maximumFractionDigits: 1 }).format(value) +
    ' ' +
    UNITS[i]
  )
}
