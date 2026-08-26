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

import { getCacheWriteTokens, hasAnyCacheTokens } from '../format'

describe('cache write token normalization', () => {
  test('reads the normalized cache creation field', () => {
    assert.equal(getCacheWriteTokens({ cache_creation_tokens: 120 }), 120)
  })

  test('reads the OpenAI cache write field', () => {
    assert.equal(getCacheWriteTokens({ cache_write_tokens: 120 }), 120)
  })

  test('does not double count matching normalized and OpenAI fields', () => {
    assert.equal(
      getCacheWriteTokens({
        cache_creation_tokens: 120,
        cache_write_tokens: 120,
      }),
      120
    )
  })

  test('uses the larger total when normalized and OpenAI fields differ', () => {
    assert.equal(
      getCacheWriteTokens({
        cache_creation_tokens: 80,
        cache_write_tokens: 120,
      }),
      120
    )
  })

  test('prefers the sum of split cache write fields', () => {
    assert.equal(
      getCacheWriteTokens({
        cache_creation_tokens: 500,
        cache_write_tokens: 500,
        cache_creation_tokens_5m: 120,
        cache_creation_tokens_1h: 80,
      }),
      200
    )
  })

  test('does not treat cache reads as cache writes', () => {
    const other = { cache_tokens: 120 }

    assert.equal(getCacheWriteTokens(other), 0)
    assert.equal(hasAnyCacheTokens(other), true)
  })
})
