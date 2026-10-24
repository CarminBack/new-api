/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import {
  flexRender,
  getCoreRowModel,
  useReactTable,
} from '@tanstack/react-table'
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  within,
  waitFor,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createInstance } from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { Toaster, toast } from 'sonner'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import zh from '@/i18n/locales/zh.json'
import { api } from '@/lib/api'
import {
  DEFAULT_CURRENCY_CONFIG,
  useSystemConfigStore,
} from '@/stores/system-config-store'

import { getApiKeys, searchApiKeys } from '../../api'
import { apiKeySchema, type ApiKey } from '../../types'
import { ApiKeyQuotaCell } from '../api-key-quota-cell'
import { useApiKeysColumns } from '../api-keys-columns'
import { ApiKeysProvider } from '../api-keys-provider'
import { ApiKeysTable } from '../api-keys-table'

const now = 1_700_000_000_000
const key = apiKeySchema.parse({
  id: 7,
  name: 'production',
  key: 'demo********1234',
  status: 1,
  remain_quota: 40_000_000,
  used_quota: 60_000_000,
  unlimited_quota: false,
  expired_time: -1,
  created_time: 0,
  accessed_time: 0,
  group: 'default',
  model_limits_enabled: false,
})
const i18n = createInstance()
await i18n.init({
  lng: 'en',
  resources: { en: { translation: {} } },
  initAsync: false,
})
const clients: QueryClient[] = []

function QuotaTable(props: { apiKey: ApiKey }) {
  const columns = useApiKeysColumns(now).filter(
    (column) => column.id === 'quota'
  )
  const table = useReactTable({
    columns,
    data: [props.apiKey],
    getCoreRowModel: getCoreRowModel(),
  })
  return (
    <table>
      <thead>
        {table.getHeaderGroups().map((group) => (
          <tr key={group.id}>
            {group.headers.map((header) => (
              <th key={header.id}>
                {flexRender(
                  header.column.columnDef.header,
                  header.getContext()
                )}
              </th>
            ))}
          </tr>
        ))}
      </thead>
      <tbody>
        {table.getRowModel().rows.map((row) => (
          <tr key={row.id}>
            {row.getVisibleCells().map((cell) => (
              <td key={cell.id}>
                {flexRender(cell.column.columnDef.cell, cell.getContext())}
              </td>
            ))}
          </tr>
        ))}
      </tbody>
    </table>
  )
}

function renderQuota(apiKey: ApiKey = key) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, enabled: false } },
  })
  clients.push(client)
  return render(
    <I18nextProvider i18n={i18n}>
      <QueryClientProvider client={client}>
        <QuotaTable apiKey={apiKey} />
      </QueryClientProvider>
    </I18nextProvider>
  )
}

beforeEach(() => {
  vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
  localStorage.clear()
  useSystemConfigStore
    .getState()
    .setConfig({ currency: { ...DEFAULT_CURRENCY_CONFIG } })
  vi.spyOn(api, 'get').mockResolvedValue({ data: { success: true, data: {} } })
})
afterEach(() => {
  cleanup()
  toast.dismiss()
  localStorage.clear()
  clients.splice(0).forEach((client) => client.clear())
  useSystemConfigStore
    .getState()
    .setConfig({ currency: { ...DEFAULT_CURRENCY_CONFIG } })
})

it('shows desktop remaining and used amounts side by side without labels, with the currency only in the header', () => {
  renderQuota()
  expect(
    screen.getByRole('columnheader', { name: 'Quota ($)' })
  ).toBeInTheDocument()
  const trigger = screen.getByRole('button', {
    name: /Remaining 80; Remaining percentage 40%; Used amount 120/,
  })
  expect(trigger).toHaveTextContent('80120')
  expect(trigger).not.toHaveTextContent(/Remaining|Used amount/)
  expect(
    trigger.querySelector('[data-slot="api-key-quota-values"]')
  ).toHaveClass('grid-cols-2')
  expect(within(trigger).getByText('80')).toHaveClass('text-left')
  expect(within(trigger).getByText('120')).toHaveClass('text-right')
  expect(trigger.parentElement).toHaveClass('max-w-45')
  expect(trigger).not.toHaveTextContent('$')
  expect(trigger.querySelector('svg')).toBeNull()
  expect(screen.getByRole('progressbar')).toHaveAttribute('aria-valuenow', '40')
})

