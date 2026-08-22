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

## 2026-08-22 API Key Ratio Limit Display

- Changed the API Key list ratio-limit cell to display the numeric value only (for example, `0.1`); `0` still displays `Unlimited`.
- Commit: `2824decd0` (`fix(web): show ratio limit as numeric value`), pushed to `CarminBack/new-api` branch `upgrade-rc24-test`.
- Web format check, typecheck, build, and targeted oxlint passed.
- Test deployment: `new-api-rc20-test` only, image digest `sha256:eae5c9a2b8389def8757af8f512ea1a8cfdf036f508cd050113ee164be1f2d35`; compose backup `docker-compose.yml.bak-20260822-031156`.
- Verification: `/api/status` returned success with version `carmin-20260822-2824dec`; container health is `healthy`, restart count `0`, and the running image digest matches the requested digest. Browser UI automation was unavailable because the browser bridge was not trusted in this environment.
- Production `new-api-docker` was not changed.
