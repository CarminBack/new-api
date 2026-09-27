/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import { describe, expect, it } from 'vitest'

import { taskBillingRule } from '../billing-record-cell'

describe('taskBillingRule', () => {
  it('shows the settled per-second calculation', () => {
    expect(
      taskBillingRule(
        {
          mode: 'tiered_expr',
          model_price: 0.7,
          group_ratio: 0.4,
          usage_facts: { seconds: 10, videos: 1 },
        },
        'fallback'
      )
    ).toBe('$0.7 × 10 s × 0.4x group')
  })

  it('shows fixed-item task calculations', () => {
    expect(
      taskBillingRule(
        {
          mode: 'tiered_expr',
          model_price: 6.08,
          group_ratio: 1,
          usage_facts: { videos: 1 },
        },
        'fallback'
      )
    ).toBe('$6.08 × 1 item × 1x group')
  })

  it('falls back when historical rows have no billing snapshot', () => {
    expect(taskBillingRule(undefined, 'historical rule')).toBe(
      'historical rule'
    )
  })
})
