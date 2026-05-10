export function ErrorMessage({ message }: { message: string }) {
  return (
    <div className="rounded-md bg-red-50 px-4 py-3 text-sm text-red-700 border border-red-200">
      {message}
    </div>
  )
}
