# Development Log

## 2026-08-22 API Key Group Ratio Limit

- Added `Token.max_ratio`; `0` preserves unlimited behavior and is persisted by the existing Token cache.
- Added request-time rejection using the resolved effective group ratio before pre-consume or upstream calls.
- Added API Key form/list controls, Redis float cache decoding, error code `token_ratio_exceeded`, and unit coverage.
- Commit: `4b947a233` (`feat(token): enforce API key group ratio limits`).
- Test deployment: `new-api-rc20-test` only, image digest `sha256:cd1a974ed625d935fd3ca0e83f265fa7babc53cec670ab7e66e51228783fec28`.
- Verification: Go full test suite, web typecheck/build/format check passed. Test request with actual ratio `0.3` and limit `0.0001` returned HTTP 403 with `token_ratio_exceeded`; boundary limit `0.3` did not trigger the guard. Test token limit restored to `0`; test container remained healthy with zero restarts.
- Production `new-api-docker` was not changed.

## 2026-08-22 API Key Ratio Limit Retest

- Retested on `new-api-rc20-test` using test token id `57` (original `max_ratio=0.1`), with Token Redis cache invalidated between changes.
- With limit `0.0001` and effective ChatGPT group ratio `0.3`, the request returned HTTP `403` with code `token_ratio_exceeded` before upstream routing.
- With boundary limit `0.3`, the request did not return `token_ratio_exceeded`; it continued to normal channel selection and returned `get_channel_failed` because no usable test upstream channel was available.
- Test token limit was restored to `0.1`; test container remained healthy with zero restarts. Production `new-api-docker` remained unchanged and healthy.

## 2026-08-22 Production Deployment

- After the test retest passed, deployed the verified image to production service `new-api-docker` only.
- Production image changed from `sha256:e096d669684ba94f0b3c0e206b70653918613076d0d0af736f4d1282db99fb57` to `sha256:6abe93bd95693259921dc9dcb5d580cd6ee51f37bc92a7fd9450bbe0470dde05` (GitHub Actions run `32560695621`, branch `upgrade-rc24-test`).
- Backup: `/opt/docker/new-api/docker-compose.yml.bak-20260822-075942`.
- Verification: `http://127.0.0.1:2888/api/status` returned `success=true`, version `carmin-20260822-6b11877`; production app and Redis were healthy with zero restarts.

## 2026-08-22 Group Channel Latency Detection

- Added read-only `GET /api/channel/latency`, which aggregates the latest channel probe latency by configured channel group and returns per-channel status and last-test time without exposing credentials.
- Added a Channels page `Latency` tab with group filtering, average/minimum/maximum latency, tested-channel counts, last-test time, per-channel details, refresh, and manual reuse of the existing “Test All Channels” probe task.
- The display uses scheduled/explicit channel probes; real user-request latency remains passive telemetry and does not create additional upstream calls.
- Backend tests: `go test ./controller ./router ./model`. Frontend format check, typecheck, targeted oxlint, and production build passed.
- Test deployment pending; production remains unchanged for this feature.

## 2026-08-22 Group Channel Latency Test Deployment

- Test image built by GitHub Actions run `32562702814`: `sha256:c19cb7842e534eda3c5fee768e6de591ead6bcb20eea931924b9fe9daade0549`.
- Updated `new-api-rc20-test` only; compose backup: `docker-compose.yml.bak-20260822-083916`.
- Verification: test container is `healthy` with zero restarts, `/api/status` reports version `carmin-20260822-284fec0`, static bundle contains `/api/channel/latency` and `All Groups`, and unauthenticated latency access correctly returns HTTP `401`.
- Production `new-api-docker` was not changed for this feature.

## 2026-08-22 Model Status Monitor Cards

- Added request and success counts to performance metric group and history buckets.
- Added a Channels `Status` tab with model-by-group cards showing ratio, average TTFT, availability counts, conversation latency, history buckets, and Normal/Warning/Abnormal states from real `/api/perf-metrics` data.
- Reuses the existing all-channel probe action and refreshes metrics without generating extra upstream requests.
- Test image built by GitHub Actions run `32564899764`: `sha256:38b75457b56361dd2c55cd283b8c31bc0f24f26b2f19d520ed389a9ca788ffe6`.
- Updated `new-api-rc20-test` only; compose backup: `docker-compose.yml.bak-20260822-092924`.
- Verification: container is `healthy` with zero restarts, `/api/status` reports `carmin-20260822-562151a`, unauthenticated `/api/perf-metrics/summary` returns `401`, and the served bundle contains the model monitor labels.
- Production `new-api-docker` was not changed for this feature.