it.each([
  ['unused', 500000, 0, 100, 'text-emerald-500'],
  ['low remaining', 150000, 350000, 30, 'text-amber-500'],
  ['critical remaining', 50000, 450000, 10, 'text-rose-500'],
  ['exhausted', 0, 500000, 0, null],
  ['overdrawn', -50000, 500000, 0, null],
  ['zero total', 0, 0, 0, null],
  ['negative total', -500000, 100000, 0, null],
])(
  'renders the %s progress without invalid values or hiding negative balances',
  (_label, remaining, used, percentage, color) => {
    renderQuota({ ...key, remain_quota: remaining, used_quota: used })
    const button = screen.getByRole('button')
    const progress = screen.getByRole('progressbar')
    expect(progress).toHaveAttribute('aria-valuenow', String(percentage))
    if (color) expect(progress).toHaveClass(color)
    if (remaining < 0) {
      expect(
        within(button).getByText(remaining === -500000 ? '-1' : '-0.1')
      ).toHaveClass('text-destructive')
    }
  }
)

it('shows unlimited with cumulative usage and explains it on demand', async () => {
  renderQuota({ ...key, unlimited_quota: true })
  const button = screen.getByRole('button', { name: /Unlimited/ })
  expect(button).toHaveTextContent('Unlimited')
  expect(button).toHaveTextContent('Unlimited120')
  expect(button).not.toHaveTextContent(/Remaining|Used amount/)
  expect(within(button).getByText('Unlimited')).toHaveClass('text-left')
  expect(screen.queryByRole('progressbar')).not.toBeInTheDocument()
  await userEvent.click(button)
  const detail = await screen.findByRole('dialog')
  expect(within(detail).getByText('120')).toBeInTheDocument()
  expect(detail).toHaveTextContent(
    'This API key has no quota limit. Requests still require available wallet or subscription quota.'
  )
})

it('keeps small custom-currency amounts exact and shows full values in the detail', async () => {
  useSystemConfigStore.getState().setConfig({
    currency: {
      ...DEFAULT_CURRENCY_CONFIG,
      quotaDisplayType: 'CUSTOM',
      customCurrencySymbol: '🐱',
    },
  })
  renderQuota({ ...key, remain_quota: 1900, used_quota: 1100 })
  expect(
    screen.getByRole('columnheader', { name: 'Quota (🐱)' })
  ).toBeInTheDocument()
  const button = screen.getByRole('button')
  expect(button).toHaveTextContent('0.0038')
  expect(button).not.toHaveTextContent('🐱')
  await userEvent.click(button)
  const detail = await screen.findByRole('dialog')
  expect(within(detail).getByText('0.0022')).toBeInTheDocument()
  expect(within(detail).getByText('0.006')).toBeInTheDocument()
})

it.each([
  ['disabled', { status: 2 }],
  ['expired status', { status: 3 }],
  ['exhausted status', { status: 4 }],
  ['expired timestamp', { expired_time: now / 1000 - 1 }],
])('renders the %s progress bar in a neutral color', (_label, overrides) => {
  renderQuota({ ...key, ...overrides })
  expect(screen.getByRole('progressbar')).toHaveClass(
    'text-muted-foreground/60'
  )
})

it('recalculates the progress when remaining quota is edited', () => {
  const { rerender } = render(
    <I18nextProvider i18n={i18n}>
      <ApiKeyQuotaCell apiKey={key} now={now} />
    </I18nextProvider>
  )
  expect(screen.getByRole('progressbar')).toHaveAttribute('aria-valuenow', '40')
  rerender(
    <I18nextProvider i18n={i18n}>
      <ApiKeyQuotaCell
        apiKey={{ ...key, remain_quota: 90_000_000 }}
        now={now}
      />
    </I18nextProvider>
  )
  expect(screen.getByRole('progressbar')).toHaveAttribute('aria-valuenow', '60')
  expect(screen.getByText('180')).toBeInTheDocument()
})

it('opens details with the keyboard and restores focus when Escape closes them', async () => {
  renderQuota()
  const user = userEvent.setup()
  const button = screen.getByRole('button')
  act(() => button.focus())
  await user.keyboard('{Enter}')
  const detail = await screen.findByRole('dialog')
  expect(within(detail).getByText('80')).toBeInTheDocument()
  expect(within(detail).getByText('120')).toBeInTheDocument()
  expect(within(detail).getByText('200')).toBeInTheDocument()
  expect(within(detail).getByText('Remaining percentage')).toBeInTheDocument()
  expect(within(detail).getByText('40%')).toBeInTheDocument()
  await user.keyboard('{Escape}')
  await waitFor(() =>
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  )
  expect(button).toHaveFocus()
})

