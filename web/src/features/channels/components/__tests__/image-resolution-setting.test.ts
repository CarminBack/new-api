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
import { describe, expect, test } from 'vitest'

import {
  buildSettingJSON,
  transformChannelToFormDefaults,
} from '../../lib/channel-form'
import { channelSchema } from '../../types'

function channelWithImageTiers(imageResolutionTiers: unknown) {
  return channelSchema.parse({
    id: 1,
    name: 'Image channel',
    key: '',
    type: 1,
    status: 1,
    created_time: 0,
    test_time: 0,
    response_time: 0,
    balance_updated_time: 0,
    models: 'gpt-image-2',
    group: 'Image',
    setting: JSON.stringify({
      image_resolution_tiers: imageResolutionTiers,
    }),
  })
}

describe('image resolution capability setting', () => {
  test('loads and saves validated model tiers', () => {
    const values = transformChannelToFormDefaults(
      channelWithImageTiers({ 'gpt-image-2': ['1k', '4k'] })
    )
    expect(values.image_resolution_tiers).toEqual({
      'gpt-image-2': ['1k', '4k'],
    })
    expect(JSON.parse(buildSettingJSON(values)).image_resolution_tiers).toEqual(
      { 'gpt-image-2': ['1k', '4k'] }
    )
  })

  test('rejects malformed persisted tiers and omits empty declarations', () => {
    const values = transformChannelToFormDefaults(
      channelWithImageTiers({ 'gpt-image-2': ['8k'] })
    )
    expect(values.image_resolution_tiers).toEqual({})
    expect(
      JSON.parse(buildSettingJSON(values)).image_resolution_tiers
    ).toBeUndefined()
  })
})
