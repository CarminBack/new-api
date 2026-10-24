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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import dayjs from '@/lib/dayjs'

import type { ApiKeyUsageRange } from '../types'

type ApiKeyUsageFilterProps = {
  range?: ApiKeyUsageRange
  onApply: (range: ApiKeyUsageRange | undefined) => void
}

export function ApiKeyUsageFilter(props: ApiKeyUsageFilterProps) {
  const { t } = useTranslation()
  const [startDate, setStartDate] = useState('')
  const [endDate, setEndDate] = useState('')
  const start = dayjs(startDate).startOf('day')
  // Calendar addition (not 86400 seconds) preserves full dates across DST transitions.
  const end = dayjs(endDate).startOf('day').add(1, 'day')
  let error = ''
  if (Boolean(startDate) !== Boolean(endDate)) {
    error = t('Select both a start date and an end date.')
  } else if (startDate && endDate) {
    if (!start.isValid() || !end.isValid() || start.unix() < 0) {
      error = t('Select valid usage dates on or after 1970-01-01.')
    } else if (startDate > endDate) {
      error = t('The end date must be on or after the start date.')
    }
  }
  const timezone = Intl.DateTimeFormat().resolvedOptions().timeZone

  return (
    <div className='min-w-0 space-y-2 rounded-lg border p-3'>
      <form
        noValidate
        className='grid grid-cols-2 gap-2 sm:flex sm:flex-wrap sm:items-end'
        onSubmit={(event) => {
          event.preventDefault()
          if (error || !startDate || !endDate) return
          props.onApply({
            start_timestamp: start.unix(),
            end_timestamp: end.unix(),
          })
        }}
      >
        <div className='min-w-0 space-y-1 sm:w-48 sm:flex-none'>
          <Label htmlFor='key-usage-start'>{t('Start date')}</Label>
          <Input
            id='key-usage-start'
            className='text-sm'
            type='date'
            min='1970-01-01'
            value={startDate}
            onChange={(event) => setStartDate(event.target.value)}
            aria-invalid={Boolean(error)}
            aria-describedby={error ? 'key-usage-error' : undefined}
          />
        </div>
        <div className='min-w-0 space-y-1 sm:w-48 sm:flex-none'>
          <Label htmlFor='key-usage-end'>{t('End date')}</Label>
          <Input
            id='key-usage-end'
            className='text-sm'
            type='date'
            min={startDate || '1970-01-01'}
            value={endDate}
            onChange={(event) => setEndDate(event.target.value)}
            aria-invalid={Boolean(error)}
            aria-describedby={error ? 'key-usage-error' : undefined}
          />
        </div>
        <Button
          type='submit'
          className='w-full sm:w-auto'
          disabled={!startDate || !endDate || Boolean(error)}
        >
          {t('Query usage')}
        </Button>
        <Button
          type='button'
          className='w-full sm:w-auto'
          variant='outline'
          disabled={!props.range && !startDate && !endDate}
          onClick={() => {
            setStartDate('')
            setEndDate('')
            props.onApply(undefined)
          }}
        >
          {t('Clear dates')}
        </Button>
      </form>
      {error && (
        <p
          id='key-usage-error'
          role='alert'
          className='text-destructive text-xs'
        >
          {error}
        </p>
      )}
      <p className='text-muted-foreground text-xs' role='status'>
        {props.range
          ? t('Applied range: {{start}} to {{end}}', {
              start: dayjs
                .unix(props.range.start_timestamp)
                .format('YYYY-MM-DD'),
              end: dayjs
                .unix(props.range.end_timestamp)
                .subtract(1, 'day')
                .format('YYYY-MM-DD'),
            })
          : t('All-time usage. Select dates to view period usage.')}
      </p>
      <p className='text-muted-foreground text-xs'>
        {t(
          'Dates use {{timezone}} and include the entire end date. Period usage is consumption minus refunds, based on retained logs.',
          { timezone }
        )}
      </p>
    </div>
  )
}
