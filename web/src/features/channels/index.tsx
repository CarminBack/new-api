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
import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { Settings2 } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/layout'
import { Badge } from '@/components/ui/badge'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { getChannelOps } from './api'
import { ChannelHealthPanel } from './components/channel-health-panel'
import { ChannelLatencyPanel } from './components/channel-latency-panel'
import { ChannelsDialogs } from './components/channels-dialogs'
import { ChannelsPrimaryButtons } from './components/channels-primary-buttons'
import { ChannelsProvider } from './components/channels-provider'
import { ChannelsTable } from './components/channels-table'

export function Channels() {
  const { t } = useTranslation()
  const isRoot = useAuthStore(
    (state) => state.auth.user?.role === ROLE.SUPER_ADMIN
  )
  const channelOpsQuery = useQuery({
    queryKey: ['channel-ops'],
    queryFn: getChannelOps,
    retry: false,
    staleTime: 5 * 60 * 1000,
  })
  const retryTimes = channelOpsQuery.data?.data?.retry_times
  const retryLabel =
    typeof retryTimes === 'number' ? `${t('Max Retries')}: ${retryTimes}` : null
  let retryBadge = null
  const [activeView, setActiveView] = useState<
    'channels' | 'health' | 'latency'
  >(() => {
    const saved = localStorage.getItem('channels:active-view')
    return saved === 'health' || saved === 'latency' ? saved : 'channels'
  })
  if (retryLabel) {
    retryBadge = isRoot ? (
      <Tooltip>
        <TooltipTrigger
          render={
            <Badge
              variant='outline'
              className='shrink-0 cursor-pointer'
              aria-label={t('Retry Settings')}
              render={
                <Link
                  to='/system-settings/models/$section'
                  params={{ section: 'routing-reliability' }}
                />
              }
            />
          }
        >
          <span>{retryLabel}</span>
          <Settings2 data-icon='inline-end' />
        </TooltipTrigger>
        <TooltipContent>
          <p>{t('Retry Settings')}</p>
        </TooltipContent>
      </Tooltip>
    ) : (
      <Badge variant='outline' className='shrink-0'>
        {retryLabel}
      </Badge>
    )
  }

  return (
    <ChannelsProvider>
      <Tabs
        value={activeView}
        onValueChange={(value) => {
          const nextView =
            value === 'health' || value === 'latency' ? value : 'channels'
          localStorage.setItem('channels:active-view', nextView)
          setActiveView(nextView)
        }}
        className='min-h-0 flex-1 gap-0'
      >
        <SectionPageLayout fixedContent>
          <SectionPageLayout.Title>
            <span className='flex min-w-0 items-center gap-2'>
              <span className='truncate'>{t('Channels')}</span>
              {retryBadge}
              <TabsList className='h-7 shrink-0'>
                <TabsTrigger value='channels' className='px-2 text-xs'>
                  {t('Channel List')}
                </TabsTrigger>
                <TabsTrigger value='health' className='px-2 text-xs'>
                  {t('Channel Health')}
                </TabsTrigger>
                <TabsTrigger value='latency' className='px-2 text-xs'>
                  {t('Latency')}
                </TabsTrigger>
              </TabsList>
            </span>
          </SectionPageLayout.Title>
          <SectionPageLayout.Actions>
            {activeView === 'channels' && <ChannelsPrimaryButtons />}
          </SectionPageLayout.Actions>
          <SectionPageLayout.Content>
            <TabsContent value='channels' className='h-full min-h-0'>
              <ChannelsTable />
            </TabsContent>
            <TabsContent value='health' className='h-full min-h-0'>
              <ChannelHealthPanel />
            </TabsContent>
            <TabsContent value='latency' className='h-full min-h-0'>
              <ChannelLatencyPanel />
            </TabsContent>
          </SectionPageLayout.Content>
        </SectionPageLayout>
      </Tabs>

      <ChannelsDialogs />
    </ChannelsProvider>
  )
}