## 2026-08-22 User-visible Channel Status and Test Fixtures

- Added authenticated read-only `/channel-status` navigation for all signed-in users; admin-only channel configuration remains protected, and the public status view hides the channel probe action.
- Test image built by GitHub Actions run `32565750248`: `sha256:a4e973afdc5de4c3e908f501b54a3932ec65f0f2f5c0508b251480ae72e75738`.
- Updated `new-api-rc20-test` only; compose backup: `docker-compose.yml.bak-20260822-094802`.
- Inserted temporary `demo-normal`, `demo-warning`, and `demo-abnormal` performance fixtures in the active `ChatGPT` group across 12 hourly buckets in the test database to exercise all status colors. An initial `default`-group fixture was removed because inactive groups are filtered from the status response. These rows are isolated to the test station and can be removed with `DELETE FROM perf_metrics WHERE model_name LIKE 'demo-%'`.
- Verification: test container healthy with zero restarts, `/api/status` reports `carmin-20260822-e790059`, bundle contains `/channel-status` and monitor labels, and unauthenticated metrics access returns `401`.
- Production `new-api-docker` was not changed.

## 2026-08-22 Group Status Display Rules

- Renamed the user-facing status section to `Group Status` (`分组状态`).
- Group state is now latency-based: latest valid TTFT at or below 5 seconds is Normal/green, above 5 seconds is Warning/yellow, and no successful request or no valid TTFT is Abnormal/red.
- The TTFT metric displays the latest valid series bucket rather than the 24-hour aggregate average.
- Group cards and status counts are filtered by the authenticated user's `usable_group` response, which includes special usable-group rules and prevents unavailable groups from being shown.
- Test image built by GitHub Actions run `32566953035`: `sha256:64355e238c7ce214e2009e61b66db422939abbc84231a006db2131009d778bdb`.
- Updated `new-api-rc20-test` only; compose backup: `docker-compose.yml.bak-20260822-101438`.
- Verification: container is healthy with zero restarts, `/api/status` reports `carmin-20260822-b54b82f`, served assets contain `Group Status`, `5000`, `usable_group`, and `分组状态`, and unauthenticated metrics access returns `401`.
- Production remains unchanged.
- Demo fixture values were aligned with the latency rule: `demo-normal` 500 ms TTFT, `demo-warning` 6000 ms TTFT, and `demo-abnormal` 0 successful requests.

## 2026-08-22 Scheduled Group Probe Configuration

- Enabled scheduled channel testing by default at a 5-minute interval.
- Added `monitor_setting.probe_models`, a JSON group-to-model mapping used by automatic channel tests; unconfigured groups retain each channel's existing test-model fallback.
- Added an administrator setting editor for the mapping, for example `{"ChatGPT":"gpt-4o-mini"}`.
- Test deployment pending; production remains unchanged.

## 2026-08-22 Scheduled Group Probe Test Deployment

- Test image built by GitHub Actions run `32567833670`: `sha256:5b2607fd065e1bdeb80febea9bea414ea7cec0bf55f69d86e28c66aa2543ce05`.
- Updated `new-api-rc20-test` only; compose backup: `docker-compose.yml.bak-20260822-103516`.
- Test options set to scheduled probes enabled, 5-minute interval, `scheduled_all` mode, and `{"ChatGPT":"gpt-4o-mini"}` as the probe model mapping.
- Verification: container is healthy with zero restarts, `/api/status` reports `carmin-20260822-a24dbc8`, and the first `channel_test` task completed 26 channel probes. Production remains unchanged.

## 2026-08-22 API Key Ratio Limit Display

- Changed the API Key list ratio-limit cell to display the numeric value only (for example, `0.1`); `0` still displays `Unlimited`.
- Commit: `2824decd0` (`fix(web): show ratio limit as numeric value`), pushed to `CarminBack/new-api` branch `upgrade-rc24-test`.
- Web format check, typecheck, build, and targeted oxlint passed.
- Test deployment: `new-api-rc20-test` only, image digest `sha256:eae5c9a2b8389def8757af8f512ea1a8cfdf036f508cd050113ee164be1f2d35`; compose backup `docker-compose.yml.bak-20260822-031156`.
- Verification: `/api/status` returned success with version `carmin-20260822-2824dec`; container health is `healthy`, restart count `0`, and the running image digest matches the requested digest. Browser UI automation was unavailable because the browser bridge was not trusted in this environment.
- Production `new-api-docker` was not changed.
