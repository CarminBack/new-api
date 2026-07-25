/*
Copyright (C) 2025 QuantumNous

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

import React, { useState } from 'react';
import { TabPane, Tabs } from '@douyinfe/semi-ui';
import { useTranslation } from 'react-i18next';
import ChannelsTable from '../../components/table/channels';
import ChannelHealthPanel from '../../components/table/channels/ChannelHealthPanel';

const File = () => {
  const { t } = useTranslation();
  const [activeView, setActiveView] = useState(
    localStorage.getItem('classic:channels-active-view') === 'health'
      ? 'health'
      : 'channels',
  );

  return (
    <div className='mt-[60px] px-2'>
      <Tabs
        type='button'
        activeKey={activeView}
        onChange={(value) => {
          localStorage.setItem('classic:channels-active-view', value);
          setActiveView(value);
        }}
        className='mb-3'
      >
        <TabPane itemKey='channels' tab={t('渠道列表')}>
          <ChannelsTable />
        </TabPane>
        <TabPane itemKey='health' tab={t('渠道健康')}>
          <ChannelHealthPanel />
        </TabPane>
      </Tabs>
    </div>
  );
};

export default File;
