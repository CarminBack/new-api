import { describe, expect, it } from 'bun:test'

import { getVideoPreviewUrl } from '../../components/columns/task-logs-columns'

describe('getVideoPreviewUrl', () => {
  it('uses the signed preview URL returned by the task log API', () => {
    expect(
      getVideoPreviewUrl({
        task_id: 'task_signed',
        preview_url:
          '/api/video-previews/task_signed/content?expires=123&signature=signed',
      })
    ).toBe(
      '/api/video-previews/task_signed/content?expires=123&signature=signed'
    )
  })

  it('keeps the legacy authenticated route as a compatibility fallback', () => {
    expect(getVideoPreviewUrl({ task_id: 'task_legacy' })).toBe(
      '/v1/videos/task_legacy/content'
    )
  })
})
