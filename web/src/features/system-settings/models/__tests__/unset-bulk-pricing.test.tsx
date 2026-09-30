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
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { buildUnsetBulkPricingExpr } from '../model-pricing-snapshots'
import { ModelRatioVisualEditor } from '../model-ratio-visual-editor'

let client: QueryClient | undefined

afterEach(() => {
  client?.clear()
  localStorage.clear()
  vi.restoreAllMocks()
})

it('builds expression prices for token and request billing', () => {
  expect(
    buildUnsetBulkPricingExpr({
      mode: 'token',
      inputPrice: 1.5,
      outputPrice: 6,
      requestPrice: 0,
    })
  ).toBe('tier("base", p * 1.5 + c * 6)')
  expect(
    buildUnsetBulkPricingExpr({
      mode: 'request',
      inputPrice: 0,
      outputPrice: 0,
      requestPrice: 0.02,
    })
  ).toBe('tier("base", fixed(0.02))')
})

it('applies one price to every unset model and saves', async () => {
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/pricing') {
      return { data: { success: true, data: [], vendors: [] } }
    }
    return { data: { success: true, data: {} } }
  })
  const savedRatio = JSON.stringify({ 'priced-model': 1 })
  const onChange = vi.fn()
  const onSave = vi.fn()
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={client}>
      <ModelRatioVisualEditor
        savedModelPrice='{}'
        savedModelRatio={savedRatio}
        savedCacheRatio='{}'
        savedCreateCacheRatio='{}'
        savedCompletionRatio='{}'
        savedImageRatio='{}'
        savedAudioRatio='{}'
        savedAudioCompletionRatio='{}'
        savedBillingMode='{}'
        savedBillingExpr='{}'
        modelPrice='{}'
        modelRatio={savedRatio}
        cacheRatio='{}'
        createCacheRatio='{}'
        completionRatio='{}'
        imageRatio='{}'
        audioRatio='{}'
        audioCompletionRatio='{}'
        billingMode='{}'
        billingExpr='{}'
        candidateModelNames={['new-a', 'new-b', 'priced-model']}
        filterMode='unset'
        onChange={onChange}
        onSave={onSave}
        isSaving={false}
      />
    </QueryClientProvider>
  )

  await screen.findByRole('row', { name: /new-a/ })
  expect(screen.queryByRole('row', { name: /priced-model/ })).toBeNull()

  fireEvent.click(screen.getByRole('button', { name: 'Set prices in bulk' }))
  fireEvent.change(screen.getByLabelText('Input price (USD / 1M tokens)'), {
    target: { value: '2' },
  })
  fireEvent.change(screen.getByLabelText('Output price (USD / 1M tokens)'), {
    target: { value: '8' },
  })
  fireEvent.click(screen.getByRole('button', { name: 'Apply and save' }))

  await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1))
  const lastValue = (key: string) =>
    JSON.parse(
      onChange.mock.calls.findLast(([field]) => field === key)?.[1] ?? '{}'
    )
  const expr = 'tier("base", p * 2 + c * 8)'
  expect(lastValue('billing_setting.billing_expr')).toEqual({
    'new-a': expr,
    'new-b': expr,
  })
  expect(lastValue('billing_setting.billing_mode')).toEqual({
    'new-a': 'tiered_expr',
    'new-b': 'tiered_expr',
  })
  expect(lastValue('ModelRatio')).toEqual({ 'priced-model': 1 })
})
