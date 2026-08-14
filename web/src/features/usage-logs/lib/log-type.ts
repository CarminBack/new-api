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
import { LOG_TYPES, LOG_TYPE_ENUM } from '../constants'
import type { UsageLog } from '../data/schema'
import type { LogOtherData } from '../types'

export function getUsageLogTypeConfig(log: UsageLog) {
  const config =
    LOG_TYPES.find((item) => item.value === log.type) || LOG_TYPES[0]
  if (
    log.type !== LOG_TYPE_ENUM.CONSUME ||
    log.quota !== 0 ||
    log.prompt_tokens !== 0 ||
    log.completion_tokens !== 0
  ) {
    return config
  }

  let other: LogOtherData | null = null
  try {
    other = JSON.parse(log.other) as LogOtherData
  } catch {
    // Old log rows can contain empty or malformed metadata.
  }
  const streamStatus = other?.stream_status
  if (
    streamStatus?.billing_status === 'client_gone' ||
    streamStatus?.end_reason === 'client_gone'
  ) {
    return { value: log.type, label: 'Request interrupted', color: 'warning' }
  }
  if (
    streamStatus?.billing_status === 'stream_error' ||
    streamStatus?.status === 'error'
  ) {
    return { value: log.type, label: 'Stream error', color: 'danger' }
  }
  return { value: log.type, label: 'Unbilled', color: 'neutral' }
}
