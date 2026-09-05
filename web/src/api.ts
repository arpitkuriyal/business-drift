import type {
  Finding,
  FixtureInput,
  FixtureResult,
  Identity,
  Organization,
  Session,
  StripeIntegration,
} from './types'

const API_BASE = import.meta.env.VITE_API_URL ?? ''
const SESSION_KEY = 'business-drift-session'

export class APIError extends Error {
  status: number

  constructor(status: number, message: string) {
    super(message)
    this.status = status
  }
}

export function loadSession(): Session | null {
  const stored = sessionStorage.getItem(SESSION_KEY)
  if (!stored) return null
  try {
    return JSON.parse(stored) as Session
  } catch {
    sessionStorage.removeItem(SESSION_KEY)
    return null
  }
}

export function saveSession(session: Session | null) {
  if (session) sessionStorage.setItem(SESSION_KEY, JSON.stringify(session))
  else sessionStorage.removeItem(SESSION_KEY)
}

async function parseResponse<T>(response: Response): Promise<T> {
  if (response.status === 204) return undefined as T
  const body = await response.json().catch(() => null)
  if (!response.ok) {
    const message = body?.error?.message ?? 'The request could not be completed.'
    throw new APIError(response.status, message)
  }
  return body as T
}

async function refreshSession(): Promise<Session | null> {
  const current = loadSession()
  if (!current?.refresh_token) return null
  const response = await fetch(`${API_BASE}/api/v1/auth/refresh`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ refresh_token: current.refresh_token }),
  })
  if (!response.ok) {
    saveSession(null)
    return null
  }
  const next = (await response.json()) as Session
  saveSession(next)
  return next
}

async function request<T>(path: string, options: RequestInit = {}, authenticated = true): Promise<T> {
  const session = loadSession()
  const headers = new Headers(options.headers)
  if (options.body) headers.set('Content-Type', 'application/json')
  if (authenticated && session) headers.set('Authorization', `Bearer ${session.access_token}`)

  let response = await fetch(`${API_BASE}${path}`, { ...options, headers })
  if (authenticated && response.status === 401 && session?.refresh_token) {
    const next = await refreshSession()
    if (next) {
      headers.set('Authorization', `Bearer ${next.access_token}`)
      response = await fetch(`${API_BASE}${path}`, { ...options, headers })
    }
  }
  return parseResponse<T>(response)
}

export async function register(input: { organization_name: string; email: string; password: string }) {
  return request<{ identity: Identity; session: Session }>(
    '/api/v1/auth/register',
    { method: 'POST', body: JSON.stringify(input) },
    false,
  )
}

export async function login(input: { organization_id: string; email: string; password: string }) {
  return request<Session>('/api/v1/auth/login', { method: 'POST', body: JSON.stringify(input) }, false)
}

export const getMe = () => request<Identity>('/api/v1/auth/me')
export const getOrganization = () => request<Organization>('/api/v1/organization')
export const getFindings = () => request<{ data: Finding[] }>('/api/v1/findings')
export const getFinding = (id: string) => request<Finding>(`/api/v1/findings/${id}`)

export async function getStripe(): Promise<StripeIntegration | null> {
  try {
    return await request<StripeIntegration>('/api/v1/integrations/stripe')
  } catch (error) {
    if (error instanceof APIError && error.status === 404) return null
    throw error
  }
}

export const saveStripe = (input: { api_key: string; webhook_secret: string }) =>
  request<StripeIntegration>('/api/v1/integrations/stripe', { method: 'POST', body: JSON.stringify(input) })

export const syncStripe = () =>
  request<{ job_id: string; status: string }>('/api/v1/integrations/stripe/sync', { method: 'POST' })

export const ingestFixture = (input: FixtureInput) =>
  request<FixtureResult>('/api/v1/dev/fixture-events', { method: 'POST', body: JSON.stringify(input) })

export async function logout() {
  const session = loadSession()
  if (session?.refresh_token) {
    await request<void>(
      '/api/v1/auth/logout',
      { method: 'POST', body: JSON.stringify({ refresh_token: session.refresh_token }) },
      false,
    ).catch(() => undefined)
  }
  saveSession(null)
}
