import { useTranslation } from 'react-i18next'
import { scorePassword, scoreToLevel, type StrengthLevel } from '@/lib/passwordStrength'

// Fill % and colour per level. Score 0 gets a sliver so the bar always shows.
const LEVEL_CONFIG: Record<StrengthLevel, { widthPct: number; color: string; labelClass: string }> = {
  weak:   { widthPct: 25,  color: '#f43f5e', labelClass: 'text-rose-500   dark:text-rose-400'   },
  fair:   { widthPct: 50,  color: '#f59e0b', labelClass: 'text-amber-500  dark:text-amber-400'  },
  good:   { widthPct: 75,  color: '#84cc16', labelClass: 'text-lime-600   dark:text-lime-400'   },
  strong: { widthPct: 100, color: '#10b981', labelClass: 'text-emerald-500 dark:text-emerald-400' },
}

/**
 * A single continuous progress bar that fills in four stages as the password
 * gets stronger. Renders nothing until the user starts typing.
 */
export function PasswordStrengthBar({ password }: { password: string }) {
  const { t } = useTranslation()
  if (!password) return null

  const score    = scorePassword(password)
  const level    = scoreToLevel(score)
  const { widthPct, color, labelClass } = LEVEL_CONFIG[level]
  // Score 0 = tiny sliver so the bar is always visible once typing starts.
  const fillPct  = score === 0 ? 8 : widthPct

  return (
    <div className="mt-2" aria-live="polite" aria-label={t('auth.password.strength.label')}>
      {/* Track */}
      <div className="h-1 w-full overflow-hidden rounded-full bg-gray-200 dark:bg-gray-700">
        {/* Fill — width + colour both transition smoothly */}
        <div
          className="h-full rounded-full"
          style={{
            width: `${fillPct}%`,
            backgroundColor: color,
            boxShadow: `0 0 8px 2px ${color}55`,
            transition: 'width 0.4s cubic-bezier(0.4, 0, 0.2, 1), background-color 0.3s ease, box-shadow 0.3s ease',
          }}
        />
      </div>

      {/* Label */}
      <p
        className={`mt-1 text-xs font-medium transition-colors duration-300 ${labelClass}`}
        style={{ transition: 'color 0.3s ease' }}
      >
        {t(`auth.password.strength.${level}`)}
      </p>
    </div>
  )
}
