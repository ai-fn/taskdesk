// OAuthButtons renders the "Continue with Google / Discord" pair shown on
// both the Login and Signup pages.
import { useTranslation } from 'react-i18next'

export function oauthErrorMessage(code: string | null): string | null {
  if (!code) return null
  // Keys are resolved at render time via t(); return the raw key here.
  const known = [
    'state_mismatch', 'provider_denied', 'exchange_failed',
    'signin_failed', 'missing_code', 'state_gen_failed',
  ]
  return known.includes(code) ? `auth.oauth.errors.${code}` : 'auth.login.failed'
}

export function OAuthButtons() {
  const { t } = useTranslation()
  return (
    <>
      <div className="relative my-4 flex items-center">
        <div className="flex-1 border-t border-gray-200 dark:border-gray-700" />
        <span className="mx-3 text-xs uppercase tracking-wide text-gray-400 dark:text-gray-500">
          {t('auth.oauth.or')}
        </span>
        <div className="flex-1 border-t border-gray-200 dark:border-gray-700" />
      </div>

      <div className="flex flex-col gap-2">
        <a
          href="/api/v1/auth/oauth/google"
          className="flex items-center justify-center gap-2 rounded-md border border-gray-300 dark:border-gray-600 bg-white dark:bg-gray-800 px-4 py-2 text-sm font-medium text-gray-700 dark:text-gray-200 hover:bg-gray-50 dark:hover:bg-gray-700 transition-colors"
        >
          <GoogleIcon />
          {t('auth.oauth.continueWithGoogle')}
        </a>
        <a
          href="/api/v1/auth/oauth/discord"
          className="flex items-center justify-center gap-2 rounded-md border border-transparent bg-[#5865F2] hover:bg-[#4752C4] px-4 py-2 text-sm font-medium text-white transition-colors"
        >
          <DiscordIcon />
          {t('auth.oauth.continueWithDiscord')}
        </a>
      </div>
    </>
  )
}

function GoogleIcon() {
  return (
    <svg viewBox="0 0 18 18" className="h-4 w-4" aria-hidden="true">
      <path d="M17.64 9.2c0-.64-.06-1.25-.16-1.84H9v3.49h4.84c-.21 1.13-.84 2.09-1.79 2.73v2.26h2.9c1.7-1.56 2.69-3.87 2.69-6.64z" fill="#4285F4"/>
      <path d="M9 18c2.43 0 4.47-.81 5.96-2.18l-2.9-2.26c-.81.54-1.84.86-3.06.86-2.35 0-4.34-1.59-5.05-3.72H.96v2.33C2.44 15.98 5.48 18 9 18z" fill="#34A853"/>
      <path d="M3.95 10.71A5.41 5.41 0 0 1 3.66 9c0-.59.1-1.17.29-1.71V4.96H.96A8.99 8.99 0 0 0 0 9c0 1.45.35 2.83.96 4.04l2.99-2.33z" fill="#FBBC05"/>
      <path d="M9 3.58c1.32 0 2.51.46 3.44 1.35l2.58-2.58C13.46.89 11.43 0 9 0 5.48 0 2.44 2.02.96 4.96l2.99 2.33C4.66 5.16 6.65 3.58 9 3.58z" fill="#EA4335"/>
    </svg>
  )
}

function DiscordIcon() {
  return (
    <svg viewBox="0 0 71 55" className="h-4 w-4" aria-hidden="true">
      <path
        fill="currentColor"
        d="M60.1 4.9A58.5 58.5 0 0 0 45.6.5l-.6 1.4a54 54 0 0 0-16.4 0L28 .5a58.5 58.5 0 0 0-14.5 4.4A60 60 0 0 0 .1 41a59 59 0 0 0 17.9 9c1.4-2 2.6-4 3.6-6.2a39 39 0 0 1-5.7-2.7l1.4-1.1a42 42 0 0 0 36 0l1.4 1.1c-1.8 1-3.7 2-5.7 2.7 1 2.2 2.2 4.2 3.6 6.2a59 59 0 0 0 17.9-9 59 59 0 0 0-10.4-36zM23.7 35.4c-3.4 0-6.3-3.2-6.3-7.1s2.7-7.1 6.3-7.1c3.6 0 6.4 3.2 6.3 7.1 0 4-2.7 7.1-6.3 7.1zm23.6 0c-3.4 0-6.3-3.2-6.3-7.1s2.8-7.1 6.3-7.1c3.6 0 6.4 3.2 6.3 7.1 0 4-2.7 7.1-6.3 7.1z"
      />
    </svg>
  )
}
