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
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

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
  test('shows a channel-level circuit even without route snapshots', () => {
    const rows = buildHealthRows(channelHealthItem, false)

    assert.equal(rows.length, 1)
    assert.equal(rows[0].scope, 'channel')
    assert.equal(rows[0].state, 'circuit_open')
    assert.equal(rows[0].reason, 'upstream timeout')
    assert.equal(rows[0].openUntil, 1_700_000_060)
  })
})
