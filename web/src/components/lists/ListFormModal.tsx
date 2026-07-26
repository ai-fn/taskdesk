import { useState, useRef, useEffect, lazy, Suspense } from 'react'
import { useTranslation } from 'react-i18next'
import type { EmojiClickData, Theme as EmojiTheme } from 'emoji-picker-react'

const EmojiPicker = lazy(() => import('emoji-picker-react'))
import type { TaskList, ListGroup } from '@/types'
import { Button } from '@/components/ui/Button'
import { Input } from '@/components/ui/Input'
import { Modal } from '@/components/ui/Modal'
import { useTheme } from '@/context/ThemeContext'

const COLORS = [
  '#FF3B30', '#FF9500', '#FFCC00', '#34C759',
  '#007AFF', '#5856D6', '#FF2D55', '#AC8E68',
  '#8E8E93', '#30B0C7', '#32ADE6', '#1C1C1E',
]

const PRESET_ICONS = [
  '🏠', '💼', '🛒', '📚', '🎯', '🎮',
  '🏋️', '✈️', '🍕', '💡', '⭐', '🔥',
  '💰', '🎵', '🌿', '🤝', '🧠', '📅',
  '⚙️', '🎨', '📝', '🏆', '❤️', '🚀',
  '🎭', '🌟', '💎', '🐶', '🌍', '⚽',
  '🎓', '🏥', '🍎', '☕', '🔑', '🎁',
]

interface Props {
  list?: TaskList
  groups?: ListGroup[]
  onSave: (name: string, color: string, groupId?: string, icon?: string) => void
  onCancel: () => void
  isLoading?: boolean
}

