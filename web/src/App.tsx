import { useEffect, useState } from 'react'
import { getMe, loadSession, logout, saveSession } from './api'
import { AuthView } from './components/AuthView'
import { Dashboard } from './components/Dashboard'
import type { Identity, Session } from './types'

function App() {
  const [session, setSession] = useState<Session | null>(() => loadSession())
  const [identity, setIdentity] = useState<Identity | null>(null)
  const [checkingSession, setCheckingSession] = useState(Boolean(session))

  useEffect(() => {
    if (!session) return

    getMe()
      .then(setIdentity)
      .catch(() => {
        saveSession(null)
        setSession(null)
      })
      .finally(() => setCheckingSession(false))
  }, [session])

  function handleAuthenticated(nextIdentity: Identity, nextSession: Session) {
    saveSession(nextSession)
    setIdentity(nextIdentity)
    setSession(nextSession)
  }

  async function handleLogout() {
    await logout()
    setIdentity(null)
    setSession(null)
  }

  if (checkingSession) {
    return (
      <main className="grid min-h-svh place-content-center justify-items-center gap-3.5 bg-[#f3f1ea] text-[#63716c]">
        <span className="grid size-9.5 place-items-center rounded-xl bg-[#c9f269] text-xs font-extrabold tracking-wider text-[#10201b]">BD</span>
        <p className="m-0 text-sm">Opening your workspace…</p>
      </main>
    )
  }

  if (!session || !identity) return <AuthView onAuthenticated={handleAuthenticated} />

  return <Dashboard identity={identity} onLogout={handleLogout} />
}

export default App
