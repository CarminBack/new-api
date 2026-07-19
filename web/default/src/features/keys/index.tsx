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
import { Copy, Check, Image as ImageIcon } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/layout'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { useCopyToClipboard } from '@/hooks/use-copy-to-clipboard'

import { ApiKeysDialogs } from './components/api-keys-dialogs'
import { ApiKeysPrimaryButtons } from './components/api-keys-primary-buttons'
import { ApiKeysProvider } from './components/api-keys-provider'
import { ApiKeysTable } from './components/api-keys-table'
import { IMAGE_API_BASE_URL } from './constants'

export function ApiKeys() {
  const { t } = useTranslation()
  const { copiedText, copyToClipboard } = useCopyToClipboard()
  const imageApiCopied = copiedText === IMAGE_API_BASE_URL

  return (
    <ApiKeysProvider>
      <SectionPageLayout fixedContent>
        <SectionPageLayout.Title>{t('API Keys')}</SectionPageLayout.Title>
        <SectionPageLayout.Actions>
          <ApiKeysPrimaryButtons />
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>
          <Alert className='mb-4'>
            <ImageIcon />
            <AlertTitle>{t('Image group request address')}</AlertTitle>
            <AlertDescription className='flex flex-wrap items-center gap-2'>
              <span>
                {t(
                  'Use this Base URL when calling an API key in the Image group:'
                )}
              </span>
              <code className='bg-muted rounded px-1.5 py-0.5 font-mono text-xs'>
                {IMAGE_API_BASE_URL}
              </code>
              <Button
                type='button'
                variant='outline'
                size='sm'
                onClick={() => copyToClipboard(IMAGE_API_BASE_URL)}
              >
                {imageApiCopied ? (
                  <Check className='mr-1.5 size-3.5' />
                ) : (
                  <Copy className='mr-1.5 size-3.5' />
                )}
                {imageApiCopied ? t('Copied') : t('Copy')}
              </Button>
            </AlertDescription>
          </Alert>
          <ApiKeysTable />
        </SectionPageLayout.Content>
      </SectionPageLayout>

      <ApiKeysDialogs />
    </ApiKeysProvider>
  )
}
