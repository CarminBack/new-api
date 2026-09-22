import { describe, expect, it } from 'vitest'

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
  it('identifies open image routes that must show only recovery', () => {
    for (const requestPath of [
      '/v1/images/generations',
      '/v1/images/edits',
      '/v1/images/variations',
    ]) {
      expect(
        isImageHealthCircuitOpen(healthRow(requestPath, 'circuit_open'))
      ).toBe(true)
      expect(
        isImageHealthCircuitOpen(healthRow(requestPath, 'half_open'))
      ).toBe(true)
    }
  })

  it('keeps standard and healthy image routes on normal actions', () => {
    expect(
      isImageHealthCircuitOpen(
        healthRow('/v1/chat/completions', 'circuit_open')
      )
    ).toBe(false)
    expect(
      isImageHealthCircuitOpen(healthRow('/v1/images/generations', 'healthy'))
    ).toBe(false)
    expect(
      isImageHealthCircuitOpen(
        imageHealthRow('/v1/chat/completions', 'circuit_open')
      )
    ).toBe(true)
  })

  it('limits channel-level actions only when its open routes are all images', () => {
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
    expect(
      isImageHealthCircuitOpen({
        id: 'channel-image',
        item: channelItem([imageRoute]),
        scope: 'channel',
        state: 'circuit_open',
      })
    ).toBe(true)
    expect(
      isImageHealthCircuitOpen({
        id: 'channel-mixed',
        item: channelItem([imageRoute, textRoute]),
        scope: 'channel',
        state: 'circuit_open',
      })
    ).toBe(false)
    expect(
      isImageHealthCircuitOpen({
        id: 'channel-image-group',
        item: channelItem([textRoute], true),
        scope: 'channel',
        state: 'circuit_open',
      })
    ).toBe(true)
  })
})
