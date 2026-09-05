import { useEffect, useState, type FormEvent } from 'react'
import {
  getFinding,
  getFindings,
  getOrganization,
  getStripe,
  ingestFixture,
  saveStripe,
  syncStripe,
} from '../api'
import type { Finding, FixtureInput, FixtureResult, Identity, Organization, StripeIntegration } from '../types'

type View = 'overview' | 'findings' | 'integration' | 'fixture'

type Props = {
  identity: Identity
  onLogout: () => Promise<void>
}

const writableRoles = new Set(['owner', 'admin'])

function messageFrom(error: unknown) {
  return error instanceof Error ? error.message : 'The request could not be completed.'
}

function formatDate(value?: string) {
  if (!value) return 'Not yet'
  return new Intl.DateTimeFormat('en', { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value))
}

function initials(value: string) {
  return value
    .split(/\s+/)
    .map((part) => part[0])
    .join('')
    .slice(0, 2)
    .toUpperCase()
}

export function Dashboard({ identity, onLogout }: Props) {
  const [view, setView] = useState<View>('overview')
  const [organization, setOrganization] = useState<Organization | null>(null)
  const [findings, setFindings] = useState<Finding[]>([])
  const [stripe, setStripe] = useState<StripeIntegration | null>(null)
  const [selectedFinding, setSelectedFinding] = useState<Finding | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  async function loadWorkspace() {
    setLoading(true)
    setError('')
    try {
      const [organizationResult, findingsResult, stripeResult] = await Promise.all([
        getOrganization(),
        getFindings(),
        getStripe(),
      ])
      setOrganization(organizationResult)
      setFindings(findingsResult.data)
      setStripe(stripeResult)
    } catch (requestError) {
      setError(messageFrom(requestError))
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    let cancelled = false

    Promise.all([getOrganization(), getFindings(), getStripe()])
      .then(([organizationResult, findingsResult, stripeResult]) => {
        if (cancelled) return
        setOrganization(organizationResult)
        setFindings(findingsResult.data)
        setStripe(stripeResult)
      })
      .catch((requestError: unknown) => {
        if (!cancelled) setError(messageFrom(requestError))
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })

    return () => {
      cancelled = true
    }
  }, [])

  async function openFinding(id: string) {
    setError('')
    try {
      setSelectedFinding(await getFinding(id))
    } catch (requestError) {
      setError(messageFrom(requestError))
    }
  }

  const openFindings = findings.filter((finding) => finding.status === 'open')
  const canManage = writableRoles.has(identity.role)

  return (
    <div className="app-shell">
      <aside className="sidebar">
        <a className="brand" href="/" aria-label="Business Drift home">
          <span className="brand-mark">BD</span>
          <span>Business Drift</span>
        </a>

        <nav className="main-nav" aria-label="Workspace navigation">
          <NavButton active={view === 'overview'} label="Overview" mark="O" onClick={() => setView('overview')} />
          <NavButton active={view === 'findings'} label="Findings" mark="F" count={openFindings.length} onClick={() => setView('findings')} />
          <NavButton active={view === 'integration'} label="Stripe" mark="S" onClick={() => setView('integration')} />
          {canManage && <NavButton active={view === 'fixture'} label="Demo data" mark="D" onClick={() => setView('fixture')} />}
        </nav>

        <div className="sidebar-user">
          <span className="avatar">{initials(identity.email)}</span>
          <span className="user-copy">
            <strong>{identity.email}</strong>
            <small>{identity.role}</small>
          </span>
          <button className="icon-button" type="button" onClick={() => void onLogout()} aria-label="Sign out">↗</button>
        </div>
      </aside>

      <main className="workspace">
        <header className="workspace-header">
          <div>
            <p className="eyebrow">{organization?.name ?? 'Workspace'}</p>
            <h1>{viewTitle(view)}</h1>
          </div>
          <button className="secondary-button refresh-button" type="button" onClick={() => void loadWorkspace()} disabled={loading}>
            {loading ? 'Refreshing…' : 'Refresh data'}
          </button>
        </header>

        {error && <p className="page-message error-message" role="alert">{error}</p>}
        {loading && !organization ? (
          <LoadingRows />
        ) : (
          <>
            {view === 'overview' && (
              <Overview
                findings={findings}
                stripe={stripe}
                onSeeFindings={() => setView('findings')}
                onOpenFinding={openFinding}
              />
            )}
            {view === 'findings' && <FindingsView findings={findings} onOpenFinding={openFinding} />}
            {view === 'integration' && (
              <StripeView integration={stripe} canManage={canManage} onChanged={setStripe} />
            )}
            {view === 'fixture' && canManage && <FixtureView onCreated={loadWorkspace} />}
          </>
        )}
      </main>

      {selectedFinding && <FindingPanel finding={selectedFinding} onClose={() => setSelectedFinding(null)} />}
    </div>
  )
}

