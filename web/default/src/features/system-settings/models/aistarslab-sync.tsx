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
import { CheckSquare, Search } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Input } from '@/components/ui/input'

import { syncAistarsLabConfig } from '../api'
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
  const [profitPercent, setProfitPercent] = useState('30')
  const [result, setResult] = useState<AistarsLabSyncResult | null>(null)
  const [confirmOpen, setConfirmOpen] = useState(false)

  const syncMutation = useMutation({
    mutationFn: (dryRun: boolean) => {
      const profit = Number(profitPercent)
      return syncAistarsLabConfig({
        dry_run: dryRun,
        markup_rate: 1 + profit / 100,
      })
    },
    onSuccess: (response, dryRun) => {
      if (!response.success || !response.data) {
        toast.error(response.message || t('AistarsLab Jimeng sync failed'))
        return
      }
      setResult(response.data)
      if (dryRun) {
        toast.success(t('AistarsLab Jimeng sync preview completed'))
        return
      }
      toast.success(t('AistarsLab Jimeng sync applied'))
      setConfirmOpen(false)
      queryClient.invalidateQueries({ queryKey: ['system-options'] })
    },
    onError: (error: Error) => {
      toast.error(error.message || t('AistarsLab Jimeng sync failed'))
    },
  })

  const parsedProfit = Number(profitPercent)
  const profitIsValid =
    profitPercent.trim() !== '' &&
    Number.isFinite(parsedProfit) &&
    parsedProfit >= 0

  const runSync = (dryRun: boolean) => {
    if (!profitIsValid) {
      toast.error(t('Profit percentage cannot be less than 0'))
      return
    }
    syncMutation.mutate(dryRun)
  }

  const rows = useMemo<ChangeRow[]>(() => {
    if (!result) return []
    const formatValue = (value: number | string | undefined) =>
      value === undefined || value === '' ? t('Not set') : String(value)
    const items: ChangeRow[] = []

    for (const model of result.added_models ?? []) {
      items.push({
        key: `added-${model}`,
        type: t('Added model'),
        model,
        oldValue: t('Not set'),
        newValue: t('Add'),
      })
    }
    for (const model of result.removed_models ?? []) {
      items.push({
        key: `removed-${model}`,
        type: t('Removed model'),
        model,
        oldValue: t('Exists'),
        newValue: t('Remove'),
      })
    }
    for (const item of result.price_changes ?? []) {
      items.push({
        key: `price-${item.model}`,
        type: t('Fixed price'),
        model: item.model,
        oldValue: formatValue(item.old),
        newValue: formatValue(item.new),
      })
    }
    for (const item of result.task_unit_changes ?? []) {
      items.push({
        key: `unit-${item.model}`,
        type: t('Billing unit'),
        model: item.model,
        oldValue: formatValue(item.old),
        newValue: formatValue(item.new),
      })
    }
    for (const item of result.mapping_changes ?? []) {
      items.push({
        key: `mapping-${item.model}`,
        type: t('Channel mapping'),
        model: item.model,
        oldValue: formatValue(item.old),
        newValue: formatValue(item.new),
      })
    }
    return items
  }, [result, t])

  return (
    <>
      <Card>
        <CardHeader>
          <CardTitle>{t('AistarsLab Jimeng model sync')}</CardTitle>
          <CardDescription>
            {t(
              'Synchronize models, prices, billing units, and channel mappings'
            )}
          </CardDescription>
        </CardHeader>
        <CardContent className='space-y-4'>
          <div className='flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between'>
            <label className='grid max-w-56 gap-1.5 text-sm'>
              <span>{t('Profit percentage')}</span>
              <div className='relative'>
                <Input
                  type='number'
                  min='0'
                  step='1'
                  value={profitPercent}
                  onChange={(event) => setProfitPercent(event.target.value)}
                  disabled={syncMutation.isPending}
                  aria-invalid={!profitIsValid}
                  className='pr-8'
                />
                <span className='text-muted-foreground pointer-events-none absolute top-1/2 right-3 -translate-y-1/2'>
                  %
                </span>
              </div>
            </label>
            <div className='flex flex-col gap-2 sm:flex-row'>
              <Button
                variant='outline'
                onClick={() => runSync(true)}
                disabled={syncMutation.isPending || !profitIsValid}
              >
                <Search className='mr-2 h-4 w-4' />
                {t('Preview Jimeng sync')}
              </Button>
              <Button
                variant='secondary'
                onClick={() => setConfirmOpen(true)}
                disabled={syncMutation.isPending || !profitIsValid}
              >
                <CheckSquare className='mr-2 h-4 w-4' />
                {t('Apply Jimeng sync')}
              </Button>
            </div>
          </div>

          {syncMutation.isPending && (
            <div className='text-muted-foreground flex items-center gap-2 text-sm'>
              <span className='h-4 w-4 animate-spin rounded-full border-2 border-current border-t-transparent' />
              {t('Synchronizing Jimeng configuration...')}
            </div>
          )}

          {result ? (
            <div className='space-y-3'>
              <div className='flex flex-wrap gap-2'>
                <Badge variant='secondary'>
                  {t('Total models')}: {result.total_models}
                </Badge>
                <Badge variant='secondary'>
                  {t('Added')}: {result.added_models?.length ?? 0}
                </Badge>
                <Badge variant='secondary'>
                  {t('Removed')}: {result.removed_models?.length ?? 0}
                </Badge>
                <Badge variant='secondary'>
                  {t('Price changes')}: {result.price_changes?.length ?? 0}
                </Badge>
                <Badge variant='secondary'>
                  {t('Billing unit changes')}:{' '}
                  {result.task_unit_changes?.length ?? 0}
                </Badge>
                <Badge variant='secondary'>
                  {t('Mapping changes')}: {result.mapping_changes?.length ?? 0}
                </Badge>
                <Badge variant='secondary'>
                  {t('Profit percentage')}:{' '}
                  {Math.round((result.markup_rate - 1) * 10000) / 100}%
                </Badge>
                <Badge variant={result.dry_run ? 'outline' : 'default'}>
                  {result.dry_run ? t('Preview') : t('Applied')}
                </Badge>
              </div>

              {rows.length > 0 ? (
                <div className='max-h-96 overflow-auto rounded-md border'>
                  <table className='w-full min-w-3xl text-left text-sm'>
                    <thead className='bg-muted sticky top-0'>
                      <tr>
                        <th className='px-3 py-2 font-medium'>{t('Type')}</th>
                        <th className='px-3 py-2 font-medium'>{t('Model')}</th>
                        <th className='px-3 py-2 font-medium'>
                          {t('Current value')}
                        </th>
                        <th className='px-3 py-2 font-medium'>
                          {t('Synchronized value')}
                        </th>
                      </tr>
                    </thead>
                    <tbody>
                      {rows.map((row) => (
                        <tr key={row.key} className='border-t'>
                          <td className='px-3 py-2'>
                            <Badge variant='outline'>{row.type}</Badge>
                          </td>
                          <td className='px-3 py-2 break-all'>{row.model}</td>
                          <td className='px-3 py-2 break-all'>
                            {row.oldValue}
                          </td>
                          <td className='px-3 py-2 break-all'>
                            {row.newValue}
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              ) : (
                <div className='text-muted-foreground rounded-md border p-6 text-center text-sm'>
                  {t('No synchronization changes')}
                </div>
              )}
            </div>
          ) : (
            <div className='text-muted-foreground rounded-md border p-6 text-center text-sm'>
              {t('No AistarsLab Jimeng sync result yet')}
            </div>
          )}
        </CardContent>
      </Card>

      <ConfirmDialog
        open={confirmOpen}
        onOpenChange={setConfirmOpen}
        title={t('Confirm AistarsLab Jimeng sync')}
        desc={t(
          'This will update Jimeng models, prices, billing units, and channel mappings. Current profit percentage: {{profit}}%',
          { profit: profitPercent }
        )}
        confirmText={t('Apply sync')}
        handleConfirm={() => runSync(false)}
        isLoading={syncMutation.isPending}
      />
    </>
  )
}
