/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.
*/
import { useQueries, useQuery } from '@tanstack/react-query'
import { Activity, Clock3, Loader2, RefreshCw, TestTube } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { StatusBadge, type StatusVariant } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import {
  getPerfMetrics,
  getPerfMetricsSummary,
} from '@/features/performance-metrics/api'
import { formatLatency } from '@/features/performance-metrics/lib/format'
import type {
  PerformanceGroup,
  PerformanceMetricsData,
  PerfModelSummary,
  PerformanceSeriesPoint,
} from '@/features/performance-metrics/types'
import { usePricingData } from '@/features/pricing/hooks/use-pricing-data'
import {
  ADMIN_PERMISSION_ACTIONS,
  ADMIN_PERMISSION_RESOURCES,
  hasPermission,
} from '@/lib/admin-permissions'
import { useAuthStore } from '@/stores/auth-store'

import { testAllChannels } from '../api'

const EMPTY_MODELS: PerfModelSummary[] = []
const DETAIL_LIMIT = 24
const TTFT_WARNING_THRESHOLD_MS = 5_000

type MonitorStatus = {
  label: string
  variant: StatusVariant
}

function latencyStatus(
  requestCount: number,
  successCount: number,
  ttftMs: number
): MonitorStatus {
  if (requestCount <= 0 || successCount <= 0 || ttftMs <= 0) {
    return { label: 'Abnormal', variant: 'danger' }
  }
  if (ttftMs <= TTFT_WARNING_THRESHOLD_MS) {
    return { label: 'Normal', variant: 'success' }
  }
  return { label: 'Warning', variant: 'warning' }
}

function latestTtftMs(group: PerformanceGroup): number {
  for (let index = group.series.length - 1; index >= 0; index -= 1) {
    const value = group.series[index]?.avg_ttft_ms ?? 0
    if (value > 0) return value
  }
  return 0
}

function groupStatus(group: PerformanceGroup): MonitorStatus {
  return latencyStatus(
    group.request_count,
    group.success_count,
    latestTtftMs(group)
  )
}

function pointStatus(point: PerformanceSeriesPoint): MonitorStatus {
  return latencyStatus(
    point.request_count,
    point.success_count,
    point.avg_ttft_ms
  )
}

function modelStatus(groups: PerformanceGroup[]): MonitorStatus {
  if (groups.length === 0) return { label: 'Abnormal', variant: 'danger' }
  if (groups.some((group) => groupStatus(group).variant === 'danger')) {
    return { label: 'Abnormal', variant: 'danger' }
  }
  if (groups.some((group) => groupStatus(group).variant === 'warning')) {
    return { label: 'Warning', variant: 'warning' }
  }
  return { label: 'Normal', variant: 'success' }
}

function HistoryStrip({ group }: { group: PerformanceGroup }) {
  const { t } = useTranslation()
  const points = group.series.slice(-60)
  if (points.length === 0) {
    return <span className='text-muted-foreground text-xs'>{t('No data')}</span>
  }

  return (
    <div className='flex min-w-0 flex-1 items-center gap-0.5 overflow-hidden'>
      {points.map((point) => {
        const status = pointStatus(point)
        let color = 'bg-red-500'
        if (status.variant === 'success') {
          color = 'bg-emerald-500'
        } else if (status.variant === 'warning') {
          color = 'bg-amber-500'
        } else if (point.request_count <= 0) {
          color = 'bg-muted'
        }
        const title = `${new Date(point.ts * 1000).toLocaleString()} · ${point.success_count}/${point.request_count}`
        return (
          <span
            key={point.ts}
            title={title}
            className={`h-4 min-w-1 flex-1 rounded-sm ${color}`}
          />
        )
      })}
    </div>
  )
}

function MetricBlock({
  label,
  value,
  hint,
}: {
  label: string
  value: string
  hint?: string
}) {
  return (
    <div className='bg-muted/40 min-w-0 rounded-md p-3'>
      <div className='text-muted-foreground flex items-center gap-1 text-xs'>
        <Clock3 className='size-3' />
        <span className='truncate'>{label}</span>
      </div>
      <div className='mt-1 font-mono text-lg font-semibold tabular-nums'>
        {value}
      </div>
      {hint && <div className='text-muted-foreground text-xs'>{hint}</div>}
    </div>
  )
}

