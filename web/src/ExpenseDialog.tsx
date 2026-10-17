import { useState, type FormEvent } from 'react'
import { APIError, createExpense, updateExpense } from './api'
import type { Category, Expense, ExpensePayload, Meta, User } from './types'

interface Props {
  expense: Expense | null
  meta: Meta
  users: User[]
  onClose: () => void
  onSaved: () => void
}

export function ExpenseDialog({ expense, meta, users, onClose, onSaved }: Props) {
  const [form, setForm] = useState<ExpensePayload>(() => ({
    user_id: expense?.user_id ?? users[0]?.id ?? '',
    amount: expense?.amount ?? '',
    category: expense?.category ?? 'other',
    spent_at: expense?.spent_at ?? meta.today,
    comment: expense?.comment ?? '',
  }))
  const [fields, setFields] = useState<Record<string, string>>({})
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setSaving(true)
    setError('')
    setFields({})
    try {
      const payload = { ...form, amount: normalizeAmount(form.amount) }
      if (expense) {
        await updateExpense(expense.id, expense.version, payload)
      } else {
        await createExpense(payload)
      }
      onSaved()
    } catch (caught) {
      if (caught instanceof APIError) {
        setFields(caught.fields)
        setError(caught.code === 'version_conflict' ? `${caught.message}. Введённые данные сохранены в форме.` : caught.message)
      } else {
        setError('Не удалось проверить результат. Обновите список перед повторной попыткой.')
      }
    } finally {
      setSaving(false)
    }
  }

  function update<K extends keyof ExpensePayload>(key: K, value: ExpensePayload[K]) {
    setForm((current) => ({ ...current, [key]: value }))
  }

  return (
    <div className="dialog-backdrop" role="presentation" onMouseDown={(event) => event.target === event.currentTarget && !saving && onClose()}>
      <section className="dialog" role="dialog" aria-modal="true" aria-labelledby="expense-dialog-title" onKeyDown={(event) => event.key === 'Escape' && !saving && onClose()}>
        <div className="dialog-heading">
          <div>
            <span className="eyebrow">{expense ? `Трата №${expense.id}` : 'Новая запись'}</span>
            <h2 id="expense-dialog-title">{expense ? 'Редактирование' : 'Добавить расход'}</h2>
          </div>
          <button className="icon-button" type="button" onClick={onClose} disabled={saving} aria-label="Закрыть">×</button>
        </div>

        <form onSubmit={submit} className="expense-form">
          <label>
            <span>Владелец</span>
            <input
              required
              inputMode="numeric"
              list="known-users"
              placeholder="Выберите или введите Telegram ID"
              value={form.user_id}
              onChange={(event) => update('user_id', event.target.value)}
              aria-invalid={Boolean(fields.user_id)}
              autoFocus={!form.user_id}
            />
            <datalist id="known-users">
              {users.map((user) => <option key={user.id} value={user.id}>{user.display_name ?? `Telegram ${user.id}`}</option>)}
            </datalist>
            {fields.user_id ? <small className="field-error">{fields.user_id}</small> : null}
          </label>

          <div className="form-grid">
            <label>
              <span>Сумма, ₽</span>
              <input required inputMode="decimal" placeholder="350,00" value={form.amount} onChange={(event) => update('amount', event.target.value)} aria-invalid={Boolean(fields.amount)} autoFocus={Boolean(form.user_id)} />
              {fields.amount ? <small className="field-error">{fields.amount}</small> : null}
            </label>
            <label>
              <span>Дата</span>
              <input required type="date" max={meta.today} value={form.spent_at} onChange={(event) => update('spent_at', event.target.value)} aria-invalid={Boolean(fields.spent_at)} />
              {fields.spent_at ? <small className="field-error">{fields.spent_at}</small> : null}
            </label>
          </div>

          <label>
            <span>Категория</span>
            <select value={form.category} onChange={(event) => update('category', event.target.value as Category)} aria-invalid={Boolean(fields.category)}>
              {meta.categories.map((category) => <option key={category.code} value={category.code}>{category.label}</option>)}
            </select>
            {fields.category ? <small className="field-error">{fields.category}</small> : null}
          </label>

          <label>
            <span>Комментарий</span>
            <textarea maxLength={500} rows={3} placeholder="Например, кофе с собой" value={form.comment ?? ''} onChange={(event) => update('comment', event.target.value)} aria-invalid={Boolean(fields.comment)} />
            {fields.comment ? <small className="field-error">{fields.comment}</small> : null}
          </label>

          {error ? <div className="notice error" role="alert">{error}</div> : null}

          <div className="dialog-actions">
            <button className="button secondary" type="button" onClick={onClose} disabled={saving}>Отмена</button>
            <button className="button primary" type="submit" disabled={saving}>{saving ? 'Сохраняем…' : 'Сохранить'}</button>
          </div>
        </form>
      </section>
    </div>
  )
}

function normalizeAmount(value: string): string {
  return value.trim().replace(/\s/g, '').replace(',', '.')
}
