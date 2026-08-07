import { describe, expect, it } from 'bun:test'

import {
  transformFormDataToPayload,
  transformRedemptionToFormDefaults,
} from './redemption-form'

describe('redemption form transformations', () => {
  it('creates a group-bound multi-user invitation payload without quota', () => {
    const payload = transformFormDataToPayload({
      name: 'downstream invite',
      quota_dollars: 10,
      count: 1,
      code_type: 'invitation',
      group: 'downstream',
      multi_use: true,
    })

    expect(payload).toEqual({
      name: 'downstream invite',
      quota: 0,
      expired_time: 0,
      count: 1,
      code_type: 'invitation',
      group: 'downstream',
      multi_use: true,
    })
  })

  it('preserves invitation settings when editing', () => {
    const values = transformRedemptionToFormDefaults({
      id: 1,
      user_id: 1,
      name: 'single invite',
      key: '12345678901234567890123456789012',
      status: 1,
      quota: 0,
      created_time: 1,
      redeemed_time: 0,
      expired_time: 0,
      used_user_id: 0,
      code_type: 'invitation',
      group: 'downstream',
      multi_use: false,
      use_count: 0,
    })

    expect(values.code_type).toBe('invitation')
    expect(values.group).toBe('downstream')
    expect(values.multi_use).toBe(false)
  })
})
