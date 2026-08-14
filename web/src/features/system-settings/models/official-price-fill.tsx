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
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { BadgeDollarSign, Loader2, TriangleAlert } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import { handleServerError } from '@/lib/handle-server-error'

import { fillOfficialModelPrices } from '../api'
import type { OfficialPriceFillItem, OfficialPriceFillResult } from '../types'

function formatPriceSource(
  item: OfficialPriceFillItem,
  t: (key: string) => string
) {
  if (item.fields.billing_expr) return t('Tiered pricing')
  if (item.fields.model_price !== undefined) {
    return `$${item.fields.model_price} / ${t('request')}`
  }
  if (item.fields.model_ratio !== undefined) {
    return `${t('Model ratio')}: ${item.fields.model_ratio}`
  }
  return t('Official price')
}

export function OfficialPriceFill() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [open, setOpen] = useState(false)
  const [preview, setPreview] = useState<OfficialPriceFillResult | null>(null)

  const previewMutation = useMutation({
    mutationFn: () => fillOfficialModelPrices({ dry_run: true }),
    onSuccess: (response) => {
      if (!response.success || !response.data) {
        toast.error(response.message || t('Failed to preview official prices'))
        return
      }
      setPreview(response.data)
      setOpen(true)
    },
    onError: handleServerError,
  })

  const applyMutation = useMutation({
    mutationFn: () => fillOfficialModelPrices({ dry_run: false }),
    onSuccess: (response) => {
      if (!response.success || !response.data) {
        toast.error(response.message || t('Failed to fill official prices'))
        return
      }
      toast.success(
        t('Filled official prices for {{count}} models', {
          count: response.data.filled_models.length,
        })
      )
      setPreview(response.data)
      setOpen(false)
      queryClient.invalidateQueries({ queryKey: ['system-options'] })
      queryClient.invalidateQueries({ queryKey: ['enabled-models'] })
    },
    onError: handleServerError,
  })

  const fillableCount = preview?.filled_models.length ?? 0
  const skippedCount = preview?.skipped_models.length ?? 0
  const isLoading = previewMutation.isPending || applyMutation.isPending

  return (
    <>
      <div className='flex justify-end'>
        <Button
          type='button'
          variant='outline'
          size='sm'
          onClick={() => previewMutation.mutate()}
          disabled={isLoading}
        >
          {previewMutation.isPending ? (
            <Loader2
              aria-hidden='true'
              className='animate-spin'
              data-icon='inline-start'
            />
          ) : (
            <BadgeDollarSign aria-hidden='true' data-icon='inline-start' />
          )}
          {previewMutation.isPending
            ? t('Checking official prices...')
            : t('Fill official prices')}
        </Button>
      </div>

      <ConfirmDialog
        open={open}
        onOpenChange={setOpen}
        title={t('Fill missing official prices?')}
        desc={
          <div className='space-y-2'>
            <p>
              {t(
                'Only enabled models without a base price will be updated. Existing custom prices will not be changed.'
              )}
            </p>
            <div className='flex flex-wrap gap-2'>
              <StatusBadge
                label={t('{{count}} to fill', { count: fillableCount })}
                variant='success'
                copyable={false}
              />
              <StatusBadge
                label={t('{{count}} already priced', {
                  count: preview?.already_priced ?? 0,
                })}
                variant='info'
                copyable={false}
              />
              {skippedCount > 0 && (
                <StatusBadge
                  label={t('{{count}} skipped', { count: skippedCount })}
                  variant='warning'
                  copyable={false}
                />
              )}
            </div>
          </div>
        }
        confirmText={
          applyMutation.isPending ? (
            <>
              <Loader2
                aria-hidden='true'
                className='animate-spin'
                data-icon='inline-start'
              />
              {t('Applying...')}
            </>
          ) : (
            t('Apply {{count}} prices', { count: fillableCount })
          )
        }
        cancelBtnText={fillableCount > 0 ? t('Cancel') : t('Close')}
        disabled={fillableCount === 0}
        isLoading={applyMutation.isPending}
        handleConfirm={() => applyMutation.mutate()}
        className='sm:max-w-2xl'
      >
        <div className='max-h-[min(50vh,28rem)] overflow-y-auto rounded-md border'>
          {preview?.filled_models.map((item) => (
            <div
              key={item.model}
              className='flex min-w-0 flex-col gap-1 border-b px-4 py-3 last:border-b-0 sm:flex-row sm:items-center sm:justify-between sm:gap-4'
            >
              <div className='min-w-0'>
                <div className='font-medium break-all'>{item.model}</div>
                {item.source_model !== item.model && (
                  <div className='text-muted-foreground text-xs break-all'>
                    {t('Source model')}: {item.source_model}
                  </div>
                )}
              </div>
              <span className='shrink-0 font-mono text-sm tabular-nums'>
                {formatPriceSource(item, t)}
              </span>
            </div>
          ))}

          {preview && preview.filled_models.length === 0 && (
            <div className='text-muted-foreground px-4 py-8 text-center text-sm'>
              {t('No missing official prices were found')}
            </div>
          )}
        </div>

        {skippedCount > 0 && (
          <div className='flex items-start gap-2 rounded-md border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-sm'>
            <TriangleAlert
              aria-hidden='true'
              className='mt-0.5 h-4 w-4 shrink-0 text-amber-600'
            />
            <div className='min-w-0'>
              <div className='font-medium'>{t('Skipped models')}</div>
              <div className='text-muted-foreground break-words'>
                {preview?.skipped_models.map((item) => item.model).join(', ')}
              </div>
            </div>
          </div>
        )}
      </ConfirmDialog>
    </>
  )
}
