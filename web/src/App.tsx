import { useEffect, useState } from 'react'
import { getMe, loadSession, logout, saveSession } from './api'
import { AuthView } from './components/AuthView'
import { Dashboard } from './components/Dashboard'
import type { Identity, Session } from './types'
import './App.css'

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
      <main className="loading-screen">
        <span className="brand-mark">BD</span>
        <p>Opening your workspace…</p>
      </main>
    )
  }

  if (!session || !identity) return <AuthView onAuthenticated={handleAuthenticated} />

  return <Dashboard identity={identity} onLogout={handleLogout} />
}

export default App
