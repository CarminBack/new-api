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

import type { ChannelHealthItem } from '../../types'
import {
  isImageHealthCircuitOpen,
  type HealthRow,
} from '../channel-health-rows'

const item = {} as ChannelHealthItem

const channelItem = (
  routes: Array<{
    model_name: string
    request_path: string
    state: 'circuit_open' | 'half_open'
  }>,
  imageGroup = false
) =>
  ({
    ...item,
    adaptive: {
      channel_id: 1,
      channel_state: 'circuit_open',
      channel_open_until: 1,
      channel_next_probe_at: 1,
      channel_probe_in_flight: false,
      image_group: imageGroup,
      routes,
      keys: [],
    },
  }) as unknown as ChannelHealthItem

function healthRow(requestPath: string, state: HealthRow['state']): HealthRow {
  return {
    id: 'test-row',
    item,
    scope: 'route',
    requestPath,
    state,
  }
}

function imageHealthRow(
  requestPath: string,
  state: HealthRow['state']
): HealthRow {
  return {
    ...healthRow(requestPath, state),
    item: {
      ...item,
      adaptive: {
        ...item.adaptive,
        image_group: true,
      },
    },
  }
}

describe('channel health actions', () => {
  test('identifies open image routes that must show only recovery', () => {
    for (const requestPath of [
      '/v1/images/generations',
      '/v1/images/edits',
      '/v1/images/variations',
    ]) {
      assert.equal(
        isImageHealthCircuitOpen(healthRow(requestPath, 'circuit_open')),
        true
      )
      assert.equal(
        isImageHealthCircuitOpen(healthRow(requestPath, 'half_open')),
        true
      )
    }
  })

  test('keeps standard and healthy image routes on normal actions', () => {
    assert.equal(
      isImageHealthCircuitOpen(
        healthRow('/v1/chat/completions', 'circuit_open')
      ),
      false
    )
    assert.equal(
      isImageHealthCircuitOpen(healthRow('/v1/images/generations', 'healthy')),
      false
    )
    assert.equal(
      isImageHealthCircuitOpen(
        imageHealthRow('/v1/chat/completions', 'circuit_open')
      ),
      true
    )
  })

  test('limits channel-level actions only when its open routes are all images', () => {
    const imageRoute = {
      model_name: 'mapped-image-model',
      request_path: '/v1/images/generations',
      state: 'circuit_open' as const,
    }
    const textRoute = {
      model_name: 'gpt-test',
      request_path: '/v1/responses',
      state: 'circuit_open' as const,
    }
    assert.equal(
      isImageHealthCircuitOpen({
        id: 'channel-image',
        item: channelItem([imageRoute]),
        scope: 'channel',
        state: 'circuit_open',
      }),
      true
    )
    assert.equal(
      isImageHealthCircuitOpen({
        id: 'channel-mixed',
        item: channelItem([imageRoute, textRoute]),
        scope: 'channel',
        state: 'circuit_open',
      }),
      false
    )
    assert.equal(
      isImageHealthCircuitOpen({
        id: 'channel-image-group',
        item: channelItem([textRoute], true),
        scope: 'channel',
        state: 'circuit_open',
      }),
      true
    )
  })
})
