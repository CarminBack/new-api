import { describe, expect, it } from 'vitest'

import type { ChannelHealthItem } from '../types'
import { buildHealthRows } from './channel-health-rows'

const channelHealthItem: ChannelHealthItem = {
  channel_id: 29,
  channel_name: 'Primary',
  channel_type: 1,
  channel_status: 1,
  persistent_keys: [],
  adaptive: {
    channel_id: 29,
    channel_state: 'circuit_open',
    channel_open_until: 1_700_000_120,
    channel_next_probe_at: 1_700_000_060,
    channel_probe_in_flight: false,
    channel_failure_reason: 'upstream timeout',
    routes: [],
    keys: [],
  },
}

describe('channel health rows', () => {
  it('shows a channel-level circuit even without route snapshots', () => {
    const rows = buildHealthRows(channelHealthItem, false)

    expect(rows).toHaveLength(1)
    expect(rows[0].scope).toBe('channel')
    expect(rows[0].state).toBe('circuit_open')
    expect(rows[0].reason).toBe('upstream timeout')
    expect(rows[0].openUntil).toBe(1_700_000_060)
  })
})
