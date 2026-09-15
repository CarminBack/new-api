import { describe, test } from 'node:test'
import assert from 'node:assert/strict'
import { renderToStaticMarkup } from 'react-dom/server'
import { createInstance } from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { UpstreamTimingDetails } from '../upstream-timing-details'

const i18n = createInstance()
await i18n.init({ lng: 'en', resources: { en: { translation: { Timing: 'Timing', Request: 'Request', Yes: 'Yes', No: 'No' } } } })

function render(timings?: Array<Record<string, number | boolean>>) {
  return renderToStaticMarkup(<I18nextProvider i18n={i18n}><UpstreamTimingDetails timings={timings} /></I18nextProvider>)
}

describe('upstream timing details', () => {
  test('old logs without timing render no section', () => {
    assert.equal(render(), '')
    assert.equal(render([]), '')
  })
  test('available zero durations and reused connections are shown without inventing missing phases', () => {
    const html = render([{ connection_reused: true, response_headers_ms: 0, first_sse_data_ms: 75000 }])
    assert.match(html, /75000 ms/)
    assert.match(html, /0 ms/)
    assert.match(html, /Yes/)
    assert.doesNotMatch(html, /dns_ms/)
  })
  test('separate HTTP exchanges remain separate instead of summing their first data times', () => {
    const html = render([{ first_sse_data_ms: 3000 }, { first_sse_data_ms: 10000 }])
    assert.match(html, /Request #1/)
    assert.match(html, /Request #2/)
    assert.match(html, /3000 ms/)
    assert.match(html, /10000 ms/)
    assert.doesNotMatch(html, /13000 ms/)
  })
})
