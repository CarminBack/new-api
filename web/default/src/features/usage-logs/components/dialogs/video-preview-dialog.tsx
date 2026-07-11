/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or (at your
option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { Check, Copy, ExternalLink, Video } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { useCopyToClipboard } from '@/hooks/use-copy-to-clipboard'

interface VideoPreviewDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  previewUrl: string
  resultUrl: string
}

export function VideoPreviewDialog({
  open,
  onOpenChange,
  previewUrl,
  resultUrl,
}: VideoPreviewDialogProps) {
  const { t } = useTranslation()
  const { copiedText, copyToClipboard } = useCopyToClipboard({ notify: false })

  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title={
        <>
          <Video className='h-5 w-5' />
          {t('Video Preview')}
        </>
      }
      contentClassName='sm:max-w-3xl'
      titleClassName='flex items-center gap-2'
      contentHeight='auto'
      bodyClassName='space-y-4'
    >
      <video
        src={previewUrl}
        controls
        preload='metadata'
        className='bg-muted aspect-video w-full rounded-lg border object-contain'
      />
      <div className='space-y-2'>
        <Label className='text-muted-foreground text-xs'>
          {t('Result URL')}
        </Label>
        <div className='bg-muted/50 flex items-center gap-2 rounded-md border p-2'>
          <span className='min-w-0 flex-1 truncate font-mono text-xs'>
            {resultUrl}
          </span>
          <Button
            variant='ghost'
            size='icon'
            className='size-8 shrink-0'
            onClick={() => copyToClipboard(resultUrl)}
            title={t('Copy Link')}
          >
            {copiedText === resultUrl ? (
              <Check className='size-4 text-green-600' />
            ) : (
              <Copy className='size-4' />
            )}
          </Button>
          <Button
            variant='ghost'
            size='icon'
            className='size-8 shrink-0'
            onClick={() =>
              window.open(resultUrl, '_blank', 'noopener,noreferrer')
            }
            title={t('Open in new tab')}
          >
            <ExternalLink className='size-4' />
          </Button>
        </div>
      </div>
    </Dialog>
  )
}
