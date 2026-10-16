import type { APIErrorBody, Expense, ExpenseList, ExpensePayload, Filters, Meta, Summary, User } from './types'

export class APIError extends Error {
  readonly status: number
  readonly code: string
  readonly fields: Record<string, string>

  constructor(status: number, body: APIErrorBody) {
    super(body.error?.message ?? `HTTP ${status}`)
    this.status = status
    this.code = body.error?.code ?? 'unknown_error'
    this.fields = body.error?.fields ?? {}
  }
}

async function request<T>(path: string, options: RequestInit = {}): Promise<T> {
  const response = await fetch(path, options)
  if (!response.ok) {
    let body: APIErrorBody = {}
    try {
      body = (await response.json()) as APIErrorBody
    } catch {
      // The status still gives the caller a useful failure mode.
    }
    throw new APIError(response.status, body)
  }
  if (response.status === 204) return undefined as T
  return (await response.json()) as T
}

function filterQuery(filters: Filters): URLSearchParams {
  const params = new URLSearchParams()
  for (const [key, value] of Object.entries(filters)) {
    if (value) params.set(key, value)
  }
  return params
}

export function getMeta(signal?: AbortSignal): Promise<Meta> {
  return request('/api/v1/meta', { signal })
}

export async function getUsers(signal?: AbortSignal): Promise<User[]> {
  const response = await request<{ items: User[] }>('/api/v1/users', { signal })
  return response.items
}

export function getExpenses(filters: Filters, limit: number, offset: number, signal?: AbortSignal): Promise<ExpenseList> {
  const params = filterQuery(filters)
  params.set('limit', String(limit))
  params.set('offset', String(offset))
  return request(`/api/v1/expenses?${params}`, { signal })
}

export function getSummary(filters: Filters, signal?: AbortSignal): Promise<Summary> {
  return request(`/api/v1/expenses/summary?${filterQuery(filters)}`, { signal })
}

export function createExpense(payload: ExpensePayload): Promise<Expense> {
  return request('/api/v1/expenses', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  })
}

export function updateExpense(id: string, version: string, payload: ExpensePayload): Promise<Expense> {
  return request(`/api/v1/expenses/${id}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ ...payload, expected_version: version }),
  })
}

export function deleteExpense(id: string, version: string): Promise<void> {
  return request(`/api/v1/expenses/${id}?expected_version=${encodeURIComponent(version)}`, { method: 'DELETE' })
}
