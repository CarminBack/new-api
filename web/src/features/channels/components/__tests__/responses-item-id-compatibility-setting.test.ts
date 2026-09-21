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
  CHANNEL_FORM_DEFAULT_VALUES,
  transformChannelToFormDefaults,
} from '../../lib/channel-form'
import { channelSchema } from '../../types'

function channelWithSetting(setting: Record<string, unknown>) {
  return channelSchema.parse({
    id: 1,
    name: 'Test channel',
    key: '',
    type: 1,
    status: 1,
    created_time: 0,
    test_time: 0,
    response_time: 0,
    balance_updated_time: 0,
    setting: JSON.stringify(setting),
  })
}

describe('Responses item ID compatibility channel setting', () => {
  test('defaults to disabled for new and existing channels', () => {
    expect(
      JSON.parse(buildSettingJSON(CHANNEL_FORM_DEFAULT_VALUES))
        .responses_item_id_compatibility_enabled
    ).toBe(false)

    const values = transformChannelToFormDefaults(channelWithSetting({}))
    expect(values.responses_item_id_compatibility_enabled).toBe(false)
  })

  test('loads and saves an explicitly enabled setting', () => {
    const values = transformChannelToFormDefaults(
      channelWithSetting({ responses_item_id_compatibility_enabled: true })
    )
    expect(values.responses_item_id_compatibility_enabled).toBe(true)
    expect(
      JSON.parse(buildSettingJSON(values))
        .responses_item_id_compatibility_enabled
    ).toBe(true)
  })
})
