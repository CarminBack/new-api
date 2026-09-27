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

function formatAmount(value: number): string {
  return formatBillingCurrencyFromUSD(value)
}

function usageUnit(facts: Record<string, unknown>): {
  label: string
  key: 'seconds' | 'videos'
  value: number
} | null {
  if (finitePositive(facts.seconds)) {
    return { label: 'per second', key: 'seconds', value: facts.seconds }
  }
  if (finitePositive(facts.duration)) {
    return { label: 'per second', key: 'seconds', value: facts.duration }
  }
  if (finitePositive(facts.videos)) {
    return { label: 'per item', key: 'videos', value: facts.videos }
  }
  return null
}

function defaultBillingTranslation(key: string): string {
  return key === 'seconds short' ? 's' : key
}

export function taskBillingRule(
  billing: TaskBillingInfo | undefined,
  fallback: string,
  translate: (key: string) => string = defaultBillingTranslation
): string {
  if (!billing) return fallback

  const facts = {
    ...(billing.other_ratios ?? {}),
    ...(billing.usage_facts ?? {}),
  }
  const unit = usageUnit(facts)
  const group =
    typeof billing.group_ratio === 'number' &&
    Number.isFinite(billing.group_ratio)
      ? billing.group_ratio
      : 1
  const unitPrice = finitePositive(billing.model_price)
    ? billing.model_price
    : null
  if (unit && unitPrice != null) {
    const total = unitPrice * unit.value * group
    const unitName =
      unit.key === 'seconds' ? translate('seconds short') : translate('item')
    return `${translate(unit.label)}: ${translate('Unit price')} ${formatAmount(unitPrice)}/${unitName}; ${compactNumber(unit.value)} ${unitName} × ${formatAmount(unitPrice)}/${unitName} × ${compactNumber(group)}x ${translate('group')} = ${formatAmount(total)}`
  }

  const parts: string[] = []
  if (unitPrice != null) parts.push(formatAmount(unitPrice))
  else if (finitePositive(billing.model_ratio))
    parts.push(`${compactNumber(billing.model_ratio)}x`)
  if (unit)
    parts.push(
      `${compactNumber(unit.value)} ${unit.key === 'seconds' ? translate('seconds short') : translate('item')}`
    )
  if (Number.isFinite(group))
    parts.push(`${compactNumber(group)}x ${translate('group')}`)
  return parts.length >= 2 ? parts.join(' × ') : fallback
}

export function BillingRecordCell(props: BillingRecordCellProps) {
  const { t } = useTranslation()
  const rule = taskBillingRule(props.billing, props.fallbackRule, t)

  return (
    <div className='flex max-w-[360px] flex-col items-start gap-1'>
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
        className='text-muted-foreground max-w-[360px] text-[11px] leading-4 break-words whitespace-normal'
        title={rule}
      >
        {rule}
      </span>
    </div>
  )
}
