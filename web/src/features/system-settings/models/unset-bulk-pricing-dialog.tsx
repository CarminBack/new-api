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
import { useEffect, useId, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'

import type {
  UnsetBulkPricingMode,
  UnsetBulkPricingValues,
} from './model-pricing-snapshots'

type UnsetBulkPricingDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  modelCount: number
  isSaving: boolean
  onConfirm: (values: UnsetBulkPricingValues) => void
}

const parsePrice = (value: string) => {
  if (value.trim() === '') return null
  const number = Number(value)
  return Number.isFinite(number) && number >= 0 ? number : null
}

export function UnsetBulkPricingDialog({
  open,
  onOpenChange,
  modelCount,
  isSaving,
  onConfirm,
}: UnsetBulkPricingDialogProps) {
  const { t } = useTranslation()
  const inputId = useId()
  const outputId = useId()
  const requestId = useId()
  const [mode, setMode] = useState<UnsetBulkPricingMode>('token')
  const [inputPrice, setInputPrice] = useState('')
  const [outputPrice, setOutputPrice] = useState('')
  const [requestPrice, setRequestPrice] = useState('')

  useEffect(() => {
    if (!open) return
    setMode('token')
    setInputPrice('')
    setOutputPrice('')
    setRequestPrice('')
  }, [open])

  const parsedInput = parsePrice(inputPrice)
  const parsedOutput = parsePrice(outputPrice)
  const parsedRequest = parsePrice(requestPrice)
  const valid =
    mode === 'request'
      ? parsedRequest !== null
      : parsedInput !== null && parsedOutput !== null

  const handleConfirm = () => {
    if (!valid) return
    onConfirm({
      mode,
      inputPrice: parsedInput ?? 0,
      outputPrice: parsedOutput ?? 0,
      requestPrice: parsedRequest ?? 0,
    })
  }

  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title={t('Set prices for unset models')}
      description={t(
        'Apply one price to {{count}} models and save immediately. Prices are saved as expressions.',
        { count: modelCount }
      )}
      contentClassName='sm:max-w-lg'
      bodyClassName='space-y-4'
      footer={
        <>
          <Button
            type='button'
            variant='outline'
            onClick={() => onOpenChange(false)}
            disabled={isSaving}
          >
            {t('Cancel')}
          </Button>
          <Button
            type='button'
            onClick={handleConfirm}
            disabled={!valid || isSaving || modelCount === 0}
          >
            {isSaving ? t('Saving...') : t('Apply and save')}
          </Button>
        </>
      }
    >
      <div className='space-y-2'>
        <Label>{t('Billing unit')}</Label>
        <ToggleGroup
          value={[mode]}
          onValueChange={(value) => {
            const next = value.find((item) => item !== mode)
            if (next) setMode(next as UnsetBulkPricingMode)
          }}
          aria-label={t('Billing unit')}
          variant='outline'
          className='grid w-full grid-cols-2 gap-2'
        >
          <ToggleGroupItem value='token' className='w-full'>
            {t('Per token')}
          </ToggleGroupItem>
          <ToggleGroupItem value='request' className='w-full'>
            {t('Per request')}
          </ToggleGroupItem>
        </ToggleGroup>
      </div>

      {mode === 'token' ? (
        <div className='grid gap-4 sm:grid-cols-2'>
          <div className='space-y-2'>
            <Label htmlFor={inputId}>
              {t('Input price (USD / 1M tokens)')}
            </Label>
            <Input
              id={inputId}
              inputMode='decimal'
              value={inputPrice}
              onChange={(event) => setInputPrice(event.target.value)}
              placeholder='0'
              aria-invalid={inputPrice !== '' && parsedInput === null}
            />
          </div>
          <div className='space-y-2'>
            <Label htmlFor={outputId}>
              {t('Output price (USD / 1M tokens)')}
            </Label>
            <Input
              id={outputId}
              inputMode='decimal'
              value={outputPrice}
              onChange={(event) => setOutputPrice(event.target.value)}
              placeholder='0'
              aria-invalid={outputPrice !== '' && parsedOutput === null}
            />
          </div>
        </div>
      ) : (
        <div className='space-y-2'>
          <Label htmlFor={requestId}>{t('Price per request (USD)')}</Label>
          <Input
            id={requestId}
            inputMode='decimal'
            value={requestPrice}
            onChange={(event) => setRequestPrice(event.target.value)}
            placeholder='0'
            aria-invalid={requestPrice !== '' && parsedRequest === null}
          />
        </div>
      )}

      <p className='text-muted-foreground text-sm'>
        {t(
          'Selected rows are used when any are checked; otherwise all models in the current filtered list are updated.'
        )}
      </p>
    </Dialog>
  )
}