function NavButton({ active, label, mark, count, onClick }: { active: boolean; label: string; mark: string; count?: number; onClick: () => void }) {
  return (
    <button className={`nav-button${active ? ' active' : ''}`} type="button" onClick={onClick}>
      <span className="nav-mark">{mark}</span>
      <span>{label}</span>
      {count !== undefined && count > 0 && <span className="nav-count">{count}</span>}
    </button>
  )
}

function viewTitle(view: View) {
  return {
    overview: 'Revenue health overview',
    findings: 'Review findings',
    integration: 'Stripe integration',
    fixture: 'Create demo evidence',
  }[view]
}

function Overview({ findings, stripe, onSeeFindings, onOpenFinding }: {
  findings: Finding[]
  stripe: StripeIntegration | null
  onSeeFindings: () => void
  onOpenFinding: (id: string) => void
}) {
  const open = findings.filter((finding) => finding.status === 'open')
  const highRisk = open.filter((finding) => finding.risk === 'high')

  return (
    <div className="content-stack">
      <section className="metric-grid" aria-label="Workspace metrics">
        <MetricCard label="Open findings" value={String(open.length)} note="Need review" tone="lime" />
        <MetricCard label="High risk" value={String(highRisk.length)} note="Prioritize these" tone="coral" />
        <MetricCard label="Stripe" value={stripe?.status ?? 'Not connected'} note={stripe ? `Synced ${formatDate(stripe.last_synced_at)}` : 'Add sandbox credentials'} />
      </section>

      <section className="surface">
        <div className="section-heading">
          <div>
            <p className="eyebrow">Latest signals</p>
            <h2>Recent findings</h2>
          </div>
          <button className="text-button" type="button" onClick={onSeeFindings}>View all</button>
        </div>
        <FindingRows findings={findings.slice(0, 5)} onOpenFinding={onOpenFinding} />
      </section>
    </div>
  )
}

function MetricCard({ label, value, note, tone = '' }: { label: string; value: string; note: string; tone?: string }) {
  return (
    <article className={`metric-card ${tone}`}>
      <span>{label}</span>
      <strong>{value}</strong>
      <small>{note}</small>
    </article>
  )
}

function FindingsView({ findings, onOpenFinding }: { findings: Finding[]; onOpenFinding: (id: string) => void }) {
  const [filter, setFilter] = useState<'all' | 'open' | 'resolved'>('open')
  const visible = filter === 'all' ? findings : findings.filter((finding) => finding.status === filter)

  return (
    <section className="surface">
      <div className="section-heading findings-heading">
        <div>
          <p className="eyebrow">Evidence-backed</p>
          <h2>Customer mismatches</h2>
        </div>
        <div className="filter-group" aria-label="Filter findings">
          {(['open', 'resolved', 'all'] as const).map((option) => (
            <button key={option} className={filter === option ? 'active' : ''} type="button" onClick={() => setFilter(option)}>
              {option}
            </button>
          ))}
        </div>
      </div>
      <FindingRows findings={visible} onOpenFinding={onOpenFinding} />
    </section>
  )
}

