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
    ).toBe('per second: Unit price $0.7/s; 10 s × $0.7/s × 0.4x group = $2.8')
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
    ).toBe(
      'per item: Unit price $6.08/item; 1 item × $6.08/item × 1x group = $6.08'
    )
  })

  it('labels the Chinese per-second unit price explicitly', () => {
    const labels: Record<string, string> = {
      'per second': '按秒收费',
      'Unit price': '单价',
      'seconds short': '秒',
      group: '分组',
    }
    expect(
      taskBillingRule(
        {
          mode: 'tiered_expr',
          model_price: 0.7,
          group_ratio: 0.4,
          usage_facts: { seconds: 10 },
        },
        'fallback',
        (key) => labels[key] ?? key
      )
    ).toBe('按秒收费: 单价 $0.7/秒; 10 秒 × $0.7/秒 × 0.4x 分组 = $2.8')
  })

  it('falls back when historical rows have no billing snapshot', () => {
    expect(taskBillingRule(undefined, 'historical rule')).toBe(
      'historical rule'
    )
  })
})
