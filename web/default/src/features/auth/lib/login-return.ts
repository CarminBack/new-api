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

const LOGIN_RETURN_STORAGE_KEY = 'new-api:login_return_to'

export function normalizeLoginReturnTo(value?: string | null): string {
  if (typeof value !== 'string') return ''
  const normalized = value.trim()
  if (
    !normalized.startsWith('/oauth/authorize?') ||
    normalized.startsWith('//')
  ) {
    return ''
  }
  return normalized
}

export function rememberLoginReturnTo(value?: string | null): string {
  const normalized = normalizeLoginReturnTo(value)
  if (normalized && typeof window !== 'undefined') {
    window.localStorage.setItem(LOGIN_RETURN_STORAGE_KEY, normalized)
  }
  return normalized
}

export function consumeLoginReturnTo(value?: string | null): string {
  const direct = normalizeLoginReturnTo(value)
  if (typeof window === 'undefined') return direct

  const stored = normalizeLoginReturnTo(
    window.localStorage.getItem(LOGIN_RETURN_STORAGE_KEY)
  )
  window.localStorage.removeItem(LOGIN_RETURN_STORAGE_KEY)
  return direct || stored
}
