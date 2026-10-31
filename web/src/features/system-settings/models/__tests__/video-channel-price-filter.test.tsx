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
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { ModelRatioVisualEditor } from '../model-ratio-visual-editor'

let client: QueryClient | undefined

afterEach(() => {
  client?.clear()
  localStorage.clear()
  vi.restoreAllMocks()
})

const channels = [
  {
    id: 282,
    name: 'Yeepay Video',
    status: 1,
    group: 'Video',
    models: 'Xseadanco2.0,TTP-grok',
    model_mapping: null,
  },
  {
    id: 17,
    name: 'video-jimeng',
    status: 1,
    group: 'Video',
    models: 'seedance-720p,unpriced-model',
    model_mapping: JSON.stringify({ 'seedance-720p': 'jimeng-upstream' }),
  },
  {
    id: 276,
    name: 'video-junli',
    status: 2,
    group: 'Video',
    models: 'grok-video',
    model_mapping: null,
  },
]

async function renderEditor(filterMode: 'all' | 'unset' = 'all') {
  const get = vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/pricing') {
      return { data: { success: true, data: [], vendors: [] } }
    }
    if (url === '/api/channel') {
      return {
        data: {
          success: true,
          data: {
            items: channels,
            total: channels.length,
            page: 1,
            page_size: 100,
          },
        },
      }
    }
    return { data: { success: true, data: {} } }
  })
  const expr = 'tier("base", u("videos") * 0.1)'
  const names = [
    'Xseadanco2.0',
    'TTP-grok',
    'jimeng-upstream',
    'grok-video',
    'gpt-4.1-mini',
  ]
  const billingMode = JSON.stringify(
    Object.fromEntries(names.slice(0, 4).map((name) => [name, 'tiered_expr']))
  )
  const billingExpr = JSON.stringify(
    Object.fromEntries(names.slice(0, 4).map((name) => [name, expr]))
  )
  const modelRatio = JSON.stringify({ 'gpt-4.1-mini': 0.2 })
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={client}>
      <ModelRatioVisualEditor
        savedModelPrice='{}'
        savedModelRatio={modelRatio}
        savedCacheRatio='{}'
        savedCreateCacheRatio='{}'
        savedCompletionRatio='{}'
        savedImageRatio='{}'
        savedAudioRatio='{}'
        savedAudioCompletionRatio='{}'
        savedBillingMode={billingMode}
        savedBillingExpr={billingExpr}
        modelPrice='{}'
        modelRatio={modelRatio}
        cacheRatio='{}'
        createCacheRatio='{}'
        completionRatio='{}'
        imageRatio='{}'
        audioRatio='{}'
        audioCompletionRatio='{}'
        billingMode={billingMode}
        billingExpr={billingExpr}
        candidateModelNames={
          filterMode === 'unset' ? ['gpt-4.1-mini'] : undefined
        }
        filterMode={filterMode}
        onChange={vi.fn()}
        onSave={vi.fn()}
        isSaving={false}
      />
    </QueryClientProvider>
  )
  await waitFor(() => expect(client?.isFetching()).toBe(0))
  return get
}

const rowNames = () =>
  screen
    .getAllByRole('row')
    .map((row) => row.textContent ?? '')
    .filter(Boolean)

it('requests only Video group channels without channel keys in the query', async () => {
  const get = await renderEditor()
  expect(get).toHaveBeenCalledWith('/api/channel', {
    params: { group: 'Video', page_size: 100, id_sort: true },
  })
})

it('shows every model until a channel is selected', async () => {
  await renderEditor()
  await screen.findByRole('row', { name: /gpt-4\.1-mini/ })
  for (const name of [
    'Xseadanco2.0',
    'TTP-grok',
    'jimeng-upstream',
    'grok-video',
  ]) {
    expect(
      screen.getByRole('row', { name: new RegExp(name) })
    ).toBeInTheDocument()
  }
})

it('narrows the table to the selected video channel', async () => {
  await renderEditor()
  await userEvent.click(
    screen.getByRole('combobox', { name: 'Filter by video channel' })
  )
  await userEvent.click(
    await screen.findByRole('option', { name: 'Yeepay Video' })
  )

  await waitFor(() =>
    expect(screen.queryByRole('row', { name: /gpt-4\.1-mini/ })).toBeNull()
  )
  const rows = rowNames().join('\n')
  expect(rows).toMatch(/Xseadanco2\.0/)
  expect(rows).toMatch(/TTP-grok/)
  expect(rows).not.toMatch(/jimeng-upstream|grok-video/)
})

it('lists mapping targets, unpriced channel models and the alias billing hint', async () => {
  await renderEditor()
  await userEvent.click(
    screen.getByRole('combobox', { name: 'Filter by video channel' })
  )
  await userEvent.click(
    await screen.findByRole('option', { name: 'video-jimeng' })
  )

  expect(
    await screen.findByRole('row', { name: /jimeng-upstream/ })
  ).toBeInTheDocument()
  // Channel models with no saved price still appear so they can be priced.
  expect(
    screen.getByRole('row', { name: /unpriced-model/ })
  ).toBeInTheDocument()
  expect(screen.getByRole('row', { name: /seedance-720p/ })).toBeInTheDocument()
  expect(
    screen.getByText(/seedance-720p → jimeng-upstream/)
  ).toBeInTheDocument()
  expect(screen.queryByRole('row', { name: /Xseadanco2\.0/ })).toBeNull()
})

it('labels disabled channels and restores the full list on "All video channels"', async () => {
  await renderEditor()
  await userEvent.click(
    screen.getByRole('combobox', { name: 'Filter by video channel' })
  )
  await userEvent.click(
    await screen.findByRole('option', { name: 'video-junli (disabled)' })
  )
  await waitFor(() =>
    expect(screen.queryByRole('row', { name: /Xseadanco2\.0/ })).toBeNull()
  )
  expect(screen.getByRole('row', { name: /grok-video/ })).toBeInTheDocument()

  await userEvent.click(
    screen.getByRole('combobox', { name: 'Filter by video channel' })
  )
  await userEvent.click(
    await screen.findByRole('option', { name: 'All video channels' })
  )
  expect(
    await screen.findByRole('row', { name: /gpt-4\.1-mini/ })
  ).toBeInTheDocument()
})

it('does not offer the channel filter or load channels on the unset view', async () => {
  const get = await renderEditor('unset')
  expect(
    screen.queryByRole('combobox', { name: 'Filter by video channel' })
  ).toBeNull()
  expect(get).not.toHaveBeenCalledWith('/api/channel', expect.anything())
})
