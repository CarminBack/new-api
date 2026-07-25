import { useQuery } from '@tanstack/react-query'
import {
  Activity,
  AlertTriangle,
  Gauge,
  KeyRound,
  Loader2,
  RefreshCw,
  RotateCcw,
  ShieldCheck,
} from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { StatusBadge, type StatusVariant } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import {
  ADMIN_PERMISSION_ACTIONS,
  ADMIN_PERMISSION_RESOURCES,
  hasPermission,
} from '@/lib/admin-permissions'
import { useAuthStore } from '@/stores/auth-store'

import {
  enableMultiKey,
  getChannelHealth,
  recoverChannelHealth,
  testChannel,
  updateChannelStatus,
} from '../api'
import { CHANNEL_STATUS } from '../constants'
import type {
  ChannelHealthItem,
  ChannelHealthRecoverParams,
  ChannelHealthResponse,
  ChannelHealthState,
} from '../types'

type HealthDisplayState =
  | ChannelHealthState
  | 'auto_disabled'
  | 'manual_disabled'
  | 'key_auto_disabled'
  | 'key_manual_disabled'

type HealthRow = {
  id: string
  item: ChannelHealthItem
  scope: 'channel' | 'route' | 'key'
  state: HealthDisplayState
  modelName?: string
  requestPath?: string
  keyIndex?: number
  reason?: string
  statusCode?: number
  openUntil?: number
  lastChanged?: number
  successes?: number
  failures?: number
  poolFailures?: number
  rateLimits?: number
  inFlight?: number
  capacity?: number
  initialCapacity?: number
  persistent?: boolean
}

const stateConfig: Record<
  HealthDisplayState,
  { label: string; variant: StatusVariant }
> = {
  healthy: { label: 'Healthy', variant: 'success' },
  circuit_open: { label: 'Circuit Open', variant: 'danger' },
  half_open: { label: 'Half-open Probe', variant: 'warning' },
  degraded: { label: 'Reduced Capacity', variant: 'warning' },
  recovering: { label: 'Recovering', variant: 'info' },
  saturated: { label: 'At Capacity', variant: 'purple' },
  isolated: { label: 'Temporarily Isolated', variant: 'danger' },
  auto_disabled: { label: 'Auto Disabled', variant: 'danger' },
  manual_disabled: { label: 'Manually Disabled', variant: 'neutral' },
  key_auto_disabled: { label: 'Key Auto Disabled', variant: 'danger' },
  key_manual_disabled: { label: 'Key Manually Disabled', variant: 'neutral' },
}

const endpointTypeByPath: Record<string, string> = {
  '/v1/chat/completions': 'openai',
  '/v1/responses': 'openai-response',
  '/v1/responses/compact': 'openai-response-compact',
  '/v1/messages': 'anthropic',
  '/v1/embeddings': 'embeddings',
  '/v1/rerank': 'jina-rerank',
  '/v1/images/generations': 'image-generation',
}

function routeLastChanged(
  route: ChannelHealthItem['adaptive']['routes'][number]
) {
  return Math.max(
    route.last_failure_at ?? 0,
    route.last_success_at ?? 0,
    route.last_recovery_at ?? 0,
    route.last_touched ?? 0
  )
}