it('keeps a long amount within its column while showing the full amount in details', async () => {
  renderQuota({ ...key, remain_quota: 123456789000000, used_quota: 0 })
  const button = screen.getByRole('button')
  expect(button).toHaveClass('w-full', 'min-w-0')
  expect(within(button).getByText('246,913,578')).toHaveClass('truncate')
  await userEvent.click(button)
  expect(
    within(await screen.findByRole('dialog')).getAllByText('246,913,578')
  ).toHaveLength(2)
})

function KeysPage() {
  return (
    <ApiKeysProvider>
      <ApiKeysTable />
      <Toaster />
    </ApiKeysProvider>
  )
}

async function renderKeysPage(
  status = 1,
  overrides: Partial<ApiKey> = {},
  initialPath = '/keys/',
  total = 1
) {
  let currentKey = { ...key, status, ...overrides }
  vi.mocked(api.get).mockImplementation(async (url) => {
    if (url.startsWith('/api/token/')) {
      return {
        data: { success: true, data: { items: [currentKey], total } },
      }
    }
    return { data: { success: true, data: { default: { ratio: 1 } } } }
  })
  const post = vi.spyOn(api, 'post').mockResolvedValue({
    data: { success: true, data: { key: 'fake-key-for-test-only' } },
  })
  const put = vi.spyOn(api, 'put').mockImplementation(async (_url, data) => {
    const update = data as { id: number; status: number }
    currentKey = { ...currentKey, status: update.status }
    return { data: { success: true, data: currentKey } }
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  client.setQueryData(['status'], {})
  clients.push(client)
  const root = createRootRoute()
  const auth = createRoute({ getParentRoute: () => root, id: '_authenticated' })
  const keysRoute = createRoute({
    getParentRoute: () => auth,
    path: 'keys/',
    component: KeysPage,
  })
  const router = createRouter({
    routeTree: root.addChildren([auth.addChildren([keysRoute])]),
    history: createMemoryHistory({ initialEntries: [initialPath] }),
  })
  await router.load()
  render(
    <I18nextProvider i18n={i18n}>
      <QueryClientProvider client={client}>
        <RouterProvider router={router} />
      </QueryClientProvider>
    </I18nextProvider>
  )
  await screen.findByText(currentKey.name)
  return { post, put }
}

it('combines creation and last use while keeping expiry, models and IP restrictions separate', async () => {
  await renderKeysPage()
  for (const name of ['Name', 'API Key', 'Group', 'Models', 'IP Restriction']) {
    expect(screen.getByRole('columnheader', { name })).toBeInTheDocument()
  }
  expect(screen.getByRole('columnheader', { name: 'Time' })).toBeInTheDocument()
  expect(
    screen.getByRole('columnheader', { name: 'Expires' })
  ).toBeInTheDocument()
  const timeCell = screen.getByRole('cell', { name: /Created.*Last Used/ })
  expect(within(timeCell).getByText('Last Used')).toBeInTheDocument()
  const quotaHeader = screen.getByRole('columnheader', { name: 'Quota ($)' })
  const quotaTrigger = screen.getByRole('button', {
    name: /Remaining 80; Remaining percentage 40%; Used amount 120/,
  })
  expect(quotaHeader).not.toHaveClass('pr-8')
  expect(quotaTrigger.closest('td')).not.toHaveClass('pr-8')
})

it('restores dates hidden by the old default and preserves unrelated column preferences', async () => {
  localStorage.setItem(
    'api-keys:column-visibility',
    JSON.stringify({
      created_time: false,
      accessed_time: false,
      expired_time: false,
      model_limits: false,
    })
  )
  await renderKeysPage()
  expect(screen.getByRole('columnheader', { name: 'Time' })).toBeInTheDocument()
  expect(
    screen.getByRole('columnheader', { name: 'Expires' })
  ).toBeInTheDocument()
  expect(
    screen.queryByRole('columnheader', { name: 'Models' })
  ).not.toBeInTheDocument()
})

it.each([
  [1, 'Disable', 2, 'Disabled'],
  [2, 'Enable', 1, 'Enabled'],
])(
  'keeps status %s toggling at its original row button without fetching a full key',
  async (status, action, nextStatus, nextLabel) => {
    const { post, put } = await renderKeysPage(status)
    const user = userEvent.setup()
    const button = screen.getByRole('button', { name: action })
    act(() => button.focus())
    await user.keyboard('{Enter}')
    await waitFor(() =>
      expect(put).toHaveBeenCalledWith('/api/token/?status_only=true', {
        id: 7,
        status: nextStatus,
      })
    )
    await screen.findByText(nextLabel)
    expect(post).not.toHaveBeenCalled()
  }
)

it('keeps expired status when the server refuses reactivation', async () => {
  const { put, post } = await renderKeysPage(3)
  put.mockResolvedValue({ data: { success: false, message: 'Token expired' } })
  await userEvent.click(screen.getByRole('button', { name: 'Enable' }))
  await screen.findByText('Token expired')
  expect(screen.getByText('Expired')).toBeInTheDocument()
  expect(screen.queryByText('Enabled')).not.toBeInTheDocument()
  expect(post).not.toHaveBeenCalled()
})

it.each([true, false])(
  'fetches a full key only on explicit copy and honors permission success=%s',
  async (success) => {
    const user = userEvent.setup()
    const { post } = await renderKeysPage()
    post.mockResolvedValue(
      success
        ? { data: { success: true, data: { key: 'fake-key-for-test-only' } } }
        : { data: { success: false, message: 'Verification required' } }
    )
    const copy = vi.spyOn(navigator.clipboard, 'writeText').mockResolvedValue()
    await user.click(screen.getByRole('button', { name: 'Open menu' }))
    expect(post).not.toHaveBeenCalled()
    await user.click(screen.getByRole('menuitem', { name: 'Copy Key' }))
    await waitFor(() => expect(post).toHaveBeenCalledWith('/api/token/7/key'))
    if (success) {
      await waitFor(() =>
        expect(copy).toHaveBeenCalledWith('sk-fake-key-for-test-only')
      )
    } else {
      await screen.findByText('Verification required')
      expect(copy).not.toHaveBeenCalled()
    }
  }
)

it('keeps full mobile information without group or quota section headings', async () => {
  const matchMedia = window.matchMedia
  vi.spyOn(window, 'matchMedia').mockImplementation((query) => ({
    ...matchMedia(query),
    matches: query.includes('max-width'),
  }))
  i18n.addResourceBundle('zh', 'translation', zh.translation)
  await i18n.changeLanguage('zh')
  try {
    await renderKeysPage()
    expect(screen.queryByRole('table')).not.toBeInTheDocument()
    expect(screen.queryByText('额度 ($)')).not.toBeInTheDocument()
    expect(screen.getByText('($)')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /剩余 80;/ })).toBeInTheDocument()
    expect(screen.getByText('80')).toBeInTheDocument()
    expect(screen.getByText('120')).toBeInTheDocument()
    expect(screen.getByText(zh.translation['Created'])).toBeInTheDocument()
    expect(screen.getByText(zh.translation['Last Used'])).toBeInTheDocument()
    expect(screen.getByText(zh.translation['Expires'])).toBeInTheDocument()
    expect(
      screen.queryByText(zh.translation['Group'], { exact: true })
    ).not.toBeInTheDocument()
    expect(screen.getByText('default')).toBeInTheDocument()
    expect(screen.getByText('1x')).toBeInTheDocument()
    expect(screen.getByText(zh.translation['Models'])).toBeInTheDocument()
    expect(
      screen.getByText(zh.translation['IP Restriction'])
    ).toBeInTheDocument()
  } finally {
    await i18n.changeLanguage('en')
  }
})

