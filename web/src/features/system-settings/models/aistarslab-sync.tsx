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
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { invalidateModelPricing } from '@/features/model-pricing/api'
import { handleServerError } from '@/lib/handle-server-error'

import { syncAistarsLabConfig, syncYeepayConfig } from '../api'
import { useSystemOptions } from '../hooks/use-system-options'
import type { AistarsLabSyncResult } from '../types'

type ChangeRow = {
  key: string
  type: string
  model: string
  oldValue: string
  newValue: string
}

export function AistarsLabSync(props: { yeepay?: boolean }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const { data: systemOptions } = useSystemOptions()
  const [profitPercent, setProfitPercent] = useState<string | null>(null)
  const [result, setResult] = useState<AistarsLabSyncResult | null>(null)
  const [confirmOpen, setConfirmOpen] = useState(false)
  const [creditOverride, setCreditRate] = useState<string | null>(null)
  const [precisionOverride, setPrecision] = useState<string | null>(null)
  const [allModels, setAllModels] = useState(false)
  const [updatePrices, setUpdatePrices] = useState(false)
  const [previewForm, setPreviewForm] = useState('')
  const provider = props.yeepay ? 'Yeepay' : 'AistarsLab'
  const channelId = props.yeepay ? 282 : 17

  const configuredProfit = useMemo(() => {
    const options = Array.isArray(systemOptions?.data) ? systemOptions.data : []
    let stored = Number(
      options.find((item) => item.key === 'AistarsLabMarkupRate')?.value
    )
    if (props.yeepay) {
      stored = 1.35
      try {
        const saved = JSON.parse(
          options.find((item) => item.key === 'YeepaySyncSettings')?.value ||
            '{}'
        ) as { markup_rate?: number }
        stored = saved.markup_rate ?? stored
      } catch {
        /* Keep the explicit editable preview default. */
      }
    }
    return Number.isFinite(stored) && stored > 0
      ? String(Math.round((stored - 1) * 10000) / 100)
      : '30'
  }, [systemOptions, props.yeepay])
  const savedYeepayRates = useMemo(() => {
    const options = Array.isArray(systemOptions?.data) ? systemOptions.data : []
    try {
      return JSON.parse(
        options.find((item) => item.key === 'YeepaySyncSettings')?.value || '{}'
      ) as { credit_rate?: number; decimal_places?: number }
    } catch {
      return {}
    }
  }, [systemOptions])
  const creditRate =
    creditOverride ?? String(savedYeepayRates.credit_rate ?? 10)
  const precision =
    precisionOverride ?? String(savedYeepayRates.decimal_places ?? 2)
  const displayedProfit = profitPercent ?? configuredProfit
  const profit = Number(displayedProfit)
  const valid =
    displayedProfit.trim() !== '' &&
    Number.isFinite(profit) &&
    profit >= 0 &&
    (!props.yeepay ||
      (creditRate.trim() !== '' &&
        Number.isFinite(Number(creditRate)) &&
        Number(creditRate) > 0 &&
        precision.trim() !== '' &&
        Number.isInteger(Number(precision)) &&
        Number(precision) >= 0 &&
        Number(precision) <= 6))
  const formKey = JSON.stringify([
    displayedProfit,
    creditRate,
    precision,
    allModels,
    updatePrices,
  ])

  const syncMutation = useMutation({
    mutationFn: async (dryRun: boolean) => {
      const request = {
        dry_run: dryRun,
        markup_rate: 1 + profit / 100,
        channel_id: channelId,
        ...(!dryRun ? { preview_token: result?.preview_token } : {}),
      }
      const response = props.yeepay
        ? await syncYeepayConfig({
            ...request,
            credit_rate: Number(creditRate),
            decimal_places: Number(precision),
            all_models: allModels,
            update_prices: updatePrices,
          })
        : await syncAistarsLabConfig(request)
      if (!response.success || !response.data) {
        throw new Error(
          response.message || t('{{provider}} price sync failed', { provider })
        )
      }
      return { data: response.data, requestForm: formKey }
    },
    onSuccess: async ({ data, requestForm }, dryRun) => {
      setResult(data)
      if (dryRun) {
        setPreviewForm(requestForm)
        toast.success(
          t('Channel {{channel}} price preview completed', {
            channel: channelId,
          })
        )
      } else {
        setProfitPercent(
          String(Math.round((data.markup_rate - 1) * 10000) / 100)
        )
        setConfirmOpen(false)
        toast.success(
          t('Channel {{channel}} prices synced', { channel: channelId })
        )
        await invalidateModelPricing(queryClient)
      }
    },
    onError: (error: Error) =>
      handleServerError(
        error,
        t('{{provider}} price sync failed', { provider })
      ),
  })

  const run = (dryRun: boolean) => {
    if (!valid) return toast.error(t('Profit percentage cannot be less than 0'))
    syncMutation.mutate(dryRun)
  }

  const rows = useMemo<ChangeRow[]>(() => {
    if (!result) return []
    const value = (input: number | string | undefined) =>
      input == null || input === '' ? t('Not set') : String(input)
    const capabilityValue = (input?: string) => {
      if (!input) return t('Not set')
      try {
        const caps = JSON.parse(input) as Record<string, unknown>
        const parts: string[] = []
        for (const [key, label] of Object.entries({
          resolutions: t('Resolution'),
          ratios: t('Aspect ratio'),
          modes: t('Generation mode'),
          secondsSteps: t('Duration options'),
          minSeconds: t('Minimum duration'),
          maxSeconds: t('Maximum duration'),
          maxImages: t('Maximum reference images'),
          maxVideos: t('Maximum reference videos'),
          maxAudios: t('Maximum reference audios'),
        })) {
          const entry = caps[key]
          if (entry !== undefined) {
            parts.push(
              `${label}: ${Array.isArray(entry) ? entry.join(' / ') : String(entry)}`
            )
          }
        }
        return parts.join('; ')
      } catch {
        return input
      }
    }
    return [
      ...(result.plugin_version &&
      result.plugin_version !== result.previous_plugin_version
        ? [
            {
              key: 'plugin',
              type: t('Task plugin version'),
              model: provider,
              oldValue: value(result.previous_plugin_version),
              newValue: value(result.plugin_version),
            },
          ]
        : []),
      ...(result.capability_changes ?? []).map((item) => ({
        key: `capability-${item.model}`,
        type: t('Model capabilities'),
        model: item.model,
        oldValue: capabilityValue(item.old),
        newValue: capabilityValue(item.new),
      })),
      ...(result.added_models ?? []).map((model) => ({
        key: `added-${model}`,
        type: t('Added models'),
        model,
        oldValue: t('Not set'),
        newValue: t('Models'),
      })),
      ...(result.removed_models ?? []).map((model) => ({
        key: `removed-${model}`,
        type: t('Removed models'),
        model,
        oldValue: t('Models'),
        newValue: t('Not set'),
      })),
      ...(result.mode_changes ?? []).map((item) => ({
        key: `mode-${item.model}`,
        type: t('Billing mode'),
        model: item.model,
        oldValue: value(item.old),
        newValue: value(item.new),
      })),
      ...(result.price_changes ?? []).map((item) => ({
        key: `price-${item.model}`,
        type: t('Unit price'),
        model: item.model,
        oldValue: value(item.old),
        newValue: value(item.new),
      })),
      ...(result.expression_changes ?? []).map((item) => ({
        key: `expr-${item.model}`,
        type: t('Billing formula'),
        model: item.model,
        oldValue: value(item.old),
        newValue: value(item.new),
      })),
      ...(result.mapping_changes ?? []).map((item) => ({
        key: `mapping-${item.model}`,
        type: t('Channel mapping'),
        model: item.model,
        oldValue: value(item.old),
        newValue: value(item.new),
      })),
    ]
  }, [result, t, provider])

  return (
    <section className='border-border/70 space-y-3 border-b pb-4'>
      <div className='flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between'>
        <div className='space-y-1'>
          <h3 className='text-sm font-semibold'>
            {t('Channel {{channel}} profit price sync', { channel: channelId })}
          </h3>
          <p className='text-muted-foreground text-xs'>
            {t(
              'Synchronize Channel {{channel}} models, mappings, billing formulas, prices and capabilities from {{provider}}',
              { channel: channelId, provider }
            )}
          </p>
        </div>
        <div className='flex flex-wrap items-end gap-2'>
          {props.yeepay && (
            <>
              <label className='grid gap-1 text-xs'>
                <span>{t('Credits per USD')}</span>
                <Input
                  className='h-9 w-28'
                  type='number'
                  min='0.000001'
                  value={creditRate}
                  onChange={(event) => setCreditRate(event.target.value)}
                />
              </label>
              <label className='grid gap-1 text-xs'>
                <span>{t('Round prices upward to decimal places')}</span>
                <Input
                  className='h-9 w-24'
                  type='number'
                  min='0'
                  max='6'
                  step='1'
                  value={precision}
                  onChange={(event) => setPrecision(event.target.value)}
                />
              </label>
              <label className='flex items-center gap-2 text-xs'>
                <Checkbox
                  checked={allModels}
                  onCheckedChange={(checked) => setAllModels(checked === true)}
                />
                {t(
                  'Include all available models, including manually excluded models'
                )}
              </label>
              <label className='flex items-center gap-2 text-xs'>
                <Checkbox
                  checked={updatePrices}
                  onCheckedChange={(checked) =>
                    setUpdatePrices(checked === true)
                  }
                />
                {t('Update existing Yeepay prices')}
              </label>
            </>
          )}
          <label className='grid gap-1 text-xs'>
            <span>
              {t(props.yeepay ? 'Cost markup percentage' : 'Profit percentage')}
            </span>
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
            disabled={
              !valid ||
              syncMutation.isPending ||
              !result?.dry_run ||
              !result.preview_token ||
              result.markup_rate !== 1 + profit / 100 ||
              previewForm !== formKey
            }
          >
            <CheckSquare className='size-4' />
            {t('Sync Channel {{channel}}', { channel: channelId })}
          </Button>
        </div>
      </div>
      {result && (
        <div className='space-y-2'>
          {(result.warnings ?? []).map((warning) => (
            <p key={warning} className='text-sm'>
              {t(warning)}
            </p>
          ))}
          <div className='flex flex-wrap gap-2 text-xs'>
            <Badge variant='secondary'>
              {t('Models')}: {result.total_models}
            </Badge>
            <Badge variant='secondary'>
              {t(props.yeepay ? 'Cost markup percentage' : 'Profit percentage')}
              : {Math.round((result.markup_rate - 1) * 10000) / 100}%
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
        title={t('Confirm Channel {{channel}} price sync', {
          channel: channelId,
        })}
        desc={t(
          'Apply the previewed model, mapping, capability and price changes to channel {{channel}} with a {{profit}}% cost markup. Changed upstream data requires a new preview.',
          { channel: channelId, profit: displayedProfit }
        )}
        disabled={!valid || previewForm !== formKey || !result?.dry_run}
        confirmText={t('Apply Sync')}
        handleConfirm={() => run(false)}
        isLoading={syncMutation.isPending}
      />
    </section>
  )
}
