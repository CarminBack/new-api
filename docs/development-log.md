# Development Log

## 2026-08-22 API Key Group Ratio Limit

- Added `Token.max_ratio`; `0` preserves unlimited behavior and is persisted by the existing Token cache.
- Added request-time rejection using the resolved effective group ratio before pre-consume or upstream calls.
- Added API Key form/list controls, Redis float cache decoding, error code `token_ratio_exceeded`, and unit coverage.
- Commit: `4b947a233` (`feat(token): enforce API key group ratio limits`).
- Test deployment: `new-api-rc20-test` only, image digest `sha256:cd1a974ed625d935fd3ca0e83f265fa7babc53cec670ab7e66e51228783fec28`.
- Verification: Go full test suite, web typecheck/build/format check passed. Test request with actual ratio `0.3` and limit `0.0001` returned HTTP 403 with `token_ratio_exceeded`; boundary limit `0.3` did not trigger the guard. Test token limit restored to `0`; test container remained healthy with zero restarts.
- Production `new-api-docker` was not changed.
