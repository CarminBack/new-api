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

import React, { useCallback, useEffect, useMemo, useState } from 'react';
import {
  Button,
  Empty,
  Input,
  Modal,
  Space,
  Spin,
  Switch,
  Table,
  Tag,
  Tooltip,
} from '@douyinfe/semi-ui';
import {
  Activity,
  AlertTriangle,
  Gauge,
  KeyRound,
  RefreshCw,
  RotateCcw,
  ShieldCheck,
} from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { API, showError, showSuccess } from '../../../helpers';

const stateConfig = {
  healthy: { label: '健康', color: 'green' },
  circuit_open: { label: '已熔断', color: 'red' },
  half_open: { label: '半开探测', color: 'orange' },
  degraded: { label: '已降量', color: 'orange' },
  recovering: { label: '恢复中', color: 'blue' },
  saturated: { label: '已满载', color: 'purple' },
  isolated: { label: '临时隔离', color: 'red' },
  auto_disabled: { label: '自动禁用', color: 'red' },
  manual_disabled: { label: '手动禁用', color: 'grey' },
  key_auto_disabled: { label: 'Key 自动禁用', color: 'red' },
  key_manual_disabled: { label: 'Key 手动禁用', color: 'grey' },
};

const endpointTypeByPath = {
  '/v1/chat/completions': 'openai',
  '/v1/responses': 'openai-response',
  '/v1/responses/compact': 'openai-response-compact',
  '/v1/messages': 'anthropic',
  '/v1/embeddings': 'embeddings',
  '/v1/rerank': 'jina-rerank',
  '/v1/images/generations': 'image-generation',
};

const routeLastChanged = (route) =>
  Math.max(
    route.last_failure_at || 0,
    route.last_success_at || 0,
    route.last_recovery_at || 0,
    route.last_touched || 0,
  );

const buildRows = (item, includeHealthy) => {
  const rows = [];
  if (item.channel_status === 3) {
    rows.push({
      id: `${item.channel_id}:persistent-channel`,
      item,
      scope: 'channel',
      state: 'auto_disabled',
      reason: item.status_reason,
      lastChanged: item.status_time,
      persistent: true,
    });
  } else if (includeHealthy && item.channel_status === 2) {
    rows.push({
      id: `${item.channel_id}:manual-channel`,
      item,
      scope: 'channel',
      state: 'manual_disabled',
      reason: item.status_reason,
      lastChanged: item.status_time,
      persistent: true,
    });
  }

  if (
    item.adaptive?.channel_state &&
    item.adaptive.channel_state !== 'healthy'
  ) {
    rows.push({
      id: `${item.channel_id}:adaptive-channel`,
      item,
      scope: 'channel',
      state: item.adaptive.channel_state,
      openUntil: item.adaptive.channel_open_until,
    });
  }

  (item.adaptive?.routes || []).forEach((route) => {
    rows.push({
      id: `${item.channel_id}:route:${route.model_name}:${route.request_path}`,
      item,
      scope: 'route',
      state: route.state,
      modelName: route.model_name,
      requestPath: route.request_path,
      reason: route.last_failure_reason || route.last_failure_class,
      statusCode: route.last_failure_status_code,
      openUntil: route.open_until,
      lastChanged: routeLastChanged(route),
      successes: route.successes,
      failures: route.failures,
      poolFailures: route.pool_failures,
      rateLimits: route.rate_limits,
      inFlight: route.in_flight,
      capacity: route.capacity,
      initialCapacity: route.initial_capacity,
    });
  });

  (item.adaptive?.keys || []).forEach((key) => {
    rows.push({
      id: `${item.channel_id}:adaptive-key:${key.key_index}:${key.scope}:${key.model_name || ''}`,
      item,
      scope: 'key',
      state: key.state,
      modelName: key.model_name,
      requestPath: key.request_path,
      keyIndex: key.key_index,
      openUntil: key.open_until,
      lastChanged: key.last_touched,
      inFlight: key.in_flight,
      capacity: key.capacity,
    });
  });

  (item.persistent_keys || []).forEach((key) => {
    rows.push({
      id: `${item.channel_id}:persistent-key:${key.key_index}`,
      item,
      scope: 'key',
      state: key.status === 3 ? 'key_auto_disabled' : 'key_manual_disabled',
      keyIndex: key.key_index,
      reason: key.reason,
      lastChanged: key.disabled_time,
      persistent: true,
    });
  });

  if (includeHealthy && rows.length === 0) {
    rows.push({
      id: `${item.channel_id}:healthy`,
      item,
      scope: 'channel',
      state: 'healthy',
    });
  }
  return rows;
};

