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

// The pricing page is scoped by model name, which hides which models a video
// channel serves once providers use unrelated names or channel mappings. This
// module only narrows the visible rows; pricing is still stored per model.
export const VIDEO_CHANNEL_GROUP = 'Video'

export type VideoChannelSource = {
  id: number
  name: string
  status: number
  group: string
  models: string
  model_mapping?: string | null
}

export type VideoChannelOption = {
  id: number
  name: string
  enabled: boolean
  // Every name a request to this channel can bill under: the channel's model
  // list (which already holds the public alias) plus each mapping target.
  modelNames: Set<string>
  // Public alias -> upstream model, only for aliases the channel exposes.
  aliasTargets: Map<string, string>
}

function splitList(value: string): string[] {
  return value
    .split(',')
    .map((item) => item.trim())
    .filter(Boolean)
}

function parseMapping(raw: string | null | undefined): Record<string, string> {
  if (!raw || !raw.trim()) return {}
  try {
    const parsed: unknown = JSON.parse(raw)
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed))
      return {}
    const result: Record<string, string> = {}
    for (const [alias, target] of Object.entries(parsed)) {
      if (typeof target === 'string' && target.trim()) {
        result[alias.trim()] = target.trim()
      }
    }
    return result
  } catch {
    // A malformed mapping must not hide the channel's own models.
    return {}
  }
}

// Mirrors the relay's chained redirect: follow the mapping to its tail, and
// treat a cycle as unresolvable rather than guessing a target.
function mappingTail(
  mapping: Record<string, string>,
  start: string
): string | null {
  let current = start
  const visited = new Set([current])
  for (;;) {
    const next = mapping[current]
    if (!next) return current
    if (visited.has(next)) return next === current ? current : null
    visited.add(next)
    current = next
  }
}

export function buildVideoChannelOptions(
  channels: VideoChannelSource[]
): VideoChannelOption[] {
  return channels
    .filter((channel) =>
      splitList(channel.group ?? '').includes(VIDEO_CHANNEL_GROUP)
    )
    .map((channel) => {
      const models = splitList(channel.models ?? '')
      const mapping = parseMapping(channel.model_mapping)
      const modelNames = new Set(models)
      const aliasTargets = new Map<string, string>()
      for (const model of models) {
        // The relay bills the alias first, then the mapping tail, so both
        // names belong to this channel. Unmapped names map to themselves.
        const target = mappingTail(mapping, model)
        if (target && target !== model) {
          aliasTargets.set(model, target)
          modelNames.add(target)
        }
      }
      return {
        id: channel.id,
        name: channel.name,
        enabled: channel.status === 1,
        modelNames,
        aliasTargets,
      }
    })
    .sort((a, b) => Number(b.enabled) - Number(a.enabled) || a.id - b.id)
}

// Models that do not have a saved price yet still belong to the channel, so
// the page can show them as unpriced rows instead of silently omitting them.
export function videoChannelMissingModels(
  option: VideoChannelOption,
  pricedNames: Iterable<string>
): string[] {
  const priced = new Set(pricedNames)
  return [...option.modelNames].filter((name) => !priced.has(name)).sort()
}
