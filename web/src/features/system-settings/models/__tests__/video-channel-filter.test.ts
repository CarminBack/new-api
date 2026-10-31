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
import { describe, expect, it } from 'vitest'

import {
  buildVideoChannelOptions,
  type VideoChannelSource,
  videoChannelMissingModels,
} from '../video-channel-filter'

const channel = (
  overrides: Partial<VideoChannelSource> & Pick<VideoChannelSource, 'id'>
): VideoChannelSource => ({
  name: `channel-${overrides.id}`,
  status: 1,
  group: 'Video',
  models: '',
  model_mapping: null,
  ...overrides,
})

describe('buildVideoChannelOptions', () => {
  it('keeps only channels in the Video group, including multi-group ones', () => {
    const options = buildVideoChannelOptions([
      channel({ id: 1, group: 'Image' }),
      channel({ id: 2, group: 'default,Video' }),
      channel({ id: 3, group: 'Videos' }),
      channel({ id: 4, group: 'Video' }),
    ])
    expect(options.map((option) => option.id)).toEqual([2, 4])
  })

  it('lists enabled channels first, then by id', () => {
    const options = buildVideoChannelOptions([
      channel({ id: 9, status: 1 }),
      channel({ id: 3, status: 2 }),
      channel({ id: 5, status: 1 }),
    ])
    expect(options.map((option) => [option.id, option.enabled])).toEqual([
      [5, true],
      [9, true],
      [3, false],
    ])
  })

  it('includes both the public alias and the upstream model it bills', () => {
    const [option] = buildVideoChannelOptions([
      channel({
        id: 1,
        models: 'seedance-720p, TTP-grok',
        model_mapping: JSON.stringify({ 'seedance-720p': 'TTsedanco2.0-720' }),
      }),
    ])
    expect([...option.modelNames].sort()).toEqual([
      'TTP-grok',
      'TTsedanco2.0-720',
      'seedance-720p',
    ])
    expect([...option.aliasTargets]).toEqual([
      ['seedance-720p', 'TTsedanco2.0-720'],
    ])
  })

  it('follows chained mappings to the tail like the relay does', () => {
    const [option] = buildVideoChannelOptions([
      channel({
        id: 1,
        models: 'public',
        model_mapping: JSON.stringify({ public: 'middle', middle: 'upstream' }),
      }),
    ])
    expect(option.aliasTargets.get('public')).toBe('upstream')
    expect(option.modelNames.has('upstream')).toBe(true)
  })

  it('ignores mapping entries for names the channel does not expose', () => {
    const [option] = buildVideoChannelOptions([
      channel({
        id: 1,
        models: 'a',
        model_mapping: JSON.stringify({ hidden: 'secret-upstream' }),
      }),
    ])
    expect([...option.modelNames]).toEqual(['a'])
  })

  it('drops cyclic mappings instead of guessing a target', () => {
    const [option] = buildVideoChannelOptions([
      channel({
        id: 1,
        models: 'a',
        model_mapping: JSON.stringify({ a: 'b', b: 'a' }),
      }),
    ])
    expect([...option.modelNames]).toEqual(['a'])
    expect(option.aliasTargets.size).toBe(0)
  })

  it('keeps the channel models when the mapping is malformed', () => {
    const [option] = buildVideoChannelOptions([
      channel({ id: 1, models: 'a,b', model_mapping: '{not json' }),
    ])
    expect([...option.modelNames]).toEqual(['a', 'b'])
  })

  it('treats a self-mapping as no mapping', () => {
    const [option] = buildVideoChannelOptions([
      channel({
        id: 1,
        models: 'a',
        model_mapping: JSON.stringify({ a: 'a' }),
      }),
    ])
    expect(option.aliasTargets.size).toBe(0)
  })
})

describe('videoChannelMissingModels', () => {
  it('returns channel models that have no saved pricing row', () => {
    const [option] = buildVideoChannelOptions([
      channel({ id: 1, models: 'b,a,c' }),
    ])
    expect(videoChannelMissingModels(option, ['a', 'other'])).toEqual([
      'b',
      'c',
    ])
  })
})
