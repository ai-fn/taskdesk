import { useState, useRef, useEffect } from 'react'
import type { TaskList } from '@/types'
import type { TaskPrefill } from './TaskForm'
import { parseTask } from '@/api/tasks'
import { Button } from '@/components/ui/Button'
import { ErrorMessage } from '@/components/ui/ErrorMessage'
import { Spinner } from '@/components/ui/Spinner'
import axios from 'axios'

const LOADING_MESSAGES = [
  'Counting bits',
  'Consulting oracle',
  'Wrangling tokens',
  'Summoning Claude',
  'Decoding intent',
  'Brewing context',
  'Parsing quarks',
  'Assembling task',
]

interface Props {
  lists: TaskList[]
  onParsed: (prefill: TaskPrefill) => void
  onCancel: () => void
}

export function NaturalLanguageInput({ lists, onParsed, onCancel }: Props) {
  const [text, setText] = useState('')
  const [isParsing, setIsParsing] = useState(false)
  const [error, setError] = useState('')
  const [msgIdx, setMsgIdx] = useState(0)
  const [msgVisible, setMsgVisible] = useState(true)
  const [dots, setDots] = useState(1)
  const textareaRef = useRef<HTMLTextAreaElement>(null)
  const abortRef = useRef<AbortController | null>(null)

  useEffect(() => {
    textareaRef.current?.focus()
  }, [])

  useEffect(() => {
    if (!isParsing) { setDots(1); return }
    const id = setInterval(() => setDots((d) => (d >= 3 ? 1 : d + 1)), 350)
    return () => clearInterval(id)
  }, [isParsing])

  // Cycle message every 1800ms with a 150ms fade-out before the swap
  useEffect(() => {
    if (!isParsing) { setMsgIdx(0); setMsgVisible(true); return }
    const id = setInterval(() => {
      setMsgVisible(false)
      setTimeout(() => {
        setMsgIdx((i) => (i + 1) % LOADING_MESSAGES.length)
        setMsgVisible(true)
      }, 150)
    }, 1800)
    return () => clearInterval(id)
  }, [isParsing])

  async function handleClick() {
    if (isParsing) {
      abortRef.current?.abort()
      return
    }

    const trimmed = text.trim()
    if (!trimmed) return

    const controller = new AbortController()
    abortRef.current = controller
    setIsParsing(true)
    setError('')

    try {
      const listNames = lists.map((l) => l.name)
      const result = await parseTask(trimmed, listNames, controller.signal)

      let listId: string | undefined
      if (result.list_name) {
        const lower = result.list_name.toLowerCase()
        const exact = lists.find((l) => l.name.toLowerCase() === lower)
        const partial = exact ?? lists.find((l) => l.name.toLowerCase().includes(lower))
        listId = partial?.id
      }

      onParsed({
        title:       result.title,
        description: result.description,
        priority:    result.priority,
        due_at:      result.due_at,
        list_id:     listId,
        tags:        result.tags,
        flagged:     result.flagged,
        subtasks:    result.subtasks,
      })
    } catch (err) {
      if (axios.isCancel(err) || (err instanceof Error && err.name === 'CanceledError')) {
        // Aborted by user — reset silently
      } else if (axios.isAxiosError(err) && err.response?.data?.error) {
        setError(err.response.data.error as string)
      } else {
        setError('Could not parse task. Try rephrasing.')
      }
    } finally {
      setIsParsing(false)
    }
  }

  function handleKeyDown(e: React.KeyboardEvent) {
    if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
      handleClick()
    }
    if (e.key === 'Escape' && !isParsing) {
      onCancel()
    }
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-col gap-1">
        <label className="text-sm font-medium text-gray-700 dark:text-gray-300">
          Describe your task
        </label>
        <textarea
          ref={textareaRef}
          value={text}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={handleKeyDown}
          rows={4}
          disabled={isParsing}
          placeholder='e.g. "Prepare deck for Meridian pitch next Friday, high priority, Client Work list"'
          className="resize-none rounded-md border border-gray-300 dark:border-gray-600 px-3 py-2 text-sm shadow-sm focus:border-indigo-500 focus:outline-none focus:ring-1 focus:ring-indigo-500 bg-white dark:bg-gray-800 text-gray-900 dark:text-gray-100 placeholder:text-gray-400 dark:placeholder:text-gray-500 disabled:opacity-60"
        />

        {/* Status line: hint when idle, animated progress when parsing */}
        <div className="flex items-center gap-1.5 min-h-[1.25rem]">
          {isParsing && <Spinner className="h-3 w-3 shrink-0 text-indigo-500" />}
          <p className="text-xs text-gray-400 dark:text-gray-500">
            {isParsing ? (
              <span style={{ opacity: msgVisible ? 1 : 0, transition: 'opacity 0.15s ease' }}>
                {LOADING_MESSAGES[msgIdx]}
                <span className="inline-block w-4 text-left">{'.'.repeat(dots)}</span>
              </span>
            ) : (
              'Include title, due date, priority, list — Claude will extract them. ⌘↵ to parse.'
            )}
          </p>
        </div>
      </div>

      {error && <ErrorMessage message={error} />}

      <div className="flex justify-end pt-1">
        <Button
          type="button"
          variant={isParsing ? 'secondary' : 'primary'}
          onClick={handleClick}
          disabled={!isParsing && !text.trim()}
        >
          {isParsing ? '✕ Cancel' : '✨ Parse'}
        </Button>
      </div>
    </div>
  )
}
