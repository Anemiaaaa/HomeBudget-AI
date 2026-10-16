import { useEffect, useMemo, useState } from 'react'
import { APIError, deleteExpense, getExpenses, getMeta, getSummary, getUsers } from './api'
import { ExpenseDialog } from './ExpenseDialog'
import type { Expense, ExpenseList, Filters, Meta, Summary, User } from './types'

const PAGE_SIZE = 20
const EMPTY_FILTERS: Filters = { user_id: '', category: '', date_from: '', date_to: '', query: '' }
const EMPTY_LIST: ExpenseList = { items: [], total_count: 0, limit: PAGE_SIZE, offset: 0 }
const EMPTY_SUMMARY: Summary = { total_amount: '0.00', count: 0, by_category: [] }

const rubles = new Intl.NumberFormat('ru-RU', { style: 'currency', currency: 'RUB' })
const dates = new Intl.DateTimeFormat('ru-RU', { day: '2-digit', month: 'short', year: 'numeric', timeZone: 'UTC' })

export function App() {
  const [meta, setMeta] = useState<Meta | null>(null)
  const [users, setUsers] = useState<User[]>([])
  const [draftFilters, setDraftFilters] = useState<Filters>(EMPTY_FILTERS)
  const [filters, setFilters] = useState<Filters>(EMPTY_FILTERS)
  const [offset, setOffset] = useState(0)
  const [list, setList] = useState<ExpenseList>(EMPTY_LIST)
  const [summary, setSummary] = useState<Summary>(EMPTY_SUMMARY)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [editing, setEditing] = useState<Expense | null>(null)
  const [dialogOpen, setDialogOpen] = useState(false)
  const [refreshToken, setRefreshToken] = useState(0)

  useEffect(() => {
    const controller = new AbortController()
    getMeta(controller.signal).then(setMeta).catch((caught) => {
      if (!controller.signal.aborted) setError(errorMessage(caught))
    })
    return () => controller.abort()
  }, [])

  useEffect(() => {
    const controller = new AbortController()
    getUsers(controller.signal).then(setUsers).catch((caught) => {
      if (!controller.signal.aborted) setError(errorMessage(caught))
    })
    return () => controller.abort()
  }, [refreshToken])

  useEffect(() => {
    const controller = new AbortController()
    setLoading(true)
    setError('')
    Promise.all([
      getExpenses(filters, PAGE_SIZE, offset, controller.signal),
      getSummary(filters, controller.signal),
    ]).then(([nextList, nextSummary]) => {
      setList(nextList)
      setSummary(nextSummary)
    }).catch((caught) => {
      if (!controller.signal.aborted) setError(errorMessage(caught))
    }).finally(() => {
      if (!controller.signal.aborted) setLoading(false)
    })
    return () => controller.abort()
  }, [filters, offset, refreshToken])

  const categoryLabels = useMemo(() => new Map(meta?.categories.map((item) => [item.code, item.label]) ?? []), [meta])
  const userLabels = useMemo(() => new Map(users.map((user) => [user.id, user.display_name ?? `Telegram ${user.id}`])), [users])
  const page = Math.floor(offset / PAGE_SIZE) + 1
  const pageCount = Math.max(1, Math.ceil(list.total_count / PAGE_SIZE))

  function applyFilters() {
    setOffset(0)
    setFilters({ ...draftFilters })
  }

  function resetFilters() {
    setDraftFilters(EMPTY_FILTERS)
    setFilters(EMPTY_FILTERS)
    setOffset(0)
  }

  function openCreate() {
    setEditing(null)
    setDialogOpen(true)
  }

  function openEdit(item: Expense) {
    setEditing(item)
    setDialogOpen(true)
  }

  function saved() {
    setDialogOpen(false)
    setEditing(null)
    setRefreshToken((current) => current + 1)
  }

  async function remove(item: Expense) {
    if (!window.confirm(`Удалить ${item.comment || categoryLabels.get(item.category) || 'трату'} на ${formatMoney(item.amount)} от ${formatDate(item.spent_at)}?`)) return
    setError('')
    try {
      await deleteExpense(item.id, item.version)
      const isOnlyItemOnPage = list.items.length === 1 && offset > 0
      if (isOnlyItemOnPage) setOffset((current) => Math.max(0, current - PAGE_SIZE))
      else setRefreshToken((current) => current + 1)
    } catch (caught) {
      setError(errorMessage(caught))
    }
  }

  if (!meta) {
    return <main className="shell"><div className="loading-card">{error || 'Загружаем панель…'}</div></main>
  }

  return (
    <main className="shell">
      <header className="page-header">
        <div>
          <span className="eyebrow">Семейный бюджет · {meta.timezone}</span>
          <h1>Расходы семьи</h1>
          <p>Все траты в одном месте — с понятными фильтрами и быстрым редактированием.</p>
        </div>
        <button className="button primary" type="button" onClick={openCreate}>+ Добавить расход</button>
      </header>

      <section className="summary-grid" aria-label="Итоги по текущему фильтру">
        <article className="summary-card accent"><span>Всего расходов</span><strong>{formatMoney(summary.total_amount)}</strong><small>{summary.count} {plural(summary.count, 'операция', 'операции', 'операций')}</small></article>
        {summary.by_category.slice(0, 3).map((item) => (
          <article className="summary-card" key={item.category}><span>{categoryLabels.get(item.category) ?? item.category}</span><strong>{formatMoney(item.amount)}</strong><small>{item.count} {plural(item.count, 'трата', 'траты', 'трат')}</small></article>
        ))}
      </section>

      <section className="panel filters-panel">
        <div className="panel-heading"><div><span className="eyebrow">Срез данных</span><h2>Фильтры</h2></div></div>
        <div className="filters">
          <label className="search-filter"><span>Поиск по комментарию</span><input type="search" maxLength={200} placeholder="Например, кофе" value={draftFilters.query} onChange={(event) => setDraftFilters((current) => ({ ...current, query: event.target.value }))} /></label>
          <label><span>Член семьи</span><select value={draftFilters.user_id} onChange={(event) => setDraftFilters((current) => ({ ...current, user_id: event.target.value }))}><option value="">Все</option>{users.map((user) => <option key={user.id} value={user.id}>{userLabels.get(user.id)}</option>)}</select></label>
          <label><span>Категория</span><select value={draftFilters.category} onChange={(event) => setDraftFilters((current) => ({ ...current, category: event.target.value }))}><option value="">Все</option>{meta.categories.map((category) => <option key={category.code} value={category.code}>{category.label}</option>)}</select></label>
          <label><span>С даты</span><input type="date" max={meta.today} value={draftFilters.date_from} onChange={(event) => setDraftFilters((current) => ({ ...current, date_from: event.target.value }))} /></label>
          <label><span>По дату</span><input type="date" max={meta.today} value={draftFilters.date_to} onChange={(event) => setDraftFilters((current) => ({ ...current, date_to: event.target.value }))} /></label>
          <div className="filter-actions"><button className="button primary" type="button" onClick={applyFilters}>Применить</button><button className="button secondary" type="button" onClick={resetFilters}>Сбросить</button></div>
        </div>
      </section>

      {error ? <div className="notice error" role="alert">{error}</div> : null}

      <section className="panel expenses-panel">
        <div className="panel-heading"><div><span className="eyebrow">{list.total_count} записей</span><h2>История расходов</h2></div>{loading ? <span className="loading-dot">Обновляем…</span> : null}</div>
        {list.items.length === 0 && !loading ? (
          <div className="empty-state"><div className="empty-icon">₽</div><h3>Расходов не найдено</h3><p>Измените фильтры или добавьте первую запись.</p><button className="button primary" type="button" onClick={openCreate}>Добавить расход</button></div>
        ) : (
          <div className="table-wrap">
            <table>
              <thead><tr><th>Дата</th><th>Владелец</th><th>Категория</th><th>Комментарий</th><th className="number">Сумма</th><th><span className="visually-hidden">Действия</span></th></tr></thead>
              <tbody>{list.items.map((item) => (
                <tr key={item.id}>
                  <td><strong>{formatDate(item.spent_at)}</strong><small>№{item.id}</small></td>
                  <td>{userLabels.get(item.user_id) ?? `Telegram ${item.user_id}`}</td>
                  <td><span className={`category category-${item.category}`}>{categoryLabels.get(item.category) ?? item.category}</span></td>
                  <td className="comment">{item.comment || '—'}</td>
                  <td className="number amount">{formatMoney(item.amount)}</td>
                  <td><div className="row-actions"><button type="button" onClick={() => openEdit(item)}>Изменить</button><button className="danger-link" type="button" onClick={() => void remove(item)}>Удалить</button></div></td>
                </tr>
              ))}</tbody>
            </table>
          </div>
        )}
        <footer className="pagination"><span>Страница {page} из {pageCount}</span><div><button className="button secondary compact" type="button" disabled={offset === 0 || loading} onClick={() => setOffset((current) => Math.max(0, current - PAGE_SIZE))}>Назад</button><button className="button secondary compact" type="button" disabled={offset + PAGE_SIZE >= list.total_count || loading} onClick={() => setOffset((current) => current + PAGE_SIZE)}>Дальше</button></div></footer>
      </section>

      {dialogOpen ? <ExpenseDialog key={editing?.id ?? 'new'} expense={editing} meta={meta} users={users} onClose={() => setDialogOpen(false)} onSaved={saved} /> : null}
    </main>
  )
}

function formatMoney(value: string): string { return rubles.format(Number(value)) }
function formatDate(value: string): string { return dates.format(new Date(`${value}T00:00:00Z`)) }
function errorMessage(error: unknown): string { return error instanceof APIError ? error.message : 'Сервис временно недоступен. Попробуйте ещё раз.' }
function plural(value: number, one: string, few: string, many: string): string {
  const mod100 = value % 100
  const mod10 = value % 10
  if (mod100 >= 11 && mod100 <= 19) return many
  if (mod10 === 1) return one
  if (mod10 >= 2 && mod10 <= 4) return few
  return many
}
