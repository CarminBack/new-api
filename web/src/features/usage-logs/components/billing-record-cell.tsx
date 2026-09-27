/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import { ReceiptText } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { StatusBadge } from '@/components/status-badge'
import { formatBillingCurrencyFromUSD } from '@/lib/currency'
import { formatLogQuota } from '@/lib/format'

import type { TaskBillingInfo } from '../types'

interface BillingRecordCellProps {
  quota: number
  billing?: TaskBillingInfo
  fallbackRule: string
}

function finitePositive(value: unknown): value is number {
  return typeof value === 'number' && Number.isFinite(value) && value > 0
}

function compactNumber(value: number): string {
  return new Intl.NumberFormat(undefined, { maximumFractionDigits: 4 }).format(
    value
  )
}

export function taskBillingRule(
  billing: TaskBillingInfo | undefined,
  fallback: string
): string {
  if (!billing) return fallback

  const parts: string[] = []
  if (finitePositive(billing.model_price)) {
    parts.push(formatBillingCurrencyFromUSD(billing.model_price))
  } else if (finitePositive(billing.model_ratio)) {
    parts.push(`${compactNumber(billing.model_ratio)}x`)
  }

  const facts = billing.usage_facts ?? {}
  if (finitePositive(facts.seconds)) {
    parts.push(`${compactNumber(facts.seconds)} s`)
  } else if (finitePositive(facts.duration)) {
    parts.push(`${compactNumber(facts.duration)} s`)
  } else if (finitePositive(facts.videos)) {
    parts.push(`${compactNumber(facts.videos)} item`)
  }

  if (parts.length < 2) {
    for (const [key, value] of Object.entries(billing.other_ratios ?? {})) {
      if (!finitePositive(value) || value === 1) continue
      parts.push(
        key === 'seconds' || key === 'duration'
          ? `${compactNumber(value)} s`
          : `${key} ${compactNumber(value)}x`
      )
    }
  }

  if (finitePositive(billing.group_ratio)) {
    parts.push(`${compactNumber(billing.group_ratio)}x group`)
  }

  return parts.length >= 2 ? parts.join(' × ') : fallback
}

export function BillingRecordCell(props: BillingRecordCellProps) {
  const { t } = useTranslation()
  const rule = taskBillingRule(props.billing, props.fallbackRule)

  return (
    <div className='flex max-w-[230px] flex-col items-start gap-1'>
      <StatusBadge
        type='badge'
        variant='neutral'
        size='lg'
        copyable={false}
        className='border-border/80 bg-muted/60 text-foreground rounded-md border font-semibold tabular-nums'
      >
        <ReceiptText className='size-3.5' aria-hidden='true' />
        <span className='whitespace-nowrap'>{formatLogQuota(props.quota)}</span>
      </StatusBadge>
      <span
        className='text-muted-foreground max-w-full truncate text-[11px]'
        title={rule}
      >
        {t(rule)}
      </span>
    </div>
  )
}
