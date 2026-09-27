/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { CheckSquare, Search } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { invalidateModelPricing } from '@/features/model-pricing/api'
import { handleServerError } from '@/lib/handle-server-error'

import { syncAistarsLabConfig } from '../api'
import { useSystemOptions } from '../hooks/use-system-options'
import type { AistarsLabSyncResult } from '../types'

type ChangeRow = {
  key: string
  type: string
  model: string
  oldValue: string
  newValue: string
}

export function AistarsLabSync() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const { data: systemOptions } = useSystemOptions()
  const [profitPercent, setProfitPercent] = useState<string | null>(null)
  const [result, setResult] = useState<AistarsLabSyncResult | null>(null)
  const [confirmOpen, setConfirmOpen] = useState(false)
  const configuredProfit = useMemo(() => {
    const options = Array.isArray(systemOptions?.data) ? systemOptions.data : []
    const stored = Number(
      options.find((item) => item.key === 'AistarsLabMarkupRate')?.value
    )
    return Number.isFinite(stored) && stored > 0
      ? String(Math.round((stored - 1) * 10000) / 100)
      : '30'
  }, [systemOptions])
  const displayedProfit = profitPercent ?? configuredProfit
  const profit = Number(displayedProfit)
  const valid =
    displayedProfit.trim() !== '' && Number.isFinite(profit) && profit >= 0

  const syncMutation = useMutation({
    mutationFn: async (dryRun: boolean) => {
      const response = await syncAistarsLabConfig({
        dry_run: dryRun,
        markup_rate: 1 + profit / 100,
        channel_id: 17,
      })
      if (!response.success || !response.data)
        throw new Error(response.message || t('AistarsLab price sync failed'))
      return response.data
    },
    onSuccess: async (data, dryRun) => {
      setResult(data)
      if (dryRun) toast.success(t('Channel 17 price preview completed'))
      else {
        setProfitPercent(
          String(Math.round((data.markup_rate - 1) * 10000) / 100)
        )
        setConfirmOpen(false)
        toast.success(t('Channel 17 prices synced'))
        await invalidateModelPricing(queryClient)
      }
    },
    onError: (error: Error) =>
      handleServerError(error, t('AistarsLab price sync failed')),
  })

  const run = (dryRun: boolean) => {
    if (!valid) return toast.error(t('Profit percentage cannot be less than 0'))
    syncMutation.mutate(dryRun)
  }

  const rows = useMemo<ChangeRow[]>(() => {
    if (!result) return []
    const value = (input: number | string | undefined) =>
      input == null || input === '' ? t('Not set') : String(input)
    return [
      ...result.price_changes.map((item) => ({
        key: `price-${item.model}`,
        type: t('Unit price'),
        model: item.model,
        oldValue: value(item.old),
        newValue: value(item.new),
      })),
      ...result.expression_changes.map((item) => ({
        key: `expr-${item.model}`,
        type: t('Billing formula'),
        model: item.model,
        oldValue: value(item.old),
        newValue: value(item.new),
      })),
      ...result.mapping_changes.map((item) => ({
        key: `mapping-${item.model}`,
        type: t('Channel mapping'),
        model: item.model,
        oldValue: value(item.old),
        newValue: value(item.new),
      })),
    ]
  }, [result, t])

  return (
    <section className='border-border/70 space-y-3 border-b pb-4'>
      <div className='flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between'>
        <div className='space-y-1'>
          <h3 className='text-sm font-semibold'>
            {t('Channel 17 profit price sync')}
          </h3>
          <p className='text-muted-foreground text-xs'>
            {t(
              'Synchronize Channel 17 models, mappings, billing formulas, and model prices from AistarsLab'
            )}
          </p>
        </div>
        <div className='flex flex-wrap items-end gap-2'>
          <label className='grid gap-1 text-xs'>
            <span>{t('Profit percentage')}</span>
            <div className='relative w-32'>
              <Input
                type='number'
                min='0'
                step='1'
                value={displayedProfit}
                onChange={(event) => setProfitPercent(event.target.value)}
                className='h-9 pr-7'
                aria-invalid={!valid}
              />
              <span className='text-muted-foreground pointer-events-none absolute top-1/2 right-2 -translate-y-1/2'>
                %
              </span>
            </div>
          </label>
          <Button
            variant='outline'
            onClick={() => run(true)}
            disabled={!valid || syncMutation.isPending}
          >
            <Search className='size-4' />
            {t('Preview')}
          </Button>
          <Button
            onClick={() => setConfirmOpen(true)}
            disabled={!valid || syncMutation.isPending}
          >
            <CheckSquare className='size-4' />
            {t('Sync Channel 17')}
          </Button>
        </div>
      </div>
      {result && (
        <div className='space-y-2'>
          <div className='flex flex-wrap gap-2 text-xs'>
            <Badge variant='secondary'>
              {t('Models')}: {result.total_models}
            </Badge>
            <Badge variant='secondary'>
              {t('Profit percentage')}:{' '}
              {Math.round((result.markup_rate - 1) * 10000) / 100}%
            </Badge>
            <Badge variant={result.dry_run ? 'outline' : 'default'}>
              {result.dry_run ? t('Preview') : t('Applied')}
            </Badge>
          </div>
          <div className='max-h-72 overflow-auto rounded-md border'>
            <table className='w-full min-w-3xl text-left text-xs'>
              <thead className='bg-muted sticky top-0'>
                <tr>
                  <th className='p-2'>{t('Type')}</th>
                  <th className='p-2'>{t('Model')}</th>
                  <th className='p-2'>{t('Current value')}</th>
                  <th className='p-2'>{t('Synchronized value')}</th>
                </tr>
              </thead>
              <tbody>
                {rows.map((row) => (
                  <tr key={row.key} className='border-t'>
                    <td className='p-2'>{row.type}</td>
                    <td className='p-2'>{row.model}</td>
                    <td className='p-2 break-all'>{row.oldValue}</td>
                    <td className='p-2 break-all'>{row.newValue}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}
      <ConfirmDialog
        open={confirmOpen}
        onOpenChange={setConfirmOpen}
        title={t('Confirm Channel 17 price sync')}
        desc={t(
          'This updates Channel 17 mappings and model prices using a {{profit}}% profit margin. Preview before applying.',
          { profit: displayedProfit }
        )}
        confirmText={t('Apply Sync')}
        handleConfirm={() => run(false)}
        isLoading={syncMutation.isPending}
      />
    </section>
  )
}
