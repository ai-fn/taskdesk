import { useState, useRef, useEffect, lazy, Suspense } from 'react'
import type { EmojiClickData, Theme as EmojiTheme } from 'emoji-picker-react'

const EmojiPicker = lazy(() => import('emoji-picker-react'))
import type { TaskList, ListGroup } from '@/types'
import { Button } from '@/components/ui/Button'
import { Input } from '@/components/ui/Input'
import { Modal } from '@/components/ui/Modal'

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
  const [name, setName]       = useState(list?.name ?? '')
  const [color, setColor]     = useState(list?.color ?? '#007AFF')
  const [groupId, setGroupId] = useState(list?.group_id ?? '')
  const [icon, setIcon]       = useState(list?.icon ?? '')
  const [showPicker, setShowPicker] = useState(false)
  const pickerRef = useRef<HTMLDivElement>(null)

  // Close picker when clicking outside it
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

  // The "custom" slot shows the current emoji if it's not a preset, or a pencil placeholder
  const isCustom = icon !== '' && !PRESET_ICONS.includes(icon)

  return (
    <Modal onClose={onCancel} className="max-w-sm">
      <div className="rounded-2xl bg-white p-6 shadow-xl">
        <h2 className="mb-4 text-lg font-semibold text-gray-900">
          {list ? 'Edit list' : 'New list'}
        </h2>

        <form onSubmit={handleSubmit} className="flex flex-col gap-4">
          <Input
            label="Name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="List name"
            autoFocus
          />

          {/* Color picker */}
          <div>
            <label className="mb-2 block text-sm font-medium text-gray-700">Color</label>
            <div className="grid grid-cols-6 gap-2">
              {COLORS.map((c) => (
                <button
                  key={c}
                  type="button"
                  onClick={() => setColor(c)}
                  className="h-8 w-8 rounded-full transition-transform hover:scale-110"
                  style={{
                    backgroundColor: c,
                    outline: color === c ? `3px solid ${c}` : 'none',
                    outlineOffset: '2px',
                  }}
                />
              ))}
            </div>
          </div>

          {/* Icon picker */}
          <div>
            <label className="mb-2 block text-sm font-medium text-gray-700">Icon</label>

            <div className="grid grid-cols-7 gap-1">
              {/* Slot 0 — opens emoji board */}
              <div className="relative" ref={pickerRef}>
                <button
                  type="button"
                  title="Pick any emoji"
                  onClick={() => setShowPicker((v) => !v)}
                  className={`flex h-9 w-9 items-center justify-center rounded-lg border-2 text-base transition-colors ${
                    showPicker || isCustom
                      ? 'border-indigo-500 bg-indigo-50'
                      : 'border-dashed border-gray-300 hover:border-gray-400'
                  }`}
                >
                  {isCustom ? icon : '😀'}
                </button>

                {showPicker && (
                  <div className="absolute left-0 top-11 z-[60]">
                    <Suspense fallback={<div className="flex h-[380px] w-[320px] items-center justify-center rounded-lg border bg-white text-sm text-gray-400">Loading…</div>}>
                      <EmojiPicker
                        onEmojiClick={handleEmojiClick}
                        theme={'light' as EmojiTheme}
                        width={320}
                        height={380}
                        searchPlaceholder="Search emoji..."
                        lazyLoadEmojis
                      />
                    </Suspense>
                  </div>
                )}
              </div>

              {/* Slots 1–6: first row of presets */}
              {PRESET_ICONS.slice(0, 6).map((p) => (
                <button
                  key={p}
                  type="button"
                  onClick={() => setIcon(p)}
                  className={`flex h-9 w-9 items-center justify-center rounded-lg border-2 text-base transition-colors ${
                    icon === p ? 'border-indigo-500 bg-indigo-50' : 'border-transparent hover:border-gray-200'
                  }`}
                >
                  {p}
                </button>
              ))}
            </div>

            {/* Rows 2–6: remaining presets + "no icon" slot */}
            {[6, 13, 20, 27].map((start) => (
              <div key={start} className="mt-1 grid grid-cols-7 gap-1">
                {start === 6 ? (
                  // "No icon" clear button in position 0 of row 2
                  <button
                    type="button"
                    title="No icon"
                    onClick={() => setIcon('')}
                    className={`flex h-9 w-9 items-center justify-center rounded-lg border-2 text-xs text-gray-400 transition-colors ${
                      !icon ? 'border-indigo-500 bg-indigo-50' : 'border-dashed border-gray-300 hover:border-gray-400'
                    }`}
                  >
                    ✕
                  </button>
                ) : (
                  <div />
                )}
                {PRESET_ICONS.slice(start, start + 6).map((p) => (
                  <button
                    key={p}
                    type="button"
                    onClick={() => setIcon(p)}
                    className={`flex h-9 w-9 items-center justify-center rounded-lg border-2 text-base transition-colors ${
                      icon === p ? 'border-indigo-500 bg-indigo-50' : 'border-transparent hover:border-gray-200'
                    }`}
                  >
                    {p}
                  </button>
                ))}
              </div>
            ))}
          </div>

          {/* Group selector */}
          {groups.length > 0 && (
            <div>
              <label className="mb-1 block text-sm font-medium text-gray-700">Group</label>
              <select
                value={groupId}
                onChange={(e) => setGroupId(e.target.value)}
                className="w-full rounded-lg border border-gray-300 px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
              >
                <option value="">No group</option>
                {groups.map((g) => (
                  <option key={g.id} value={g.id}>{g.name}</option>
                ))}
              </select>
            </div>
          )}

          <div className="flex justify-end gap-2 pt-2">
            <Button type="button" variant="secondary" onClick={onCancel}>Cancel</Button>
            <Button type="submit" loading={isLoading} disabled={!name.trim()}>
              {list ? 'Save' : 'Create'}
            </Button>
          </div>
        </form>
      </div>
    </Modal>
  )
}