it('keeps mobile quota readable and opens complete model and IP restrictions by tapping', async () => {
  const matchMedia = window.matchMedia
  vi.spyOn(window, 'matchMedia').mockImplementation((query) => ({
    ...matchMedia(query),
    matches: query.includes('max-width'),
  }))
  await renderKeysPage(1, {
    name: 'production-with-a-long-key-name',
    used_quota: 2245080000,
    unlimited_quota: true,
    model_limits_enabled: true,
    model_limits: 'model-alpha,model-beta-with-a-long-name',
    allow_ips: '192.0.2.1\n2001:db8::1',
  })
  const quota = screen.getByRole('button', {
    name: /Unlimited; Used amount 4,490.16/,
  })
  expect(quota).toHaveTextContent('Remaining($)UnlimitedUsed amount4,490.16')
  expect(quota.parentElement).toHaveClass('w-full')
  expect(quota.parentElement).not.toHaveClass('max-w-45')
  expect(quota.querySelector('[data-slot="api-key-quota-values"]')).toHaveClass(
    'grid-cols-[auto_minmax(0,1fr)]'
  )
  expect(within(quota).getByText('Unlimited')).toHaveClass(
    'text-right',
    'text-sm',
    'font-normal'
  )
  expect(within(quota).getByText('4,490.16')).toHaveClass(
    'tabular-nums',
    'text-sm',
    'font-normal',
    'text-right'
  )
  await userEvent.click(screen.getByRole('button', { name: /Models: 2 model/ }))
  let details = await screen.findByRole('dialog')
  expect(within(details).getByText('model-alpha')).toBeVisible()
  expect(within(details).getByText('model-beta-with-a-long-name')).toBeVisible()
  await userEvent.keyboard('{Escape}')
  await userEvent.click(
    screen.getByRole('button', { name: /IP Restriction: 2 IP/ })
  )
  details = await screen.findByRole('dialog')
  expect(within(details).getByText('192.0.2.1')).toBeVisible()
  expect(within(details).getByText('2001:db8::1')).toBeVisible()
})

