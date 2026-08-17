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
import { CHANNEL_STATUS } from '../constants'
import type { ChannelHealthItem, ChannelHealthState } from '../types'

export type HealthDisplayState =
  | ChannelHealthState
  | 'auto_disabled'
  | 'manual_disabled'
  | 'key_auto_disabled'
  | 'key_manual_disabled'

export type HealthRow = {
  id: string
  item: ChannelHealthItem
  scope: 'channel' | 'route' | 'key'
  state: HealthDisplayState
  modelName?: string
  requestPath?: string
  reason?: string
  statusCode?: number
  openUntil?: number
  lastChanged?: number
  recoverySuccesses?: number
  recoverySuccessTarget?: number
  persistent?: boolean
}

const imageHealthPaths = new Set([
  '/v1/images/generations',
  '/v1/images/edits',
  '/v1/images/variations',
])
const openHealthStates = new Set(['circuit_open', 'half_open'])

export function isImageHealthCircuitOpen(row: HealthRow): boolean {
  if (row.scope === 'route') {
    return (
      imageHealthPaths.has(row.requestPath ?? '') &&
      openHealthStates.has(row.state)
    )
  }
  if (row.scope !== 'channel') return false

  const openRoutes = (row.item.adaptive?.routes ?? []).filter((route) =>
    openHealthStates.has(route.state)
  )
  return (
    openRoutes.length > 0 &&
    openRoutes.every((route) => imageHealthPaths.has(route.request_path))
  )
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

export function buildHealthRows(
  item: ChannelHealthItem,
  includeHealthy: boolean
): HealthRow[] {
  let state: HealthDisplayState | undefined
  let route: ChannelHealthItem['adaptive']['routes'][number] | undefined
  let persistent = false

  if (item.channel_status === CHANNEL_STATUS.AUTO_DISABLED) {
    state = 'auto_disabled'
    persistent = true
  } else if (
    includeHealthy &&
    item.channel_status === CHANNEL_STATUS.MANUAL_DISABLED
  ) {
    state = 'manual_disabled'
    persistent = true
  } else if (
    item.adaptive?.channel_state === 'circuit_open' ||
    item.adaptive?.channel_state === 'half_open'
  ) {
    state = item.adaptive.channel_state
  } else {
    const adaptiveRoutes = item.adaptive?.routes ?? []
    const openRoutes = adaptiveRoutes.filter((candidate) =>
      ['circuit_open', 'half_open'].includes(candidate.state)
    )
    const recoveringRoutes = adaptiveRoutes.filter(
      (candidate) => candidate.state === 'recovering'
    )
    if (openRoutes.length > 0) {
      route =
        openRoutes.find((candidate) => candidate.state === 'half_open') ??
        openRoutes[0]
      state = route.state
    } else if (recoveringRoutes.length > 0) {
      route = recoveringRoutes[0]
      state = 'recovering'
    } else if (includeHealthy) {
      state = 'healthy'
    }
  }

  if (!state) return []

  let reason = item.status_reason
  let openUntil: number | undefined
  if (route) {
    reason =
      route.last_failure_reason ||
      route.last_failure_class ||
      item.status_reason
    openUntil = route.next_probe_at || route.open_until
  } else if (!persistent) {
    reason = item.adaptive?.channel_failure_reason || item.status_reason
    openUntil =
      item.adaptive?.channel_next_probe_at || item.adaptive?.channel_open_until
  }

  return [
    {
      id: `${item.channel_id}:${state}`,
      item,
      scope: route ? 'route' : 'channel',
      state,
      modelName: route?.model_name,
      requestPath: route?.request_path,
      reason,
      statusCode: route?.last_failure_status_code,
      openUntil,
      lastChanged: route ? routeLastChanged(route) : item.status_time,
      recoverySuccesses: route?.recovery_successes,
      recoverySuccessTarget: route?.recovery_success_target,
      persistent,
    },
  ]
}
