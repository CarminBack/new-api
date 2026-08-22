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
import { useQuery } from '@tanstack/react-query'
import { Activity, Clock3, Loader2, RefreshCw, TestTube } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import {
  ADMIN_PERMISSION_ACTIONS,
  ADMIN_PERMISSION_RESOURCES,
  hasPermission,
} from '@/lib/admin-permissions'
import { useAuthStore } from '@/stores/auth-store'

import { getChannelLatency, testAllChannels } from '../api'
import { CHANNEL_STATUS_CONFIG, CHANNEL_STATUS } from '../constants'
import { formatResponseTime, getResponseTimeConfig } from '../lib'
import type { ChannelLatencyChannel, ChannelLatencyGroup } from '../types'

const EMPTY_GROUPS: ChannelLatencyGroup[] = []

function formatTestTime(timestamp: number, notTestedLabel: string) {
  if (!timestamp) return notTestedLabel
  return new Intl.DateTimeFormat(undefined, {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  }).format(new Date(timestamp * 1000))
}

function LatencyValue({
  value,
  notTestedLabel,
}: {
  value: number
  notTestedLabel: string
}) {
  const { t } = useTranslation()
  if (!value) {
    return <span className='text-muted-foreground'>{notTestedLabel}</span>
  }
  return (
    <span className='font-mono tabular-nums'>
      {formatResponseTime(value, t)}
    </span>
  )
}

function ChannelStatusBadge({ status }: { status: number }) {
  const { t } = useTranslation()
  const config =
    CHANNEL_STATUS_CONFIG[status as keyof typeof CHANNEL_STATUS_CONFIG] ??
    CHANNEL_STATUS_CONFIG[CHANNEL_STATUS.UNKNOWN]
  return (
    <StatusBadge
      label={t(config.label)}
      variant={config.variant}
      size='sm'
      copyable={false}
    />
  )
}