function enterUsageDates(start: string, end: string) {
  fireEvent.change(screen.getByLabelText('Start date'), {
    target: { value: start },
  })
  fireEvent.change(screen.getByLabelText('End date'), {
    target: { value: end },
  })
}

it('validates both calendar dates and includes the full end day without replacing cumulative quota', async () => {
  await renderKeysPage(1, { period_used_quota: 0 })
  const query = screen.getByRole('button', { name: 'Query usage' })
  expect(query.closest('form')).toHaveClass('grid', 'grid-cols-2', 'sm:flex')
  expect(query).toBeDisabled()
  expect(
    screen.queryByRole('columnheader', { name: 'Period used quota' })
  ).not.toBeInTheDocument()
  enterUsageDates('2026-03-08', '')
  expect(screen.getByRole('alert')).toHaveTextContent('Select both')
  expect(screen.getByLabelText('End date')).toHaveAttribute(
    'aria-invalid',
    'true'
  )
  expect(query).toBeDisabled()
  enterUsageDates('2026-03-09', '2026-03-08')
  expect(screen.getByRole('alert')).toHaveTextContent('The end date must')
  expect(query).toBeDisabled()
  enterUsageDates('2026-03-08', '2026-03-08')
  await userEvent.click(query)
  await screen.findByRole('columnheader', { name: 'Period used quota' })
  const expectedStart = String(new Date(2026, 2, 8).getTime() / 1000)
  const expectedEnd = String(new Date(2026, 2, 9).getTime() / 1000)
  await waitFor(() =>
    expect(
      vi.mocked(api.get).mock.calls.some(([url]) => {
        const parsed = new URL(url, 'http://localhost')
        return (
          parsed.searchParams.get('start_timestamp') === expectedStart &&
          parsed.searchParams.get('end_timestamp') === expectedEnd
        )
      })
    ).toBe(true)
  )
  expect(screen.getByLabelText('Period used quota')).toHaveTextContent('$0')
  expect(
    screen.getByRole('button', {
      name: /Remaining 80; Remaining percentage 40%; Used amount 120/,
    })
  ).toBeInTheDocument()
  expect(screen.getByRole('status')).toHaveTextContent(
    '2026-03-08 to 2026-03-08'
  )
  await userEvent.click(screen.getByRole('button', { name: 'Clear dates' }))
  await waitFor(() =>
    expect(
      screen.queryByRole('columnheader', { name: 'Period used quota' })
    ).not.toBeInTheDocument()
  )
  expect(screen.getByLabelText('Start date')).toHaveValue('')
  expect(screen.getByLabelText('End date')).toHaveValue('')
  expect(screen.getByRole('status')).toHaveTextContent('All-time usage')
})

it('applies a date range from a later page to page one and keeps the range when searching', async () => {
  await renderKeysPage(1, { period_used_quota: 500000 }, '/keys/?page=2', 40)
  enterUsageDates('2026-10-01', '2026-10-24')
  await userEvent.click(screen.getByRole('button', { name: 'Query usage' }))
  await screen.findByRole('columnheader', { name: 'Period used quota' })
  await waitFor(() =>
    expect(
      vi.mocked(api.get).mock.calls.some(([url]) => {
        const params = new URL(url, 'http://localhost').searchParams
        return params.has('start_timestamp') && params.get('p') === '1'
      })
    ).toBe(true)
  )
  await userEvent.type(
    screen.getByPlaceholderText('Filter by name...'),
    'production'
  )
  await waitFor(() =>
    expect(
      vi.mocked(api.get).mock.calls.some(([url]) => {
        const parsed = new URL(url, 'http://localhost')
        return (
          parsed.pathname === '/api/token/search' &&
          parsed.searchParams.get('keyword') === 'production' &&
          parsed.searchParams.has('start_timestamp') &&
          parsed.searchParams.has('end_timestamp')
        )
      })
    ).toBe(true)
  )
})

