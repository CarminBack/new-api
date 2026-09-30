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

import { ModelRatioVisualEditor } from '../model-ratio-visual-editor'
import { pickOfficialPrices } from '../upstream-ratio-sync-helpers'

const OFFICIAL = '官方倍率预设(-100)'
const MODELS_DEV = 'models.dev 价格预设(-101)'
const t = (key: string) => key

let client: QueryClient | undefined

afterEach(() => {
  client?.clear()
  localStorage.clear()
  vi.restoreAllMocks()
})

it('prefers the official preset and falls back to models.dev', () => {
  const selection = pickOfficialPrices(
    {
      a: {
        current: {},
        upstreams: {
          [MODELS_DEV]: { model_ratio: 9 },
          [OFFICIAL]: { model_ratio: 1, completion_ratio: 4 },
        },
      },
      b: { current: {}, upstreams: { [MODELS_DEV]: { model_ratio: 2 } } },
    },
    ['a', 'b', 'c'],
    t
  )
  expect(selection.resolutions).toEqual({
    a: { model_ratio: 1, completion_ratio: 4 },
    b: { model_ratio: 2 },
  })
  expect(selection.missing).toBe(1)
  expect(selection.previews.map((item) => item.channel)).toEqual([
    'Official pricing preset',
    'models.dev pricing preset',
  ])
})

it('fetches official prices for unset models and saves after confirmation', async () => {
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/pricing') {
      return { data: { success: true, data: [], vendors: [] } }
    }
    return { data: { success: true, data: {} } }
  })
  const post = vi.spyOn(api, 'post').mockResolvedValue({
    data: {
      success: true,
      data: {
        differences: {},
        test_results: [
          { name: OFFICIAL, status: 'success' },
          { name: MODELS_DEV, status: 'success' },
        ],
        prices: {
          'new-a': {
            current: {},
            upstreams: {
              [OFFICIAL]: { model_ratio: 1.25, completion_ratio: 8 },
            },
          },
          'priced-model': {
            current: { model_ratio: 1 },
            upstreams: { [OFFICIAL]: { model_ratio: 5 } },
          },
        },
      },
    },
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
  fireEvent.click(screen.getByRole('button', { name: 'Fetch official prices' }))

  expect(await screen.findByText('Preview price changes')).toBeInTheDocument()
  expect(post).toHaveBeenCalledWith(
    '/api/ratio_sync/fetch',
    expect.objectContaining({
      upstreams: [
        expect.objectContaining({ id: -100 }),
        expect.objectContaining({ id: -101 }),
      ],
    })
  )
  expect(
    screen.getByText(/1 models have no official price and are skipped/)
  ).toBeInTheDocument()
  expect(onSave).not.toHaveBeenCalled()

  fireEvent.click(screen.getByRole('button', { name: 'Confirm Changes' }))

  await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1))
  const lastValue = (key: string) =>
    JSON.parse(
      [...onChange.mock.calls].reverse().find((call) => call[0] === key)?.[1] ??
        '{}'
    )
  expect(lastValue('ModelRatio')).toEqual({ 'priced-model': 1, 'new-a': 1.25 })
  expect(lastValue('CompletionRatio')).toEqual({ 'new-a': 8 })
})