function buildHealthRows(item: ChannelHealthItem, includeHealthy: boolean) {
  const rows: HealthRow[] = []
  if (item.channel_status === CHANNEL_STATUS.AUTO_DISABLED) {
    rows.push({
      id: `${item.channel_id}:persistent-channel`,
      item,
      scope: 'channel',
      state: 'auto_disabled',
      reason: item.status_reason,
      lastChanged: item.status_time,
      persistent: true,
    })
  } else if (
    includeHealthy &&
    item.channel_status === CHANNEL_STATUS.MANUAL_DISABLED
  ) {
    rows.push({
      id: `${item.channel_id}:manual-channel`,
      item,
      scope: 'channel',
      state: 'manual_disabled',
      reason: item.status_reason,
      lastChanged: item.status_time,
      persistent: true,
    })
  }

  if (
    item.adaptive.channel_state &&
    item.adaptive.channel_state !== 'healthy'
  ) {
    rows.push({
      id: `${item.channel_id}:adaptive-channel`,
      item,
      scope: 'channel',
      state: item.adaptive.channel_state,
      openUntil: item.adaptive.channel_open_until,
    })
  }

  for (const route of item.adaptive.routes ?? []) {
    rows.push({
      id: `${item.channel_id}:route:${route.model_name}:${route.request_path}`,
      item,
      scope: 'route',
      state: route.state,
      modelName: route.model_name,
      requestPath: route.request_path,
      reason: route.last_failure_reason || route.last_failure_class,
      statusCode: route.last_failure_status_code,
      openUntil: route.open_until,
      lastChanged: routeLastChanged(route),
      successes: route.successes,
      failures: route.failures,
      poolFailures: route.pool_failures,
      rateLimits: route.rate_limits,
      inFlight: route.in_flight,
      capacity: route.capacity,
      initialCapacity: route.initial_capacity,
    })
  }

  for (const key of item.adaptive.keys ?? []) {
    rows.push({
      id: `${item.channel_id}:adaptive-key:${key.key_index}:${key.scope}:${key.model_name ?? ''}`,
      item,
      scope: 'key',
      state: key.state,
      modelName: key.model_name,
      requestPath: key.request_path,
      keyIndex: key.key_index,
      openUntil: key.open_until,
      lastChanged: key.last_touched,
      inFlight: key.in_flight,
      capacity: key.capacity,
    })
  }

  for (const key of item.persistent_keys ?? []) {
    rows.push({
      id: `${item.channel_id}:persistent-key:${key.key_index}`,
      item,
      scope: 'key',
      state:
        key.status === CHANNEL_STATUS.AUTO_DISABLED
          ? 'key_auto_disabled'
          : 'key_manual_disabled',
      keyIndex: key.key_index,
      reason: key.reason,
      lastChanged: key.disabled_time,
      persistent: true,
    })
  }

  if (includeHealthy && rows.length === 0) {
    rows.push({
      id: `${item.channel_id}:healthy`,
      item,
      scope: 'channel',
      state: 'healthy',
    })
  }
  return rows
}

function formatDuration(seconds: number) {
  if (seconds <= 0) return '0s'
  const minutes = Math.floor(seconds / 60)
  const remaining = seconds % 60
  return minutes > 0 ? `${minutes}m ${remaining}s` : `${remaining}s`
}

function formatTime(timestamp?: number) {
  if (!timestamp) return '—'
  return new Intl.DateTimeFormat(undefined, {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  }).format(new Date(timestamp * 1000))
}

function recoveryPayload(row: HealthRow): ChannelHealthRecoverParams {
  if (row.scope === 'route') {
    return {
      scope: 'route',
      model_name: row.modelName,
      request_path: row.requestPath,
    }
  }
  if (row.scope === 'key') {
    return { scope: 'key', key_index: row.keyIndex }
  }
  return { scope: 'channel' }
}

function HealthStateBadge({ state }: { state: HealthDisplayState }) {
  const { t } = useTranslation()
  const config = stateConfig[state]
  return (
    <StatusBadge
      label={t(config.label)}
      variant={config.variant}
      copyable={false}
      size='sm'
    />
  )
}

function ScopeLabel({ row }: { row: HealthRow }) {
  const { t } = useTranslation()
  if (row.scope === 'route') {
    return (
      <div className='min-w-0 space-y-0.5'>
        <div className='truncate font-mono text-xs'>{row.modelName}</div>
        <div className='text-muted-foreground truncate font-mono text-xs'>
          {row.requestPath}
        </div>
      </div>
    )
  }
  if (row.scope === 'key') {
    return (
      <div className='flex min-w-0 items-center gap-1.5'>
        <KeyRound className='text-muted-foreground size-3.5' />
        <span>{t('Key #{{index}}', { index: (row.keyIndex ?? 0) + 1 })}</span>
        {row.modelName && (
          <span className='text-muted-foreground truncate font-mono text-xs'>
            {row.modelName}
          </span>
        )}
      </div>
    )
  }
  return <span>{t('Whole Channel')}</span>
}

type HealthRowActionsProps = {
  row: HealthRow
  canOperate: boolean
  actionKey: string | null
  onTest: (row: HealthRow, recoverAfter: boolean) => void
  onRecover: (row: HealthRow) => void
}

