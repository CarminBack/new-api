import { createInstance } from 'i18next'
import { renderToStaticMarkup } from 'react-dom/server'
import { I18nextProvider } from 'react-i18next'
import { expect, test } from 'vitest'

import { UpstreamTimingDetails } from '../upstream-timing-details'

const i18n = createInstance()
await i18n.init({
  lng: 'en',
  resources: {
    en: {
      translation: {
        Timing: 'Timing',
        Request: 'Request',
        Yes: 'Yes',
        No: 'No',
      },
    },
  },
})

function render(timings?: Array<Record<string, number | boolean | string>>) {
  return renderToStaticMarkup(
    <I18nextProvider i18n={i18n}>
      <UpstreamTimingDetails timings={timings} />
    </I18nextProvider>
  )
}

test('upstream timing details hide missing data', () => {
  expect(render()).toBe('')
  expect(render([])).toBe('')
})

test('upstream timing details show zero durations and reused connections', () => {
  const html = render([
    {
      connection_reused: true,
      response_headers_ms: 0,
      first_sse_data_ms: 75000,
      response_header_server_timing: 'queue;dur=31200',
    },
  ])
  expect(html).toMatch(/75000 ms/)
  expect(html).toMatch(/0 ms/)
  expect(html).toMatch(/queue;dur=31200/)
  expect(html).not.toMatch(/queue;dur=31200 ms/)
  expect(html).toMatch(/Yes/)
  expect(html).not.toMatch(/dns_ms/)
})

test('upstream timing details keep HTTP exchanges separate', () => {
  const html = render([
    { first_sse_data_ms: 3000 },
    { first_sse_data_ms: 10000 },
  ])
  expect(html).toMatch(/Request #1/)
  expect(html).toMatch(/Request #2/)
  expect(html).toMatch(/3000 ms/)
  expect(html).toMatch(/10000 ms/)
  expect(html).not.toMatch(/13000 ms/)
})
