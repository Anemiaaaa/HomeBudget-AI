export type Category = 'food' | 'transport' | 'housing' | 'entertainment' | 'health' | 'shopping' | 'other'

export interface CategoryOption {
  code: Category
  label: string
}

export interface Meta {
  categories: CategoryOption[]
  currency: 'RUB'
  timezone: string
  today: string
}

export interface User {
  id: string
  display_name: string | null
  created_at: string
}

export interface Expense {
  id: string
  user_id: string
  amount: string
  category: Category
  spent_at: string
  comment: string | null
  created_at: string
  updated_at: string
  version: string
}

export interface ExpenseList {
  items: Expense[]
  total_count: number
  limit: number
  offset: number
}

export interface Summary {
  total_amount: string
  count: number
  by_category: Array<{ category: Category; amount: string; count: number }>
}

export interface Filters {
  user_id: string
  category: string
  date_from: string
  date_to: string
  query: string
}

export interface ExpensePayload {
  user_id: string
  amount: string
  category: Category
  spent_at: string
  comment?: string | null
}

export interface APIErrorBody {
  error?: {
    code?: string
    message?: string
    fields?: Record<string, string>
    request_id?: string
  }
}