function ChannelLatencyTable({
  channels,
  notTestedLabel,
}: {
  channels: ChannelLatencyChannel[]
  notTestedLabel: string
}) {
  const { t } = useTranslation()
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>{t('Channel')}</TableHead>
          <TableHead>{t('Status')}</TableHead>
          <TableHead>{t('Response')}</TableHead>
          <TableHead>{t('Last Tested')}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {channels.map((channel) => (
          <TableRow key={channel.channel_id}>
            <TableCell>
              <div className='font-medium'>{channel.channel_name}</div>
              <div className='text-muted-foreground font-mono text-xs'>
                #{channel.channel_id}
              </div>
            </TableCell>
            <TableCell>
              <ChannelStatusBadge status={channel.channel_status} />
            </TableCell>
            <TableCell>
              <LatencyValue
                value={channel.response_time_ms}
                notTestedLabel={notTestedLabel}
              />
            </TableCell>
            <TableCell className='text-muted-foreground text-xs'>
              {formatTestTime(channel.test_time, notTestedLabel)}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}

export function ChannelLatencyPanel() {
  const { t } = useTranslation()
  const [selectedGroup, setSelectedGroup] = useState('all')
  const [isTesting, setIsTesting] = useState(false)
  const currentUser = useAuthStore((state) => state.auth.user)
  const canOperate = hasPermission(
    currentUser,
    ADMIN_PERMISSION_RESOURCES.CHANNEL,
    ADMIN_PERMISSION_ACTIONS.OPERATE
  )

  const latencyQuery = useQuery({
    queryKey: ['channel-latency'],
    queryFn: getChannelLatency,
    refetchInterval: 30_000,
    refetchIntervalInBackground: false,
  })

  const groups = latencyQuery.data?.data?.groups ?? EMPTY_GROUPS
  const groupOptions = useMemo(
    () => groups.map((group) => ({ value: group.group, label: group.group })),
    [groups]
  )
  const visibleGroups = useMemo(
    () =>
      selectedGroup === 'all'
        ? groups
        : groups.filter((group) => group.group === selectedGroup),
    [groups, selectedGroup]
  )
  const selectedChannels = visibleGroups.flatMap((group) => group.channels)
  const testedChannels = visibleGroups.reduce(
    (total, group) => total + group.tested_count,
    0
  )
  const totalChannels = visibleGroups.reduce(
    (total, group) => total + group.channel_count,
    0
  )
  const averageLatency =
    testedChannels > 0
      ? visibleGroups.reduce(
          (total, group) =>
            total + group.average_response_time_ms * group.tested_count,
          0
        ) / testedChannels
      : 0
  const lastTestTime = visibleGroups.reduce(
    (latest, group) => Math.max(latest, group.last_test_time),
    0
  )

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
      await latencyQuery.refetch()
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

  const notTestedLabel = t('Not tested')
  const avgConfig = getResponseTimeConfig(Math.round(averageLatency))

  return (
    <div className='flex h-full min-h-0 flex-col overflow-hidden rounded-md border'>
      <div className='flex flex-wrap items-center gap-2 border-b p-2'>
        <Select
          value={selectedGroup}
          onValueChange={(value) => setSelectedGroup(value ?? 'all')}
          items={[{ value: 'all', label: t('All Groups') }, ...groupOptions]}
        >
          <SelectTrigger className='min-w-36'>
            <SelectValue placeholder={t('All Groups')} />
          </SelectTrigger>
          <SelectContent alignItemWithTrigger={false}>
            <SelectGroup>
              <SelectItem value='all'>{t('All Groups')}</SelectItem>
              {groupOptions.map((group) => (
                <SelectItem key={group.value} value={group.value}>
                  {group.label}
                </SelectItem>
              ))}
            </SelectGroup>
          </SelectContent>
        </Select>
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
          <span className='max-sm:hidden'>{t('Test All Channels')}</span>
        </Button>
        <Button
          variant='outline'
          size='icon-sm'
          onClick={() => latencyQuery.refetch()}
          disabled={latencyQuery.isFetching}
          aria-label={t('Refresh')}
        >
          <RefreshCw
            className={`size-4 ${latencyQuery.isFetching ? 'animate-spin' : ''}`}
          />
        </Button>
      </div>

      <div className='grid grid-cols-2 border-b sm:grid-cols-4'>
        <div className='flex min-h-16 items-center gap-2 border-r border-b px-3 sm:border-b-0'>
          <Activity className='text-muted-foreground size-4' />
          <span>
            <span className='block text-lg leading-5 font-semibold tabular-nums'>
              {visibleGroups.length}
            </span>
            <span className='text-muted-foreground block text-xs'>
              {t('Groups')}
            </span>
          </span>
        </div>
        <div className='flex min-h-16 items-center gap-2 border-b px-3 sm:border-r sm:border-b-0'>
          <Clock3 className='text-muted-foreground size-4' />
          <span>
            <span className='block text-lg leading-5 font-semibold tabular-nums'>
              {testedChannels} / {totalChannels}
            </span>
            <span className='text-muted-foreground block text-xs'>
              {t('Channels')}
            </span>
          </span>
        </div>
        <div className='flex min-h-16 items-center gap-2 border-r px-3'>
          <Clock3 className='text-muted-foreground size-4' />
          <span>
            <span className='block text-lg leading-5 font-semibold tabular-nums'>
              {averageLatency ? (
                <StatusBadge
                  label={formatResponseTime(Math.round(averageLatency), t)}
                  variant={avgConfig.variant}
                  size='sm'
                  copyable={false}
                />
              ) : (
                notTestedLabel
              )}
            </span>
            <span className='text-muted-foreground block text-xs'>
              {t('Average latency')}
            </span>
          </span>
        </div>
        <div className='flex min-h-16 items-center gap-2 px-3'>
          <Clock3 className='text-muted-foreground size-4' />
          <span>
            <span className='block text-sm leading-5 font-medium'>
              {formatTestTime(lastTestTime, notTestedLabel)}
            </span>
            <span className='text-muted-foreground block text-xs'>
              {t('Last Tested')}
            </span>
          </span>
        </div>
      </div>

      <div className='min-h-0 flex-1 overflow-auto'>
        {latencyQuery.isLoading && (
          <div className='text-muted-foreground flex h-40 items-center justify-center gap-2'>
            <Loader2 className='size-4 animate-spin' />
            {t('Loading...')}
          </div>
        )}
        {!latencyQuery.isLoading && visibleGroups.length === 0 && (
          <div className='text-muted-foreground flex h-40 flex-col items-center justify-center gap-2'>
            <Clock3 className='size-6' />
            <span>{t('No latency data available')}</span>
          </div>
        )}
        {!latencyQuery.isLoading && visibleGroups.length > 0 && (
          <>
            <div className='hidden md:block'>
              <Table>
                <TableHeader className='bg-background sticky top-0 z-10'>
                  <TableRow>
                    <TableHead>{t('Group')}</TableHead>
                    <TableHead>{t('Channels')}</TableHead>
                    <TableHead>{t('Average latency')}</TableHead>
                    <TableHead>{t('Response')}</TableHead>
                    <TableHead>{t('Last Tested')}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {visibleGroups.map((group: ChannelLatencyGroup) => (
                    <TableRow key={group.group}>
                      <TableCell className='font-medium'>
                        {group.group}
                      </TableCell>
                      <TableCell className='font-mono text-xs'>
                        {group.tested_count} / {group.channel_count}
                      </TableCell>
                      <TableCell>
                        <LatencyValue
                          value={Math.round(group.average_response_time_ms)}
                          notTestedLabel={notTestedLabel}
                        />
                      </TableCell>
                      <TableCell className='font-mono text-xs'>
                        {group.min_response_time_ms || '—'} –{' '}
                        {group.max_response_time_ms || '—'} ms
                      </TableCell>
                      <TableCell className='text-muted-foreground text-xs'>
                        {formatTestTime(group.last_test_time, notTestedLabel)}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
            <div className='divide-y md:hidden'>
              {visibleGroups.map((group: ChannelLatencyGroup) => (
                <section key={group.group} className='space-y-2 p-3'>
                  <div className='flex items-center justify-between gap-2'>
                    <span className='font-medium'>{group.group}</span>
                    <span className='font-mono text-xs'>
                      {group.tested_count} / {group.channel_count}
                    </span>
                  </div>
                  <div className='grid grid-cols-3 gap-2 text-xs'>
                    <span>
                      <span className='text-muted-foreground block'>
                        {t('Average latency')}
                      </span>
                      <LatencyValue
                        value={Math.round(group.average_response_time_ms)}
                        notTestedLabel={notTestedLabel}
                      />
                    </span>
                    <span>
                      <span className='text-muted-foreground block'>
                        {t('Minimum')}
                      </span>
                      <LatencyValue
                        value={group.min_response_time_ms}
                        notTestedLabel={notTestedLabel}
                      />
                    </span>
                    <span>
                      <span className='text-muted-foreground block'>
                        {t('Maximum')}
                      </span>
                      <LatencyValue
                        value={group.max_response_time_ms}
                        notTestedLabel={notTestedLabel}
                      />
                    </span>
                  </div>
                  <ChannelLatencyTable
                    channels={group.channels}
                    notTestedLabel={notTestedLabel}
                  />
                </section>
              ))}
            </div>
            {selectedGroup !== 'all' && (
              <div className='hidden border-t md:block'>
                <ChannelLatencyTable
                  channels={selectedChannels}
                  notTestedLabel={notTestedLabel}
                />
              </div>
            )}
          </>
        )}
      </div>
    </div>
  )
}