export function ListFormModal({ list, groups = [], onSave, onCancel, isLoading }: Props) {
  const { t } = useTranslation()
  const [name, setName]       = useState(list?.name ?? '')
  const [color, setColor]     = useState(list?.color ?? '#007AFF')
  const [groupId, setGroupId] = useState(list?.group_id ?? '')
  const [icon, setIcon]       = useState(list?.icon ?? '')
  const [showPicker, setShowPicker] = useState(false)
  const pickerRef = useRef<HTMLDivElement>(null)
  const { theme } = useTheme()
  const emojiTheme = (theme === 'dark' ? 'dark' : theme === 'light' ? 'light' : 'auto') as EmojiTheme

  useEffect(() => {
    if (!showPicker) return
    function onPointerDown(e: PointerEvent) {
      if (pickerRef.current && !pickerRef.current.contains(e.target as Node)) {
        setShowPicker(false)
      }
    }
    document.addEventListener('pointerdown', onPointerDown)
    return () => document.removeEventListener('pointerdown', onPointerDown)
  }, [showPicker])

  function handleEmojiClick(data: EmojiClickData) {
    setIcon(data.emoji)
    setShowPicker(false)
  }

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (!name.trim()) return
    onSave(name.trim(), color, groupId || undefined, icon || undefined)
  }

  const isCustom = icon !== '' && !PRESET_ICONS.includes(icon)

  const iconBtn = (active: boolean, dashed = false) =>
    `flex aspect-square w-full items-center justify-center overflow-hidden rounded-lg border-2 text-lg leading-none transition-colors ${
      active
        ? 'border-indigo-500 bg-indigo-50 dark:bg-indigo-900/30'
        : dashed
          ? 'border-dashed border-gray-300 dark:border-gray-600 hover:border-gray-400 dark:hover:border-gray-500'
          : 'border-transparent hover:border-gray-200 dark:hover:border-gray-600'
    }`

  return (
    <Modal onClose={onCancel} className="max-w-sm">
      <div className="rounded-2xl bg-white dark:bg-gray-800 shadow-xl p-5">
          <h2 className="mb-3 text-base font-semibold text-gray-900 dark:text-gray-100">
            {list ? t('lists.form.editTitle') : t('lists.form.newTitle')}
          </h2>

          <form onSubmit={handleSubmit} className="flex flex-col gap-3">
            <Input
              label={t('lists.form.nameLabel')}
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder={t('lists.form.namePlaceholder')}
              autoFocus
            />

            {/* Color picker */}
            <div>
              <label className="mb-1.5 block text-xs font-medium text-gray-700 dark:text-gray-300">
                {t('lists.form.colorLabel')}
              </label>
              <div className="grid grid-cols-6 gap-1.5">
                {COLORS.map((c) => (
                  <button
                    key={c}
                    type="button"
                    onClick={() => setColor(c)}
                    className="aspect-square w-full rounded-full transition-transform hover:scale-110"
                    style={{
                      backgroundColor: c,
                      outline: color === c ? `3px solid ${c}` : 'none',
                      outlineOffset: '2px',
                    }}
                  />
                ))}
              </div>
            </div>

            {/* Icon picker — all slots in one grid, identical size */}
            <div>
              <label className="mb-1.5 block text-xs font-medium text-gray-700 dark:text-gray-300">
                {t('lists.form.iconLabel')}
              </label>
              <div className="grid grid-cols-8 gap-1">
                {/* Slot 0: open emoji picker */}
                <div className="relative" ref={pickerRef}>
                  <button
                    type="button"
                    title={t('lists.form.iconPickerPlaceholder')}
                    onClick={() => setShowPicker((v) => !v)}
                    className={iconBtn(showPicker || isCustom, !showPicker && !isCustom)}
                  >
                    {isCustom ? icon : '😀'}
                  </button>
                  {showPicker && (
                    <div className="absolute left-0 top-[calc(100%+4px)] z-[60]">
                      <Suspense fallback={
                        <div className="flex h-[380px] w-[320px] items-center justify-center rounded-lg border bg-white dark:bg-gray-800 dark:border-gray-700 text-sm text-gray-400">
                          {t('common.loading')}
                        </div>
                      }>
                        <EmojiPicker
                          onEmojiClick={handleEmojiClick}
                          theme={emojiTheme}
                          width={320}
                          height={380}
                          searchPlaceholder={t('lists.form.iconSearch')}
                          lazyLoadEmojis
                        />
                      </Suspense>
                    </div>
                  )}
                </div>

                {/* Slot 1: clear / no icon */}
                <button
                  type="button"
                  title={t('lists.form.noIcon')}
                  onClick={() => setIcon('')}
                  className={`${iconBtn(!icon, !!icon)} text-sm text-gray-400 dark:text-gray-500`}
                >
                  ✕
                </button>

                {/* Slots 2–37: presets */}
                {PRESET_ICONS.map((p) => (
                  <button
                    key={p}
                    type="button"
                    onClick={() => setIcon(p)}
                    className={iconBtn(icon === p)}
                  >
                    {p}
                  </button>
                ))}
              </div>
            </div>

            {/* Group selector */}
            {groups.length > 0 && (
              <div>
                <label className="mb-1 block text-xs font-medium text-gray-700 dark:text-gray-300">
                  {t('lists.form.groupLabel')}
                </label>
                <select
                  value={groupId}
                  onChange={(e) => setGroupId(e.target.value)}
                  className="w-full rounded-lg border border-gray-300 dark:border-gray-600 px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500 bg-white dark:bg-gray-700 text-gray-900 dark:text-gray-100"
                >
                  <option value="">{t('lists.form.noGroup')}</option>
                  {groups.map((g) => (
                    <option key={g.id} value={g.id}>{g.name}</option>
                  ))}
                </select>
              </div>
            )}

            <div className="flex flex-col-reverse gap-2 pt-2 sm:flex-row sm:justify-end">
              <Button type="button" variant="secondary" onClick={onCancel} className="w-full justify-center sm:w-auto">
                {t('common.cancel')}
              </Button>
              <Button type="submit" loading={isLoading} disabled={!name.trim()} className="w-full justify-center sm:w-auto">
                {list ? t('common.save') : t('common.create')}
              </Button>
            </div>
          </form>
      </div>
    </Modal>
  )
}