const formatTime = (timestamp) => {
  if (!timestamp) return '—';
  return new Intl.DateTimeFormat(undefined, {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  }).format(new Date(timestamp * 1000));
};

const formatDuration = (seconds) => {
  if (seconds <= 0) return '0s';
  const minutes = Math.floor(seconds / 60);
  const remaining = seconds % 60;
  return minutes > 0 ? `${minutes}m ${remaining}s` : `${remaining}s`;
};

const recoveryPayload = (row) => {
  if (row.scope === 'route') {
    return {
      scope: 'route',
      model_name: row.modelName,
      request_path: row.requestPath,
    };
  }
  if (row.scope === 'key') {
    return { scope: 'key', key_index: row.keyIndex };
  }
  return { scope: 'channel' };
};

const ChannelHealthPanel = () => {
  const { t } = useTranslation();
  const [includeHealthy, setIncludeHealthy] = useState(false);
  const [search, setSearch] = useState('');
  const [stateFilter, setStateFilter] = useState('all');
  const [data, setData] = useState(null);
  const [loading, setLoading] = useState(true);
  const [actionKey, setActionKey] = useState(null);

  const loadHealth = useCallback(
    async (silent = false) => {
      if (!silent) setLoading(true);
      try {
        const response = await API.get('/api/channel/health', {
          params: { include_healthy: includeHealthy },
        });
        if (!response.data?.success) {
          throw new Error(response.data?.message || t('加载渠道健康状态失败'));
        }
        setData(response.data.data);
      } catch (error) {
        if (!silent) showError(error.message || t('加载渠道健康状态失败'));
      } finally {
        if (!silent) setLoading(false);
      }
    },
    [includeHealthy, t],
  );

  useEffect(() => {
    loadHealth();
    const timer = window.setInterval(() => {
      if (document.visibilityState === 'visible') loadHealth(true);
    }, 10000);
    return () => window.clearInterval(timer);
  }, [loadHealth]);

  const rows = useMemo(() => {
    const keyword = search.trim().toLowerCase();
    return (data?.items || [])
      .flatMap((item) => buildRows(item, includeHealthy))
      .filter((row) => {
        if (stateFilter !== 'all' && row.state !== stateFilter) return false;
        if (!keyword) return true;
        return [
          row.item.channel_id,
          row.item.channel_name,
          row.modelName,
          row.requestPath,
          row.reason,
        ]
          .filter(Boolean)
          .some((value) => String(value).toLowerCase().includes(keyword));
      });
  }, [data?.items, includeHealthy, search, stateFilter]);

  const recover = async (row) => {
    setActionKey(row.id);
    try {
      let response;
      if (row.persistent && row.scope === 'key') {
        response = await API.post('/api/channel/multi_key/manage', {
          channel_id: row.item.channel_id,
          action: 'enable_key',
          key_index: row.keyIndex,
        });
        if (response.data?.success) {
          await API.post(
            `/api/channel/${row.item.channel_id}/health/recover`,
            recoveryPayload(row),
          );
        }
      } else {
        response = await API.post(
          `/api/channel/${row.item.channel_id}/health/recover`,
          recoveryPayload(row),
        );
      }
      if (!response.data?.success) {
        throw new Error(response.data?.message || t('恢复失败'));
      }
      showSuccess(t('已进入恢复流程'));
      await loadHealth(true);
    } catch (error) {
      showError(error.message || t('恢复失败'));
    } finally {
      setActionKey(null);
    }
  };

  const confirmRecover = (row) => {
    Modal.confirm({
      title: t('确认恢复'),
      content: t(
        '仅清除所选的本地健康状态，并以慢启动容量恢复；不会解除上游账号本身的限制。',
      ),
      okText: t('恢复'),
      cancelText: t('取消'),
      onOk: () => recover(row),
    });
  };

  const test = async (row, recoverAfter) => {
    setActionKey(row.id);
    try {
      const response = await API.get(
        `/api/channel/test/${row.item.channel_id}`,
        {
          params: {
            model: row.modelName || row.item.test_model || undefined,
            endpoint_type: row.requestPath
              ? endpointTypeByPath[row.requestPath]
              : undefined,
          },
        },
      );
      if (!response.data?.success) {
        throw new Error(response.data?.message || t('渠道测试失败'));
      }
      if (!recoverAfter) {
        showSuccess(t('渠道测试成功'));
        return;
      }
      if (row.state === 'auto_disabled') {
        const enableResponse = await API.post(
          `/api/channel/${row.item.channel_id}/status`,
          { status: 1 },
        );
        if (!enableResponse.data?.success) {
          throw new Error(enableResponse.data?.message || t('启用渠道失败'));
        }
      }
      const recoverResponse = await API.post(
        `/api/channel/${row.item.channel_id}/health/recover`,
        recoveryPayload(row),
      );
      if (!recoverResponse.data?.success) {
        throw new Error(recoverResponse.data?.message || t('恢复失败'));
      }
      showSuccess(t('测试成功，已进入恢复流程'));
      await loadHealth(true);
    } catch (error) {
      showError(error.message || t('渠道测试失败'));
    } finally {
      setActionKey(null);
    }
  };

  const scopeContent = (row) => {
    if (row.scope === 'route') {
      return (
        <div className='min-w-0'>
          <div className='truncate font-mono text-xs'>{row.modelName}</div>
          <div className='text-xs text-[var(--semi-color-text-2)] font-mono truncate'>
            {row.requestPath}
          </div>
        </div>
      );
    }
    if (row.scope === 'key') {
      return (
        <Space spacing={6}>
          <KeyRound size={14} />
          <span>{t('Key #{{index}}', { index: (row.keyIndex || 0) + 1 })}</span>
          {row.modelName ? <code>{row.modelName}</code> : null}
        </Space>
      );
    }
    return t('整条渠道');
  };

  const columns = [
    {
      title: t('渠道'),
      dataIndex: 'channel',
      width: 170,
      fixed: 'left',
      render: (_, row) => (
        <div>
          <div className='font-medium'>{row.item.channel_name}</div>
          <div className='text-xs text-[var(--semi-color-text-2)] font-mono'>
            #{row.item.channel_id}
          </div>
        </div>
      ),
    },
    {
      title: t('作用范围'),
      width: 220,
      render: (_, row) => scopeContent(row),
    },
    {
      title: t('状态'),
      width: 120,
      render: (_, row) => {
        const config = stateConfig[row.state] || stateConfig.healthy;
        return <Tag color={config.color}>{t(config.label)}</Tag>;
      },
    },
    {
      title: t('原因'),
      width: 240,
      render: (_, row) => (
        <div className='whitespace-normal'>
          <div className='line-clamp-2'>
            {row.statusCode ? `${row.statusCode} · ` : ''}
            {row.reason || '—'}
          </div>
          <div className='text-xs text-[var(--semi-color-text-2)]'>
            {formatTime(row.lastChanged)}
          </div>
        </div>
      ),
    },
    {
      title: t('2分钟窗口'),
      width: 180,
      render: (_, row) =>
        row.successes === undefined
          ? '—'
          : `S ${row.successes} / F ${row.failures} / P ${row.poolFailures} / 429 ${row.rateLimits}`,
    },
    {
      title: t('在途 / 容量'),
      width: 130,
      render: (_, row) =>
        row.capacity === undefined
          ? '—'
          : `${row.inFlight || 0} / ${row.capacity}${row.initialCapacity ? ` (${row.initialCapacity})` : ''}`,
    },
    {
      title: t('自动恢复'),
      width: 110,
      render: (_, row) =>
        row.openUntil && row.openUntil > Date.now() / 1000
          ? formatDuration(Math.ceil(row.openUntil - Date.now() / 1000))
          : '—',
    },
    {
      title: t('操作'),
      width: 150,
      fixed: 'right',
      render: (_, row) => {
        const busy = actionKey === row.id;
        const actionInProgress = actionKey !== null;
        const canTest = row.scope !== 'key';
        const canRecover =
          row.state !== 'healthy' && row.state !== 'manual_disabled';
        return (
          <Space spacing={4}>
            {canTest ? (
              <Tooltip content={t('测试')}>
                <Button
                  theme='borderless'
                  type='tertiary'
                  icon={<Gauge size={16} />}
                  loading={busy}
                  disabled={actionInProgress}
                  onClick={() => test(row, false)}
                  aria-label={t('测试')}
                />
              </Tooltip>
            ) : null}
            {canTest && canRecover ? (
              <Tooltip
                content={
                  row.state === 'auto_disabled'
                    ? t('测试并启用')
                    : t('测试并恢复')
                }
              >
                <Button
                  theme='borderless'
                  type='tertiary'
                  icon={<ShieldCheck size={16} />}
                  disabled={actionInProgress}
                  onClick={() => test(row, true)}
                  aria-label={t('测试并恢复')}
                />
              </Tooltip>
            ) : null}
            {canRecover && row.state !== 'auto_disabled' ? (
              <Tooltip content={row.persistent ? t('启用') : t('恢复')}>
                <Button
                  theme='borderless'
                  type='tertiary'
                  icon={<RotateCcw size={16} />}
                  disabled={actionInProgress}
                  onClick={() => confirmRecover(row)}
                  aria-label={t('恢复')}
                />
              </Tooltip>
            ) : null}
          </Space>
        );
      },
    },
  ];

  const summaryItems = [
    {
      label: '自动禁用',
      value: data?.summary?.auto_disabled_channels || 0,
      state: 'auto_disabled',
      icon: AlertTriangle,
    },
    {
      label: '已熔断',
      value: data?.summary?.circuit_open_routes || 0,
      state: 'circuit_open',
      icon: Activity,
    },
    {
      label: '已降量',
      value: data?.summary?.degraded_routes || 0,
      state: 'degraded',
      icon: Gauge,
    },
    {
      label: '恢复中',
      value: data?.summary?.recovering_routes || 0,
      state: 'recovering',
      icon: ShieldCheck,
    },
    {
      label: '已满载',
      value: data?.summary?.saturated_routes || 0,
      state: 'saturated',
      icon: Gauge,
    },
    {
      label: 'Key 隔离',
      value: data?.summary?.isolated_keys || 0,
      state: 'isolated',
      icon: KeyRound,
    },
  ];

  return (
    <div className='overflow-hidden rounded-lg border border-[var(--semi-color-border)] bg-[var(--semi-color-bg-2)]'>
      <div className='flex flex-wrap items-center gap-2 border-b border-[var(--semi-color-border)] p-3'>
        <Input
          value={search}
          onChange={setSearch}
          placeholder={t('搜索渠道、模型、路径或原因')}
          className='min-w-[220px] max-w-[420px] flex-1'
          showClear
        />
        <Space spacing={8}>
          <span className='text-sm'>{t('显示健康渠道')}</span>
          <Switch checked={includeHealthy} onChange={setIncludeHealthy} />
          <Button
            theme='borderless'
            type='tertiary'
            icon={<RefreshCw size={16} />}
            onClick={() => loadHealth()}
            loading={loading}
            aria-label={t('刷新')}
          />
        </Space>
      </div>

      <div className='grid grid-cols-2 border-b border-[var(--semi-color-border)] sm:grid-cols-3 lg:grid-cols-6'>
        {summaryItems.map((item) => {
          const Icon = item.icon;
          const active = stateFilter === item.state;
          return (
            <button
              key={item.label}
              type='button'
              onClick={() => setStateFilter(active ? 'all' : item.state)}
              aria-pressed={active}
              className={`flex min-h-14 items-center gap-2 border-r border-[var(--semi-color-border)] px-3 text-left transition-colors hover:bg-[var(--semi-color-fill-0)] ${
                active ? 'bg-[var(--semi-color-fill-1)]' : ''
              }`}
            >
              <Icon size={16} className='text-[var(--semi-color-text-2)]' />
              <span>
                <span className='block text-lg font-semibold tabular-nums leading-5'>
                  {item.value}
                </span>
                <span className='block text-xs text-[var(--semi-color-text-2)]'>
                  {t(item.label)}
                </span>
              </span>
            </button>
          );
        })}
      </div>

      {loading ? (
        <div className='flex h-40 items-center justify-center'>
          <Spin />
        </div>
      ) : rows.length === 0 ? (
        <div className='flex h-40 items-center justify-center'>
          <Empty description={t('没有匹配的渠道健康问题')} />
        </div>
      ) : (
        <Table
          columns={columns}
          dataSource={rows}
          rowKey='id'
          pagination={false}
          scroll={{ x: 1320 }}
          size='small'
        />
      )}
    </div>
  );
};

export default ChannelHealthPanel;
