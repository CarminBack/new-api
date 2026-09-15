import { useTranslation } from 'react-i18next'

export function UpstreamTimingDetails(props: {
  timings?: Array<Record<string, number | boolean | string>>
}) {
  const { t } = useTranslation()
  if (!props.timings?.length) return null
  return (
    <section aria-label={t('Timing')} className='space-y-3'>
      <h3 className='text-sm font-medium'>{t('Timing')}</h3>
      {props.timings.map((timing, index) => (
        <div key={JSON.stringify(timing)}>
          <div className='text-sm'>{t('Request')} #{index + 1}</div>
          <dl className='space-y-1 text-xs'>
            {Object.entries(timing).map(([key, value]) => {
              let display = String(value)
              if (typeof value === 'number') display = `${value} ms`
              if (typeof value === 'boolean') display = value ? t('Yes') : t('No')
              return (
                <div key={key} className='flex flex-wrap justify-between gap-2'>
                  <dt className='font-mono'>{key}</dt>
                  <dd>{display}</dd>
                </div>
              )
            })}
          </dl>
        </div>
      ))}
    </section>
  )
}