function HealthRowActions(props: HealthRowActionsProps) {
  const { t } = useTranslation()
  const loading = props.actionKey === props.row.id
  const busy = props.actionKey !== null
  const canTest = props.row.scope !== 'key'
  const canRecover =
    props.row.state !== 'healthy' && props.row.state !== 'manual_disabled'
  const testRecoverLabel =
    props.row.state === 'auto_disabled' ? 'Test and Enable' : 'Test and Recover'

  return (
    <div className='flex items-center justify-end gap-1'>
      {canTest && (
        <Tooltip>
          <TooltipTrigger
            render={
              <Button
                variant='ghost'
                size='icon-sm'
                disabled={busy || !props.canOperate}
                onClick={() => props.onTest(props.row, false)}
                aria-label={t('Test')}
              />
            }
          >
            {loading ? (
              <Loader2 className='size-4 animate-spin' />
            ) : (
              <Gauge className='size-4' />
            )}
          </TooltipTrigger>
          <TooltipContent>{t('Test')}</TooltipContent>
        </Tooltip>
      )}

      {canTest && canRecover && (
        <Tooltip>
          <TooltipTrigger
            render={
              <Button
                variant='ghost'
                size='icon-sm'
                disabled={busy || !props.canOperate}
                onClick={() => props.onTest(props.row, true)}
                aria-label={t(testRecoverLabel)}
              />
            }
          >
            <ShieldCheck className='size-4' />
          </TooltipTrigger>
          <TooltipContent>{t(testRecoverLabel)}</TooltipContent>
        </Tooltip>
      )}

      {canRecover && props.row.state !== 'auto_disabled' && (
        <Tooltip>
          <TooltipTrigger
            render={
              <Button
                variant='ghost'
                size='icon-sm'
                disabled={busy || !props.canOperate}
                onClick={() => props.onRecover(props.row)}
                aria-label={t('Recover')}
              />
            }
          >
            <RotateCcw className='size-4' />
          </TooltipTrigger>
          <TooltipContent>
            {props.row.persistent ? t('Enable') : t('Recover')}
          </TooltipContent>
        </Tooltip>
      )}
    </div>
  )
}

