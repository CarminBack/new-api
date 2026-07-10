const LOGIN_RETURN_STORAGE_KEY = 'new-api:login_return_to';

export function normalizeLoginReturnTo(value) {
  if (typeof value !== 'string') return '';
  const normalized = value.trim();
  if (
    !normalized.startsWith('/oauth/authorize?') ||
    normalized.startsWith('//')
  ) {
    return '';
  }
  return normalized;
}

export function rememberLoginReturnTo(value) {
  const normalized = normalizeLoginReturnTo(value);
  if (normalized) {
    localStorage.setItem(LOGIN_RETURN_STORAGE_KEY, normalized);
  }
  return normalized;
}

export function consumeLoginReturnTo() {
  const normalized = normalizeLoginReturnTo(
    localStorage.getItem(LOGIN_RETURN_STORAGE_KEY),
  );
  localStorage.removeItem(LOGIN_RETURN_STORAGE_KEY);
  return normalized;
}

export function redirectAfterLogin(navigate, fallback) {
  const returnTo = consumeLoginReturnTo();
  if (returnTo) {
    window.location.assign(returnTo);
    return;
  }
  navigate(fallback);
}