function ModelMonitorCard({
  summary,
  details,
  groupRatios,
  usableGroups,
}: {
  summary: PerfModelSummary
  details?: PerformanceMetricsData
  groupRatios: Record<string, number>
  usableGroups: Record<string, { desc: string; ratio: number }>
}) {
  const { t } = useTranslation()
  const groups = (details?.data.groups ?? []).filter(
    (group) => usableGroups[group.group] !== undefined
  )
  const status = modelStatus(groups)

  return (
    <article className='bg-background rounded-lg border p-4 shadow-sm'>
      <header className='flex items-start justify-between gap-3 border-b pb-3'>
        <div className='flex min-w-0 items-center gap-3'>
          <div className='bg-muted flex size-10 shrink-0 items-center justify-center rounded-md text-lg font-semibold'>
            {summary.model_name.slice(0, 1).toUpperCase()}
          </div>
          <div className='min-w-0'>
            <div className='text-muted-foreground text-[10px] font-medium tracking-[0.16em] uppercase'>
              {summary.model_name.includes('claude') ? 'Anthropic' : 'OpenAI'}
            </div>
            <h3 className='truncate text-lg font-semibold'>
              {summary.model_name}
            </h3>
          </div>
        </div>
        <StatusBadge
          label={t(status.label)}
          variant={status.variant}
          size='sm'
          copyable={false}
        />
      </header>

      <div className='space-y-4 pt-3'>
        {groups.map((group) => {
          const ratio = groupRatios[group.group]
          const groupState = groupStatus(group)
          const availability = `${group.success_rate.toFixed(2)}%`
          const availabilityHint = `${group.success_count}/${group.request_count}`
          return (
            <section key={group.group} className='space-y-2'>
              <div className='flex items-center justify-between gap-2'>
                <div className='flex min-w-0 items-center gap-2'>
                  <span className='truncate font-mono text-sm font-semibold'>
                    {group.group}
                  </span>
                  {typeof ratio === 'number' && (
                    <span className='border-info/40 bg-info/10 text-info rounded-full border px-2 py-0.5 font-mono text-xs'>
                      {t('Ratio')} {ratio}
                    </span>
                  )}
                </div>
                <StatusBadge
                  label={t(groupState.label)}
                  variant={groupState.variant}
                  size='sm'
                  copyable={false}
                />
              </div>
              <div className='grid grid-cols-1 gap-2 sm:grid-cols-3'>
                <MetricBlock
                  label={t('Average TTFT')}
                  value={formatLatency(latestTtftMs(group))}
                />
                <MetricBlock
                  label={t('Availability')}
                  value={availability}
                  hint={availabilityHint}
                />
                <MetricBlock
                  label={t('Conversation latency')}
                  value={formatLatency(group.avg_latency_ms)}
                />
              </div>
              <div className='flex items-center gap-2 border-t pt-2'>
                <span className='text-muted-foreground shrink-0 text-[10px] font-medium tracking-wider uppercase'>
                  {t('History')}
                </span>
                <HistoryStrip group={group} />
              </div>
            </section>
          )
        })}
        {groups.length === 0 && (
          <div className='text-muted-foreground py-5 text-center text-sm'>
            {t('No latency data available')}
          </div>
        )}
      </div>
    </article>
  )
}