it.each([false, true])(
  'shows an error instead of zero or an empty list when range query fails (network=%s)',
  async (network) => {
    await renderKeysPage()
    vi.mocked(api.get).mockImplementation(async (url) => {
      if (url.includes('start_timestamp')) {
        if (network) throw new Error('network unavailable')
        return { data: { success: false, message: 'query failed' } }
      }
      return { data: { success: true, data: { items: [key], total: 1 } } }
    })
    enterUsageDates('2026-10-01', '2026-10-24')
    await userEvent.click(screen.getByRole('button', { name: 'Query usage' }))
    await screen.findByText('Failed to load API key usage')
    expect(screen.queryByText('No API Keys Found')).not.toBeInTheDocument()
    expect(screen.queryByLabelText('Period used quota')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Retry' })).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Clear dates' }))
    await screen.findByText(key.name)
  }
)

it.each([0, -500000, undefined])(
  'shows range usage %s separately on mobile and never replaces missing data with zero',
  async (quota) => {
    const matchMedia = window.matchMedia
    vi.spyOn(window, 'matchMedia').mockImplementation((query) => ({
      ...matchMedia(query),
      matches: query.includes('max-width'),
    }))
    await renderKeysPage(1, { period_used_quota: quota })
    enterUsageDates('2026-10-01', '2026-10-24')
    await userEvent.click(screen.getByRole('button', { name: 'Query usage' }))
    const period = await screen.findByLabelText('Period used quota')
    let expected = 'Unavailable'
    if (quota === 0) expected = '$0'
    else if (quota === -500000) expected = '-$1'
    expect(period).toHaveTextContent(expected)
    expect(
      screen.getByRole('button', {
        name: /Remaining 80; Remaining percentage 40%; Used amount 120/,
      })
    ).toBeInTheDocument()
  }
)

it('propagates range bounds including epoch zero through list and paginated search requests', async () => {
  await getApiKeys({ p: 2, size: 20, start_timestamp: 0, end_timestamp: 86400 })
  await searchApiKeys({
    keyword: 'same-name',
    token: 'masked',
    p: 3,
    size: 10,
    start_timestamp: 0,
    end_timestamp: 86400,
  })
  const calls = vi.mocked(api.get).mock.calls
  const list = new URL(calls[0][0], 'http://localhost')
  const search = new URL(calls[1][0], 'http://localhost')
  expect(Object.fromEntries(list.searchParams)).toEqual({
    p: '2',
    size: '20',
    start_timestamp: '0',
    end_timestamp: '86400',
  })
  expect(Object.fromEntries(search.searchParams)).toEqual({
    keyword: 'same-name',
    token: 'masked',
    p: '3',
    size: '10',
    start_timestamp: '0',
    end_timestamp: '86400',
  })
})

it('does not show the previous period usage under new dates while the new request is pending', async () => {
  await renderKeysPage(1, { period_used_quota: 500000 })
  enterUsageDates('2026-10-01', '2026-10-02')
  await userEvent.click(screen.getByRole('button', { name: 'Query usage' }))
  expect(await screen.findByLabelText('Period used quota')).toHaveTextContent(
    '$1'
  )
  let resolveRequest: (value: unknown) => void = () => {}
  const pending = new Promise((resolve) => {
    resolveRequest = resolve
  })
  vi.mocked(api.get).mockImplementation(async (url) => {
    if (url.includes('start_timestamp')) return pending
    return { data: { success: true, data: {} } }
  })
  enterUsageDates('2026-10-03', '2026-10-04')
  await userEvent.click(screen.getByRole('button', { name: 'Query usage' }))
  expect(screen.queryByLabelText('Period used quota')).not.toBeInTheDocument()
  expect(screen.queryByText('No API Keys Found')).not.toBeInTheDocument()
  await act(async () =>
    resolveRequest({
      data: {
        success: true,
        data: { items: [{ ...key, period_used_quota: 1500000 }], total: 1 },
      },
    })
  )
  expect(await screen.findByLabelText('Period used quota')).toHaveTextContent(
    '$3'
  )
  expect(screen.getByRole('status')).toHaveTextContent(
    '2026-10-03 to 2026-10-04'
  )
})
