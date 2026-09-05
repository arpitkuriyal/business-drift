import { useState, type FormEvent } from 'react'
import { getMe, login, register, saveSession } from '../api'
import type { Identity, Session } from '../types'

type AuthMode = 'login' | 'register'

type Props = {
  onAuthenticated: (identity: Identity, session: Session) => void
}

function messageFrom(error: unknown) {
  return error instanceof Error ? error.message : 'Something went wrong. Please try again.'
}

export function AuthView({ onAuthenticated }: Props) {
  const [mode, setMode] = useState<AuthMode>('login')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setBusy(true)
    setError('')

    const values = new FormData(event.currentTarget)
    const email = String(values.get('email') ?? '').trim()
    const password = String(values.get('password') ?? '')

    try {
      if (mode === 'register') {
        const result = await register({
          organization_name: String(values.get('organization_name') ?? '').trim(),
          email,
          password,
        })
        localStorage.setItem('business-drift-organization', result.identity.organization_id)
        localStorage.setItem('business-drift-email', result.identity.email)
        onAuthenticated(result.identity, result.session)
        return
      }

      const organizationID = String(values.get('organization_id') ?? '').trim()
      const session = await login({ organization_id: organizationID, email, password })

      // Save first because getMe reads the bearer token from the shared API client.
      saveSession(session)
      const identity = await getMe()
      localStorage.setItem('business-drift-organization', organizationID)
      localStorage.setItem('business-drift-email', email)
      onAuthenticated(identity, session)
    } catch (requestError) {
      saveSession(null)
      setError(messageFrom(requestError))
    } finally {
      setBusy(false)
    }
  }

  function changeMode(nextMode: AuthMode) {
    setMode(nextMode)
    setError('')
  }

  return (
    <main className="auth-layout">
      <section className="auth-story">
        <a className="brand" href="/" aria-label="Business Drift home">
          <span className="brand-mark">BD</span>
          <span>Business Drift</span>
        </a>
        <div className="story-copy">
          <p className="eyebrow">Revenue intelligence</p>
          <h1>Find the gaps between billing and customer reality.</h1>
          <p className="story-intro">
            Review mismatched customer states, trace the evidence, and decide what
            needs attention before revenue drifts.
          </p>
        </div>
        <p className="story-footnote">Rules detect. Evidence explains. You decide.</p>
      </section>

      <section className="auth-panel">
        <form className="auth-card" onSubmit={handleSubmit}>
          <div>
            <p className="eyebrow">{mode === 'login' ? 'Welcome back' : 'Start reviewing drift'}</p>
            <h2>{mode === 'login' ? 'Sign in to your workspace' : 'Create your workspace'}</h2>
            <p className="muted">
              {mode === 'login'
                ? 'Use the organization ID saved when you registered.'
                : 'The first account becomes the workspace owner.'}
            </p>
          </div>

          {mode === 'register' && (
            <label>
              Organization name
              <input name="organization_name" placeholder="Acme Inc." required />
            </label>
          )}

          {mode === 'login' && (
            <label>
              Organization ID
              <input
                name="organization_id"
                defaultValue={localStorage.getItem('business-drift-organization') ?? ''}
                placeholder="xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"
                required
              />
            </label>
          )}

          <label>
            Work email
            <input
              name="email"
              type="email"
              defaultValue={localStorage.getItem('business-drift-email') ?? ''}
              placeholder="you@company.com"
              autoComplete="email"
              required
            />
          </label>
          <label>
            Password
            <input
              name="password"
              type="password"
              placeholder="At least 12 characters"
              minLength={12}
              autoComplete={mode === 'login' ? 'current-password' : 'new-password'}
              required
            />
          </label>

          {error && <p className="form-message error-message" role="alert">{error}</p>}

          <button className="primary-button" type="submit" disabled={busy}>
            {busy ? 'Please wait…' : mode === 'login' ? 'Sign in' : 'Create workspace'}
          </button>
          <p className="auth-switch">
            {mode === 'login' ? 'New to Business Drift?' : 'Already have a workspace?'}{' '}
            <button type="button" onClick={() => changeMode(mode === 'login' ? 'register' : 'login')}>
              {mode === 'login' ? 'Create one' : 'Sign in'}
            </button>
          </p>
        </form>
      </section>
    </main>
  )
}
