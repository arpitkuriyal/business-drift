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

const eyebrowClass = 'mb-3.5 text-xs font-extrabold tracking-[0.14em] uppercase text-[#7b8f88]'
const inputClass = 'w-full rounded-xl border border-[#d6d9d5] bg-white px-3.5 py-3 text-[#14201c] outline-none transition focus:border-[#39745f] focus:ring-3 focus:ring-[#39745f]/12'
const labelClass = 'grid gap-2 text-[13px] font-bold text-[#36433f]'

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
    <main className="grid min-h-svh lg:grid-cols-[minmax(0,1.08fr)_minmax(420px,0.92fr)]">
      <section className="relative flex min-h-[42svh] flex-col justify-between overflow-hidden bg-[#101d1a] px-6 py-8 text-[#f7f6f1] lg:min-h-svh lg:px-[clamp(2.5rem,6vw,5.75rem)] lg:py-11">
        <div aria-hidden="true" className="absolute -right-[16%] -bottom-[24%] size-[520px] rounded-full border border-[#daeee1]/15 shadow-[0_0_0_78px_rgba(218,238,225,0.04),0_0_0_156px_rgba(218,238,225,0.025)]" />
        <a className="relative z-10 inline-flex w-fit items-center gap-3 font-bold tracking-[-0.02em] text-inherit no-underline" href="/" aria-label="Business Drift home">
          <span className="grid size-9.5 place-items-center rounded-xl bg-[#c9f269] text-xs font-extrabold tracking-wider text-[#10201b]">BD</span>
          <span>Business Drift</span>
        </a>
        <div className="relative z-10 max-w-[680px]">
          <p className="mb-3.5 text-xs font-extrabold tracking-[0.14em] text-[#c9f269] uppercase">Revenue intelligence</p>
          <h1 className="m-0 max-w-[650px] text-[clamp(2.375rem,5.5vw,4.875rem)] leading-[0.98] font-semibold tracking-[-0.055em]">Find the gaps between billing and customer reality.</h1>
          <p className="mt-5 max-w-[580px] text-base leading-relaxed text-[#aab9b4] sm:text-lg lg:mt-7.5">
            Review mismatched customer states, trace the evidence, and decide what
            needs attention before revenue drifts.
          </p>
        </div>
        <p className="relative z-10 hidden text-[13px] text-[#738680] lg:block">Rules detect. Evidence explains. You decide.</p>
      </section>

      <section className="grid min-h-0 place-items-center bg-[#f3f1ea] px-4.5 py-7 lg:min-h-svh lg:p-12">
        <form className="grid w-full max-w-[440px] gap-5.5 rounded-[22px] border border-[#dcd9cf] bg-[#fffefa] p-5.5 shadow-[0_24px_70px_rgba(35,45,41,0.08)] sm:p-10" onSubmit={handleSubmit}>
          <div>
            <p className={eyebrowClass}>{mode === 'login' ? 'Welcome back' : 'Start reviewing drift'}</p>
            <h2 className="mb-2 text-3xl font-bold tracking-[-0.04em] text-[#16211e]">{mode === 'login' ? 'Sign in to your workspace' : 'Create your workspace'}</h2>
            <p className="m-0 leading-relaxed text-[#74807c]">
              {mode === 'login'
                ? 'Use the organization ID saved when you registered.'
                : 'The first account becomes the workspace owner.'}
            </p>
          </div>

          {mode === 'register' && (
            <label className={labelClass}>
              Organization name
              <input className={inputClass} name="organization_name" placeholder="Acme Inc." required />
            </label>
          )}

          {mode === 'login' && (
            <label className={labelClass}>
              Organization ID
              <input
                className={inputClass}
                name="organization_id"
                defaultValue={localStorage.getItem('business-drift-organization') ?? ''}
                placeholder="xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"
                required
              />
            </label>
          )}

          <label className={labelClass}>
            Work email
            <input
              className={inputClass}
              name="email"
              type="email"
              defaultValue={localStorage.getItem('business-drift-email') ?? ''}
              placeholder="you@company.com"
              autoComplete="email"
              required
            />
          </label>
          <label className={labelClass}>
            Password
            <input
              className={inputClass}
              name="password"
              type="password"
              placeholder="At least 12 characters"
              minLength={12}
              autoComplete={mode === 'login' ? 'current-password' : 'new-password'}
              required
            />
          </label>

          {error && <p className="m-0 rounded-[10px] bg-[#fff0ed] px-3.5 py-3 text-[13px] leading-relaxed text-[#8c2f2f]" role="alert">{error}</p>}

          <button className="cursor-pointer rounded-xl border-0 bg-[#1b5a47] px-4.5 py-3.5 font-extrabold text-white transition hover:bg-[#124938] disabled:cursor-wait disabled:opacity-60" type="submit" disabled={busy}>
            {busy ? 'Please wait…' : mode === 'login' ? 'Sign in' : 'Create workspace'}
          </button>
          <p className="m-0 text-center text-[13px] text-[#73807b]">
            {mode === 'login' ? 'New to Business Drift?' : 'Already have a workspace?'}{' '}
            <button className="cursor-pointer border-0 bg-transparent p-0 font-extrabold text-[#1b5a47]" type="button" onClick={() => changeMode(mode === 'login' ? 'register' : 'login')}>
              {mode === 'login' ? 'Create one' : 'Sign in'}
            </button>
          </p>
        </form>
      </section>
    </main>
  )
}