export function ChannelHealthPanel() {
  const { t } = useTranslation()
  const [includeHealthy, setIncludeHealthy] = useState(false)
  const [search, setSearch] = useState('')
  const [stateFilter, setStateFilter] = useState<HealthDisplayState | 'all'>(
    'all'
  )
  const [actionKey, setActionKey] = useState<string | null>(null)
  const [pendingRecovery, setPendingRecovery] = useState<HealthRow | null>(null)
  const currentUser = useAuthStore((state) => state.auth.user)
  const canOperate = hasPermission(
    currentUser,
    ADMIN_PERMISSION_RESOURCES.CHANNEL,
    ADMIN_PERMISSION_ACTIONS.OPERATE
  )

  const healthQuery = useQuery({
    queryKey: ['channel-health', includeHealthy],
    queryFn: () => getChannelHealth(includeHealthy),
    refetchInterval: 10_000,
    refetchIntervalInBackground: false,
  })

  const data: ChannelHealthResponse['data'] = healthQuery.data?.data
  const groups = useMemo(() => {
    const keyword = search.trim().toLowerCase()
    return (data?.items ?? [])
      .map((item) => {
        const rows = buildHealthRows(item, includeHealthy).filter((row) => {
          if (stateFilter !== 'all' && row.state !== stateFilter) return false
          if (!keyword) return true
          return [
            item.channel_id,
            item.channel_name,
            row.modelName,
            row.requestPath,
            row.reason,
          ]
            .filter(Boolean)
            .some((value) => String(value).toLowerCase().includes(keyword))
        })
        return { item, rows }
      })
      .filter((group) => group.rows.length > 0)
  }, [data?.items, includeHealthy, search, stateFilter])

  const refresh = async () => {
    await healthQuery.refetch()
  }

  const performRecovery = async (row: HealthRow) => {
    setActionKey(row.id)
    try {
      let response: { success: boolean; message?: string }
      if (row.persistent && row.scope === 'key') {
        response = await enableMultiKey(row.item.channel_id, row.keyIndex ?? -1)
        if (response.success) {
          await recoverChannelHealth(row.item.channel_id, recoveryPayload(row))
        }
      } else {
        response = await recoverChannelHealth(
          row.item.channel_id,
          recoveryPayload(row)
        )
      }
      if (!response.success) {
        throw new Error(response.message || t('Recovery failed'))
      }
      toast.success(t('Recovery started'))
      await refresh()
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('Recovery failed'))
    } finally {
      setActionKey(null)
      setPendingRecovery(null)
    }
  }

  const performTest = async (row: HealthRow, recoverAfter: boolean) => {
    setActionKey(row.id)
    try {
      const model = row.modelName || row.item.test_model
      const endpointType = row.requestPath
        ? endpointTypeByPath[row.requestPath]
        : undefined
      const testResponse = await testChannel(row.item.channel_id, {
        model: model || undefined,
        endpoint_type: endpointType,
      })
      if (!testResponse.success) {
        throw new Error(testResponse.message || t('Channel test failed'))
      }
      if (!recoverAfter) {
        toast.success(t('Channel test succeeded'))
        return
      }

      if (row.state === 'auto_disabled') {
        const enableResponse = await updateChannelStatus(
          row.item.channel_id,
          CHANNEL_STATUS.ENABLED
        )
        if (!enableResponse.success) {
          throw new Error(
            enableResponse.message || t('Failed to enable channel')
          )
        }
      }
      const recoverResponse = await recoverChannelHealth(
        row.item.channel_id,
        recoveryPayload(row)
      )
      if (!recoverResponse.success) {
        throw new Error(recoverResponse.message || t('Recovery failed'))
      }
      toast.success(t('Test succeeded and recovery started'))
      await refresh()
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : t('Channel test failed')
      )
    } finally {
      setActionKey(null)
    }
  }

  const summaryItems: Array<{
    label: string
    value: number
    state: HealthDisplayState
    icon: typeof Activity
  }> = [
    {
      label: 'Auto Disabled',
      value: data?.summary.auto_disabled_channels ?? 0,
      state: 'auto_disabled',
      icon: AlertTriangle,
    },
    {
      label: 'Circuit Open',
      value: data?.summary.circuit_open_routes ?? 0,
      state: 'circuit_open',
      icon: Activity,
    },
    {
      label: 'Reduced Capacity',
      value: data?.summary.degraded_routes ?? 0,
      state: 'degraded',
      icon: Gauge,
    },
    {
      label: 'Recovering',
      value: data?.summary.recovering_routes ?? 0,
      state: 'recovering',
      icon: ShieldCheck,
    },
    {
      label: 'At Capacity',
      value: data?.summary.saturated_routes ?? 0,
      state: 'saturated',
      icon: Gauge,
    },
    {
      label: 'Isolated Keys',
      value: data?.summary.isolated_keys ?? 0,
      state: 'isolated',
      icon: KeyRound,
    },
  ]

  return (
    <div className='flex h-full min-h-0 flex-col overflow-hidden rounded-md border'>
      <div className='flex flex-wrap items-center gap-2 border-b p-2'>
        <Input
          value={search}
          onChange={(event) => setSearch(event.target.value)}
          placeholder={t('Search channel, model, path, or reason')}
          className='h-8 min-w-48 flex-1 sm:max-w-sm'
        />
        <div className='flex items-center gap-2 px-1'>
          <Label htmlFor='channel-health-show-healthy' className='text-xs'>
            {t('Show Healthy')}
          </Label>
          <Switch
            id='channel-health-show-healthy'
            checked={includeHealthy}
            onCheckedChange={setIncludeHealthy}
          />
        </div>
        <Button
          variant='outline'
          size='icon-sm'
          onClick={refresh}
          disabled={healthQuery.isFetching}
          aria-label={t('Refresh')}
        >
          <RefreshCw
            className={`size-4 ${healthQuery.isFetching ? 'animate-spin' : ''}`}
          />
        </Button>
      </div>

      <div className='grid grid-cols-2 border-b sm:grid-cols-3 lg:grid-cols-6'>
        {summaryItems.map((item) => {
          const Icon = item.icon
          const active = stateFilter === item.state
          return (
            <button
              key={item.label}
              type='button'
              onClick={() => setStateFilter(active ? 'all' : item.state)}
              aria-pressed={active}
              className='hover:bg-muted/50 focus-visible:ring-ring flex min-h-14 items-center gap-2 border-r border-b px-3 text-left transition-colors focus-visible:ring-2 focus-visible:outline-none sm:border-b-0'
            >
              <Icon className='text-muted-foreground size-4 shrink-0' />
              <span className='min-w-0'>
                <span className='block text-lg leading-5 font-semibold tabular-nums'>
                  {item.value}
                </span>
                <span className='text-muted-foreground block truncate text-xs'>
                  {t(item.label)}
                </span>
              </span>
            </button>
          )
        })}
      </div>

      <div className='min-h-0 flex-1 overflow-auto'>
        {healthQuery.isLoading && (
          <div className='text-muted-foreground flex h-40 items-center justify-center gap-2'>
            <Loader2 className='size-4 animate-spin' />
            {t('Loading...')}
          </div>
        )}
        {!healthQuery.isLoading && groups.length === 0 && (
          <div className='text-muted-foreground flex h-40 flex-col items-center justify-center gap-2'>
            <ShieldCheck className='size-6' />
            <span>{t('No matching channel health issues')}</span>
          </div>
        )}
        {!healthQuery.isLoading && groups.length > 0 && (
          <>
            <div className='hidden md:block'>
              <Table>
                <TableHeader className='bg-background sticky top-0 z-10'>
                  <TableRow>
                    <TableHead>{t('Channel')}</TableHead>
                    <TableHead>{t('Scope')}</TableHead>
                    <TableHead>{t('State')}</TableHead>
                    <TableHead>{t('Reason')}</TableHead>
                    <TableHead>{t('2-minute Window')}</TableHead>
                    <TableHead>{t('In-flight / Capacity')}</TableHead>
                    <TableHead>{t('Recovery')}</TableHead>
                    <TableHead className='text-right'>{t('Actions')}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {groups.map((group) =>
                    group.rows.map((row, index) => (
                      <TableRow key={row.id}>
                        {index === 0 && (
                          <TableCell
                            rowSpan={group.rows.length}
                            className='min-w-40 border-r align-top'
                          >
                            <div className='font-medium'>
                              {group.item.channel_name}
                            </div>
                            <div className='text-muted-foreground font-mono text-xs'>
                              #{group.item.channel_id}
                            </div>
                          </TableCell>
                        )}
                        <TableCell className='max-w-60'>
                          <ScopeLabel row={row} />
                        </TableCell>
                        <TableCell>
                          <HealthStateBadge state={row.state} />
                        </TableCell>
                        <TableCell className='max-w-64 whitespace-normal'>
                          <div className='line-clamp-2'>
                            {row.statusCode ? `${row.statusCode} · ` : ''}
                            {row.reason || '—'}
                          </div>
                          <div className='text-muted-foreground text-xs'>
                            {formatTime(row.lastChanged)}
                          </div>
                        </TableCell>
                        <TableCell className='font-mono text-xs'>
                          {row.successes === undefined
                            ? '—'
                            : `S ${row.successes} / F ${row.failures} / P ${row.poolFailures} / 429 ${row.rateLimits}`}
                        </TableCell>
                        <TableCell className='font-mono text-xs'>
                          {row.capacity === undefined
                            ? '—'
                            : `${row.inFlight ?? 0} / ${row.capacity}${row.initialCapacity ? ` (${row.initialCapacity})` : ''}`}
                        </TableCell>
                        <TableCell>
                          {row.openUntil && row.openUntil > Date.now() / 1000
                            ? formatDuration(
                                Math.ceil(row.openUntil - Date.now() / 1000)
                              )
                            : '—'}
                        </TableCell>
                        <TableCell>
                          <HealthRowActions
                            row={row}
                            canOperate={canOperate}
                            actionKey={actionKey}
                            onTest={performTest}
                            onRecover={setPendingRecovery}
                          />
                        </TableCell>
                      </TableRow>
                    ))
                  )}
                </TableBody>
              </Table>
            </div>

            <div className='divide-y md:hidden'>
              {groups.map((group) => (
                <section key={group.item.channel_id} className='p-3'>
                  <div className='mb-2 flex items-center justify-between gap-2'>
                    <div className='min-w-0'>
                      <div className='truncate font-medium'>
                        {group.item.channel_name}
                      </div>
                      <div className='text-muted-foreground font-mono text-xs'>
                        #{group.item.channel_id}
                      </div>
                    </div>
                  </div>
                  <div className='divide-y rounded-md border'>
                    {group.rows.map((row) => (
                      <div key={row.id} className='space-y-2 p-2.5'>
                        <div className='flex items-start justify-between gap-2'>
                          <ScopeLabel row={row} />
                          <HealthStateBadge state={row.state} />
                        </div>
                        {row.reason && (
                          <div className='text-muted-foreground text-xs'>
                            {row.statusCode ? `${row.statusCode} · ` : ''}
                            {row.reason}
                          </div>
                        )}
                        <div className='text-muted-foreground grid grid-cols-2 gap-2 text-xs'>
                          <span>{formatTime(row.lastChanged)}</span>
                          <span className='text-right font-mono'>
                            {row.capacity === undefined
                              ? ''
                              : `${row.inFlight ?? 0} / ${row.capacity}`}
                          </span>
                        </div>
                        <HealthRowActions
                          row={row}
                          canOperate={canOperate}
                          actionKey={actionKey}
                          onTest={performTest}
                          onRecover={setPendingRecovery}
                        />
                      </div>
                    ))}
                  </div>
                </section>
              ))}
            </div>
          </>
        )}
      </div>

      <ConfirmDialog
        open={pendingRecovery !== null}
        onOpenChange={(open) => !open && setPendingRecovery(null)}
        title={t('Confirm Recovery')}
        desc={t(
          'This clears only the selected local health state and resumes with slow-start capacity. Upstream account restrictions are not removed.'
        )}
        confirmText={t('Recover')}
        handleConfirm={() => {
          if (pendingRecovery) void performRecovery(pendingRecovery)
        }}
      />
    </div>
  )
}