export function ChannelModelMonitorPanel({
  showActions = true,
}: {
  showActions?: boolean
}) {
  const { t } = useTranslation()
  const [isTesting, setIsTesting] = useState(false)
  const currentUser = useAuthStore((state) => state.auth.user)
  const canOperate = hasPermission(
    currentUser,
    ADMIN_PERMISSION_RESOURCES.CHANNEL,
    ADMIN_PERMISSION_ACTIONS.OPERATE
  )
  const pricing = usePricingData()
  const summaryQuery = useQuery({
    queryKey: ['perf-metrics-summary', 24],
    queryFn: () => getPerfMetricsSummary(24),
    refetchInterval: 60_000,
    refetchIntervalInBackground: false,
    retry: false,
  })
  const summaries = summaryQuery.data?.data?.models ?? EMPTY_MODELS
  const visibleSummaries = summaries.slice(0, DETAIL_LIMIT)
  const detailQueries = useQueries({
    queries: visibleSummaries.map((model) => ({
      queryKey: ['perf-metrics', model.model_name, 24],
      queryFn: () => getPerfMetrics(model.model_name, 24),
      staleTime: 60_000,
      retry: false,
    })),
  })
  const detailMap = useMemo(() => {
    const map = new Map<string, PerformanceMetricsData>()
    visibleSummaries.forEach((model, index) => {
      const data = detailQueries[index]?.data
      if (data) map.set(model.model_name, data)
    })
    return map
  }, [detailQueries, visibleSummaries])
  const groupRatios = useMemo(
    () => ({ ...pricing.groupRatio }),
    [pricing.groupRatio]
  )
  const usableGroups = pricing.usableGroup
  const normalCount = visibleSummaries.filter((model) => {
    const details = detailMap.get(model.model_name)
    const groups = (details?.data.groups ?? []).filter(
      (group) => usableGroups[group.group] !== undefined
    )
    return modelStatus(groups).variant === 'success'
  }).length
  const warningCount = visibleSummaries.filter((model) => {
    const details = detailMap.get(model.model_name)
    const groups = (details?.data.groups ?? []).filter(
      (group) => usableGroups[group.group] !== undefined
    )
    return modelStatus(groups).variant === 'warning'
  }).length

  const refresh = async () => {
    await summaryQuery.refetch()
    await Promise.all(detailQueries.map((query) => query.refetch()))
  }

  const runDetection = async () => {
    if (!canOperate || isTesting) return
    setIsTesting(true)
    try {
      const response = await testAllChannels()
      if (!response.success) {
        throw new Error(response.message || t('Failed to test all channels'))
      }
      toast.success(
        t(
          'Testing all enabled channels started. Please refresh to see results.'
        )
      )
      await refresh()
    } catch (error) {
      toast.error(
        error instanceof Error
          ? error.message
          : t('Failed to test all channels')
      )
    } finally {
      setIsTesting(false)
    }
  }

  return (
    <div className='flex h-full min-h-0 flex-col gap-3 overflow-auto'>
      <div className='flex flex-wrap items-center gap-2'>
        <div className='bg-background flex items-center gap-2 rounded-md border px-3 py-1.5'>
          <Activity className='text-muted-foreground size-4' />
          <span className='text-sm'>
            {t('Models')} <strong>{visibleSummaries.length}</strong>
          </span>
        </div>
        <div className='bg-background rounded-md border px-3 py-1.5 text-sm'>
          {t('Normal')}{' '}
          <strong className='text-emerald-600'>{normalCount}</strong>
        </div>
        <div className='bg-background rounded-md border px-3 py-1.5 text-sm'>
          {t('Warning')}{' '}
          <strong className='text-amber-600'>{warningCount}</strong>
        </div>
        <div className='text-muted-foreground text-xs'>
          {t('Updated')} {new Date().toLocaleTimeString()} · {t('24h window')}
        </div>
        <div className='ml-auto flex items-center gap-2'>
          {showActions && (
            <Button
              variant='outline'
              size='sm'
              onClick={runDetection}
              disabled={!canOperate || isTesting}
            >
              {isTesting ? (
                <Loader2 className='size-4 animate-spin' />
              ) : (
                <TestTube className='size-4' />
              )}
              {t('Test All Channels')}
            </Button>
          )}
          <Button
            variant='outline'
            size='icon-sm'
            onClick={refresh}
            disabled={summaryQuery.isFetching}
            aria-label={t('Refresh')}
          >
            <RefreshCw
              className={`size-4 ${summaryQuery.isFetching ? 'animate-spin' : ''}`}
            />
          </Button>
        </div>
      </div>

      {(summaryQuery.isLoading ||
        detailQueries.some((query) => query.isLoading)) && (
        <div className='text-muted-foreground flex h-40 items-center justify-center gap-2'>
          <Loader2 className='size-4 animate-spin' />
          {t('Loading...')}
        </div>
      )}
      {!summaryQuery.isLoading && visibleSummaries.length === 0 && (
        <div className='text-muted-foreground flex h-40 items-center justify-center'>
          {t('No latency data available')}
        </div>
      )}
      {!summaryQuery.isLoading && visibleSummaries.length > 0 && (
        <div className='grid grid-cols-1 gap-3 xl:grid-cols-2'>
          {visibleSummaries.map((summary) => (
            <ModelMonitorCard
              key={summary.model_name}
              summary={summary}
              details={detailMap.get(summary.model_name)}
              groupRatios={groupRatios}
              usableGroups={usableGroups}
            />
          ))}
        </div>
      )}
    </div>
  )
}