function FindingRows({ findings, onOpenFinding }: { findings: Finding[]; onOpenFinding: (id: string) => void }) {
  if (findings.length === 0) {
    return <EmptyState title="No findings here" body="Ingest demo data or sync Stripe to evaluate customer state." />
  }

  return (
    <div className="finding-list">
      {findings.map((finding) => (
        <button className="finding-row" type="button" key={finding.id} onClick={() => onOpenFinding(finding.id)}>
          <span className={`risk-dot ${finding.risk}`} aria-label={`${finding.risk} risk`} />
          <span className="finding-main">
            <strong>{finding.title}</strong>
            <small>{finding.customer_name} · {finding.rule_name}</small>
          </span>
          <span className={`status-pill ${finding.status}`}>{finding.status}</span>
          <time>{formatDate(finding.last_detected_at)}</time>
          <span className="row-arrow">→</span>
        </button>
      ))}
    </div>
  )
}

function StripeView({ integration, canManage, onChanged }: {
  integration: StripeIntegration | null
  canManage: boolean
  onChanged: (integration: StripeIntegration) => void
}) {
  const [busy, setBusy] = useState(false)
  const [message, setMessage] = useState('')
  const [error, setError] = useState('')

  async function handleSave(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setBusy(true)
    setError('')
    setMessage('')
    const values = new FormData(event.currentTarget)
    try {
      const result = await saveStripe({
        api_key: String(values.get('api_key') ?? ''),
        webhook_secret: String(values.get('webhook_secret') ?? ''),
      })
      onChanged(result)
      event.currentTarget.reset()
      setMessage('Sandbox credentials saved securely.')
    } catch (requestError) {
      setError(messageFrom(requestError))
    } finally {
      setBusy(false)
    }
  }

  async function handleSync() {
    setBusy(true)
    setError('')
    setMessage('')
    try {
      const result = await syncStripe()
      setMessage(`Sync queued. Job ${result.job_id.slice(0, 8)} is ${result.status}.`)
    } catch (requestError) {
      setError(messageFrom(requestError))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="two-column">
      <section className="surface integration-summary">
        <div className="integration-logo">S</div>
        <div>
          <p className="eyebrow">Billing source</p>
          <h2>Stripe sandbox</h2>
          <p className="muted">Import customers and subscriptions, then receive ongoing updates by webhook.</p>
        </div>
        <dl className="detail-list">
          <div><dt>Status</dt><dd><span className={`status-pill ${integration?.status === 'active' ? 'open' : 'resolved'}`}>{integration?.status ?? 'not configured'}</span></dd></div>
          <div><dt>Last sync</dt><dd>{formatDate(integration?.last_synced_at)}</dd></div>
          {integration?.webhook_path && <div><dt>Webhook path</dt><dd><code>{integration.webhook_path}</code></dd></div>}
        </dl>
        {integration && canManage && <button className="primary-button" type="button" onClick={() => void handleSync()} disabled={busy}>Queue Stripe sync</button>}
      </section>

      <section className="surface">
        <div className="section-heading compact">
          <div>
            <p className="eyebrow">Configuration</p>
            <h2>{integration ? 'Replace credentials' : 'Connect Stripe'}</h2>
          </div>
        </div>
        {canManage ? (
          <form className="stack-form" onSubmit={handleSave}>
            <p className="muted">Only test or restricted test keys are accepted. Secrets are encrypted by the backend.</p>
            <label>Sandbox API key<input name="api_key" type="password" placeholder="sk_test_…" autoComplete="off" required /></label>
            <label>Webhook signing secret<input name="webhook_secret" type="password" placeholder="whsec_…" autoComplete="off" required /></label>
            {error && <p className="form-message error-message" role="alert">{error}</p>}
            {message && <p className="form-message success-message" role="status">{message}</p>}
            <button className="primary-button" type="submit" disabled={busy}>{busy ? 'Saving…' : 'Save configuration'}</button>
          </form>
        ) : (
          <EmptyState title="View-only access" body="An owner or admin can update Stripe credentials and start a sync." />
        )}
      </section>
    </div>
  )
}

function FixtureView({ onCreated }: { onCreated: () => Promise<void> }) {
  const [busy, setBusy] = useState(false)
  const [result, setResult] = useState<FixtureResult | null>(null)
  const [error, setError] = useState('')

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setBusy(true)
    setError('')
    setResult(null)
    const values = new FormData(event.currentTarget)
    const input = Object.fromEntries(values) as FixtureInput
    try {
      setResult(await ingestFixture(input))
      await onCreated()
    } catch (requestError) {
      setError(messageFrom(requestError))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="two-column fixture-layout">
      <section className="surface">
        <div className="section-heading compact">
          <div><p className="eyebrow">Development only</p><h2>Ingest fixture event</h2></div>
        </div>
        <form className="stack-form" onSubmit={handleSubmit}>
          <label>Customer name<input name="customer_name" defaultValue="Northstar Labs" required /></label>
          <div className="field-pair">
            <label>Stripe customer ID<input name="stripe_customer_id" defaultValue="cus_demo_001" required /></label>
            <label>HubSpot company ID<input name="hubspot_company_id" defaultValue="company_demo_001" required /></label>
          </div>
          <div className="field-pair">
            <label>Stripe subscription
              <select name="stripe_subscription_status" defaultValue="active">
                <option value="active">Active</option><option value="past_due">Past due</option><option value="canceled">Canceled</option>
              </select>
            </label>
            <label>HubSpot customer
              <select name="hubspot_customer_status" defaultValue="churned">
                <option value="active">Active</option><option value="inactive">Inactive</option><option value="churned">Churned</option><option value="unknown">Unknown</option>
              </select>
            </label>
          </div>
          {error && <p className="form-message error-message" role="alert">{error}</p>}
          <button className="primary-button" type="submit" disabled={busy}>{busy ? 'Evaluating…' : 'Ingest and evaluate'}</button>
        </form>
      </section>
      <section className="surface fixture-explainer">
        <p className="eyebrow">What this proves</p>
        <h2>One small end-to-end path</h2>
        <ol>
          <li>Map Stripe and HubSpot IDs to one customer.</li>
          <li>Store normalized facts from both sources.</li>
          <li>Run the mismatch rule and save its evidence.</li>
        </ol>
        {result && (
          <div className="result-card" role="status">
            <strong>{result.outcome === 'finding_open' ? 'Finding opened' : 'No mismatch found'}</strong>
            <span>Customer {result.customer_id.slice(0, 8)}</span>
          </div>
        )}
      </section>
    </div>
  )
}

function FindingPanel({ finding, onClose }: { finding: Finding; onClose: () => void }) {
  return (
    <div className="drawer-backdrop" role="presentation" onMouseDown={onClose}>
      <aside className="finding-drawer" role="dialog" aria-modal="true" aria-labelledby="finding-title" onMouseDown={(event) => event.stopPropagation()}>
        <button className="drawer-close" type="button" onClick={onClose} aria-label="Close finding">×</button>
        <p className="eyebrow">{finding.risk} risk · {finding.status}</p>
        <h2 id="finding-title">{finding.title}</h2>
        <p className="drawer-explanation">{finding.explanation}</p>
        <dl className="detail-list">
          <div><dt>Customer</dt><dd>{finding.customer_name}</dd></div>
          <div><dt>Rule</dt><dd>{finding.rule_name} v{finding.rule_version}</dd></div>
          <div><dt>First detected</dt><dd>{formatDate(finding.first_detected_at)}</dd></div>
          <div><dt>Last detected</dt><dd>{formatDate(finding.last_detected_at)}</dd></div>
        </dl>
        <div className="evidence-section">
          <p className="eyebrow">Evidence</p>
          {(finding.evidence ?? []).map((evidence) => (
            <article className="evidence-card" key={evidence.id}>
              <div><strong>{evidence.source}</strong><span>{evidence.fact_type}</span></div>
              <code>{JSON.stringify(evidence.value)}</code>
              <time>{formatDate(evidence.observed_at)}</time>
            </article>
          ))}
          {!finding.evidence?.length && <p className="muted">No evidence was returned for this finding.</p>}
        </div>
      </aside>
    </div>
  )
}

function EmptyState({ title, body }: { title: string; body: string }) {
  return <div className="empty-state"><span>◎</span><strong>{title}</strong><p>{body}</p></div>
}

function LoadingRows() {
  return <div className="surface loading-rows" aria-label="Loading workspace"><span /><span /><span /></div>
}
