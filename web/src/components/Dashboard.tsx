import { useEffect, useState, type FormEvent } from 'react'
import {
  getFinding,
  getFindings,
  getHubSpot,
  getOrganization,
  getStripe,
  saveHubSpot,
  saveStripe,
  syncHubSpot,
  syncStripe,
} from '../api'
import type { Finding, HubSpotIntegration, Identity, Organization, StripeIntegration } from '../types'

type View = 'overview' | 'findings' | 'stripe' | 'hubspot'

type Props = {
  identity: Identity
  onLogout: () => Promise<void>
}

const writableRoles = new Set(['owner', 'admin'])
const eyebrowClass = 'mb-3.5 text-xs font-extrabold tracking-[0.14em] uppercase text-[#7b8f88]'
const surfaceClass = 'rounded-[18px] border border-[#dbd9d0] bg-[#fffefa] p-4 shadow-[0_10px_36px_rgba(39,48,44,0.035)] sm:p-6.5'
const primaryButtonClass = 'cursor-pointer rounded-xl border-0 bg-[#1b5a47] px-4.5 py-3.5 font-extrabold text-white transition hover:bg-[#124938] disabled:cursor-wait disabled:opacity-60'
const secondaryButtonClass = 'cursor-pointer rounded-[10px] border border-[#d2d6d1] bg-[#fffefa] px-3.5 py-2.5 text-xs font-extrabold text-[#30413b] transition hover:border-[#9da9a4] disabled:cursor-wait disabled:opacity-60'
const inputClass = 'w-full rounded-xl border border-[#d6d9d5] bg-white px-3.5 py-3 text-[#14201c] outline-none transition focus:border-[#39745f] focus:ring-3 focus:ring-[#39745f]/12'
const labelClass = 'grid gap-2 text-[13px] font-bold text-[#36433f]'

function messageFrom(error: unknown) {
  return error instanceof Error ? error.message : 'The request could not be completed.'
}

function formatDate(value?: string) {
  if (!value) return 'Not yet'
  return new Intl.DateTimeFormat('en', { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value))
}

function integrationStatus(status?: string) {
  if (!status) return 'Not connected'
  if (status === 'active') return 'Connected'
  return status
}

function ruleLabel(ruleName: string) {
  return {
    status_mismatch: 'Status mismatch',
    missing_in_hubspot: 'Missing in HubSpot',
    missing_in_stripe: 'Missing in Stripe',
  }[ruleName] ?? ruleName.replaceAll('_', ' ')
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
  const [hubSpot, setHubSpot] = useState<HubSpotIntegration | null>(null)
  const [selectedFinding, setSelectedFinding] = useState<Finding | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  async function loadWorkspace() {
    setLoading(true)
    setError('')
    try {
      const [organizationResult, findingsResult, stripeResult, hubSpotResult] = await Promise.all([
        getOrganization(),
        getFindings(),
        getStripe(),
        getHubSpot(),
      ])
      setOrganization(organizationResult)
      setFindings(findingsResult.data)
      setStripe(stripeResult)
      setHubSpot(hubSpotResult)
    } catch (requestError) {
      setError(messageFrom(requestError))
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    let cancelled = false

    Promise.all([getOrganization(), getFindings(), getStripe(), getHubSpot()])
      .then(([organizationResult, findingsResult, stripeResult, hubSpotResult]) => {
        if (cancelled) return
        setOrganization(organizationResult)
        setFindings(findingsResult.data)
        setStripe(stripeResult)
        setHubSpot(hubSpotResult)
      })
      .catch((requestError: unknown) => {
        if (!cancelled) setError(messageFrom(requestError))
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })

    let refreshing = false
    const refresh = window.setInterval(() => {
      if (document.hidden || refreshing) return
      refreshing = true
      void Promise.all([getFindings(), getStripe(), getHubSpot()])
        .then(([result, stripeResult, hubSpotResult]) => {
          if (cancelled) return
          setFindings(result.data)
          setStripe(stripeResult)
          setHubSpot(hubSpotResult)
        })
        .catch((requestError: unknown) => { if (!cancelled) setError(messageFrom(requestError)) })
        .finally(() => { refreshing = false })
    }, 10000)

    return () => {
      cancelled = true
      window.clearInterval(refresh)
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
    <div className="min-h-svh bg-[#f3f1ea] pb-20 lg:pb-0 lg:pl-[244px]">
      <aside className="fixed inset-x-0 bottom-0 z-20 flex h-[68px] items-center bg-[#101d1a] px-3 py-2 text-[#f8f7f2] lg:inset-y-0 lg:left-0 lg:right-auto lg:h-auto lg:w-[244px] lg:flex-col lg:items-stretch lg:px-4.5 lg:pt-6.5 lg:pb-5">
        <a className="hidden w-fit items-center gap-3 px-2 font-bold tracking-[-0.02em] text-inherit no-underline lg:inline-flex" href="/" aria-label="Business Drift home">
          <span className="grid size-9.5 place-items-center rounded-xl bg-[#c9f269] text-xs font-extrabold tracking-wider text-[#10201b]">BD</span>
          <span>Business Drift</span>
        </a>

        <nav className="flex w-full justify-around gap-1 lg:mt-12 lg:grid lg:gap-1" aria-label="Workspace navigation">
          <NavButton active={view === 'overview'} label="Overview" mark="O" onClick={() => setView('overview')} />
          <NavButton active={view === 'findings'} label="Findings" mark="F" count={openFindings.length} onClick={() => setView('findings')} />
          <NavButton active={view === 'stripe'} label="Stripe" mark="S" onClick={() => setView('stripe')} />
          <NavButton active={view === 'hubspot'} label="HubSpot" mark="H" onClick={() => setView('hubspot')} />
        </nav>

        <div className="mt-auto hidden grid-cols-[34px_minmax(0,1fr)_28px] items-center gap-2 border-t border-[#283a34] px-1 pt-4 lg:grid">
          <span className="grid size-[34px] place-items-center rounded-[10px] bg-[#d8e9dc] text-[11px] font-extrabold text-[#18251f]">{initials(identity.email)}</span>
          <span className="min-w-0">
            <strong className="block overflow-hidden text-[11px] text-ellipsis whitespace-nowrap">{identity.email}</strong>
            <small className="mt-0.5 block overflow-hidden text-[10px] text-ellipsis whitespace-nowrap text-[#758982] capitalize">{identity.role}</small>
          </span>
          <button className="cursor-pointer border-0 bg-transparent p-1 text-lg text-[#82958e]" type="button" onClick={() => void onLogout()} aria-label="Sign out">↗</button>
        </div>
      </aside>

      <main className="mx-auto w-full max-w-[1440px] px-4 pt-6 pb-10 sm:px-6 lg:px-[clamp(1.5rem,4vw,4rem)] lg:pt-9.5 lg:pb-17.5">
        <header className="mb-6 flex items-end justify-between gap-4 lg:mb-8.5 lg:items-center lg:gap-6">
          <div>
            <p className="mb-1.5 hidden text-xs font-extrabold tracking-[0.14em] text-[#7b8f88] uppercase sm:block">{organization?.name ?? 'Workspace'}</p>
            <h1 className="m-0 text-2xl font-semibold tracking-[-0.045em] text-[#14201c] sm:text-3xl lg:text-[42px]">{viewTitle(view)}</h1>
          </div>
          <button className={secondaryButtonClass} type="button" onClick={() => void loadWorkspace()} disabled={loading}>
            {loading ? 'Refreshing…' : 'Refresh data'}
          </button>
        </header>

        {error && <p className="mb-6 rounded-[10px] bg-[#fff0ed] px-3.5 py-3 text-[13px] leading-relaxed text-[#8c2f2f]" role="alert">{error}</p>}
        {loading && !organization ? (
          <LoadingRows />
        ) : (
          <>
            {view === 'overview' && (
              <Overview
                findings={findings}
                stripe={stripe}
                hubSpot={hubSpot}
                onSeeFindings={() => setView('findings')}
                onOpenFinding={openFinding}
              />
            )}
            {view === 'findings' && <FindingsView findings={findings} onOpenFinding={openFinding} />}
            {view === 'stripe' && (
              <StripeView integration={stripe} canManage={canManage} onChanged={setStripe} />
            )}
            {view === 'hubspot' && (
              <HubSpotView integration={hubSpot} canManage={canManage} onChanged={setHubSpot} />
            )}
          </>
        )}
      </main>

      {selectedFinding && <FindingPanel finding={selectedFinding} onClose={() => setSelectedFinding(null)} />}
    </div>
  )
}

function NavButton({ active, label, mark, count, onClick }: { active: boolean; label: string; mark: string; count?: number; onClick: () => void }) {
  return (
    <button
      className={`relative flex min-w-[62px] cursor-pointer flex-col items-center gap-1 rounded-xl border-0 px-2 py-1 text-center text-[9px] font-bold transition lg:grid lg:w-full lg:grid-cols-[28px_1fr_auto] lg:gap-2.5 lg:p-2.5 lg:text-left lg:text-sm ${active ? 'bg-[#1d312b] text-[#f7f6f1]' : 'bg-transparent text-[#8fa29b] hover:bg-white/5 hover:text-[#f7f6f1]'}`}
      type="button"
      onClick={onClick}
    >
      <span className={`grid size-6.5 place-items-center rounded-lg border text-[10px] ${active ? 'border-[#c9f269] bg-[#c9f269] text-[#15221e]' : 'border-[#344841]'}`}>{mark}</span>
      <span>{label}</span>
      {count !== undefined && count > 0 && <span className="absolute mt-[-3px] ml-7 min-w-5.5 rounded-full bg-[#c9f269] px-1.5 py-0.5 text-center text-[10px] text-[#14201c] lg:static lg:m-0">{count}</span>}
    </button>
  )
}

function viewTitle(view: View) {
  return {
    overview: 'Customer drift overview',
    findings: 'Review findings',
    stripe: 'Stripe integration',
    hubspot: 'HubSpot integration',
  }[view]
}

function Overview({ findings, stripe, hubSpot, onSeeFindings, onOpenFinding }: {
  findings: Finding[]
  stripe: StripeIntegration | null
  hubSpot: HubSpotIntegration | null
  onSeeFindings: () => void
  onOpenFinding: (id: string) => void
}) {
  const open = findings.filter((finding) => finding.status === 'open')
  const statusMismatches = open.filter((finding) => finding.rule_name === 'status_mismatch')
  const missingRecords = open.filter((finding) => finding.rule_name.startsWith('missing_in_'))
  const connectedSources = [stripe, hubSpot].filter((integration) => integration?.status === 'active').length

  return (
    <div className="grid gap-6">
      <section className="grid grid-cols-2 gap-4 lg:grid-cols-4" aria-label="Workspace metrics">
        <MetricCard label="Open findings" value={String(open.length)} note="Need review" tone="lime" />
        <MetricCard label="Status mismatches" value={String(statusMismatches.length)} note="Different customer states" tone="coral" />
        <MetricCard label="Missing records" value={String(missingRecords.length)} note="Present in only one source" tone="amber" />
        <MetricCard label="Sources connected" value={`${connectedSources}/2`} note={connectedSources === 2 ? 'Stripe and HubSpot ready' : 'Connect both data sources'} />
      </section>

      <section className={surfaceClass}>
        <div className="mb-5 flex items-center justify-between gap-5">
          <div>
            <p className="mb-1.5 text-xs font-extrabold tracking-[0.14em] text-[#7b8f88] uppercase">Latest signals</p>
            <h2 className="m-0 text-[23px] font-semibold tracking-[-0.035em] text-[#18231f]">Recent findings</h2>
          </div>
          <button className="cursor-pointer border-0 bg-transparent p-2 text-xs font-extrabold text-[#1b5a47]" type="button" onClick={onSeeFindings}>View all</button>
        </div>
        <FindingRows findings={findings.slice(0, 5)} onOpenFinding={onOpenFinding} />
      </section>
    </div>
  )
}

function MetricCard({ label, value, note, tone = '' }: { label: string; value: string; note: string; tone?: string }) {
  const toneClass = {
    lime: 'border-[#c9f269] bg-[#c9f269] after:bg-white/30',
    coral: 'after:bg-[#ffe3da]',
    amber: 'after:bg-[#fff0cf]',
  }[tone] ?? 'after:bg-[#eef1ed]'

  return (
    <article className={`relative grid min-h-[130px] content-between overflow-hidden rounded-[17px] border border-[#dbd9d0] bg-[#fffefa] p-4 text-[#14201c] after:absolute after:-right-7.5 after:-bottom-11 after:size-[110px] after:rounded-full sm:min-h-[164px] sm:p-5.5 ${toneClass}`}>
      <span className={`text-xs font-extrabold ${tone === 'lime' ? 'text-[#42542f]' : 'text-[#65726d]'}`}>{label}</span>
      <strong className="relative z-10 self-center text-[clamp(1.75rem,3vw,2.625rem)] font-semibold tracking-[-0.04em] capitalize">{value}</strong>
      <small className={`relative z-10 ${tone === 'lime' ? 'text-[#526239]' : 'text-[#78837f]'}`}>{note}</small>
    </article>
  )
}

function FindingsView({ findings, onOpenFinding }: { findings: Finding[]; onOpenFinding: (id: string) => void }) {
  const [filter, setFilter] = useState<'all' | 'open' | 'resolved'>('open')
  const visible = filter === 'all' ? findings : findings.filter((finding) => finding.status === filter)

  return (
    <section className={surfaceClass}>
      <div className="mb-5 flex flex-col items-start justify-between gap-5 sm:flex-row sm:items-center">
        <div>
          <p className="mb-1.5 text-xs font-extrabold tracking-[0.14em] text-[#7b8f88] uppercase">Evidence-backed</p>
          <h2 className="m-0 text-[23px] font-semibold tracking-[-0.035em] text-[#18231f]">Customer drift findings</h2>
        </div>
        <div className="inline-flex rounded-[10px] bg-[#f0efe9] p-1" aria-label="Filter findings">
          {(['open', 'resolved', 'all'] as const).map((option) => (
            <button key={option} className={`cursor-pointer rounded-lg border-0 px-2.5 py-2 text-[11px] font-extrabold capitalize ${filter === option ? 'bg-[#fffefa] text-[#24322d] shadow-sm' : 'bg-transparent text-[#74807b]'}`} type="button" onClick={() => setFilter(option)}>
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
    return <EmptyState title="No findings here" body="Sync Stripe first, then HubSpot to compare customer records." />
  }

  return (
    <div className="-mx-2.5 -mb-2.5">
      {findings.map((finding) => (
        <button className="grid w-full cursor-pointer grid-cols-[8px_minmax(0,1fr)_16px] items-center gap-3 border-0 border-t border-[#edebe5] bg-transparent px-2.5 py-4 text-left text-[#1d2925] first:border-t-0 hover:rounded-[10px] hover:bg-[#f7f7f2] sm:grid-cols-[10px_minmax(220px,1fr)_auto_20px] sm:gap-4 xl:grid-cols-[10px_minmax(220px,1fr)_auto_150px_20px]" type="button" key={finding.id} onClick={() => onOpenFinding(finding.id)}>
          <span className={`size-2 rounded-full ${finding.risk === 'high' ? 'bg-[#df664f] shadow-[0_0_0_4px_#fcebe6]' : finding.risk === 'medium' ? 'bg-[#d89d3a] shadow-[0_0_0_4px_#fff2d8]' : 'bg-[#76a18f] shadow-[0_0_0_4px_#edf3f0]'}`} aria-label={`${finding.risk} risk`} />
          <span className="min-w-0">
            <strong className="block overflow-hidden text-[13px] text-ellipsis whitespace-nowrap">{finding.title}</strong>
            <small className="mt-1 block overflow-hidden text-[11px] text-ellipsis whitespace-nowrap text-[#7a8581]">{finding.customer_name} · {ruleLabel(finding.rule_name)}</small>
          </span>
          <span className={`hidden w-fit rounded-full px-2 py-1 text-[10px] font-extrabold capitalize sm:inline-flex ${finding.status === 'open' ? 'bg-[#ffe6de] text-[#81402f]' : 'bg-[#e5f2e9] text-[#426657]'}`}>{finding.status}</span>
          <time className="hidden text-[11px] text-[#7b8581] xl:block">{formatDate(finding.last_detected_at)}</time>
          <span className="text-[#81908a]">→</span>
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
    const form = event.currentTarget
    setBusy(true)
    setError('')
    setMessage('')
    const values = new FormData(form)
    try {
      const result = await saveStripe({
        api_key: String(values.get('api_key') ?? ''),
        webhook_secret: String(values.get('webhook_secret') ?? ''),
      })
      onChanged(result)
      form.reset()
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
      await syncStripe()
      setMessage('Sync requested. The last sync time and findings will update automatically.')
    } catch (requestError) {
      setError(messageFrom(requestError))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="grid items-start gap-5 xl:grid-cols-[minmax(0,0.9fr)_minmax(380px,1.1fr)]">
      <section className={`${surfaceClass} grid gap-6`}>
        <div className="grid size-13 place-items-center rounded-[15px] bg-[#6655c7] text-[23px] font-extrabold text-white">S</div>
        <div>
          <p className={eyebrowClass}>Billing source</p>
          <h2 className="m-0 text-[23px] font-semibold tracking-[-0.035em] text-[#18231f]">Stripe sandbox</h2>
          <p className="m-0 leading-relaxed text-[#74807c]">Sync once to import Stripe test data, then use webhooks to keep subscription status current.</p>
        </div>
        <dl className="m-0 grid">
          <div className="grid grid-cols-[92px_minmax(0,1fr)] gap-4 border-t border-[#eceae4] py-3 sm:grid-cols-[120px_minmax(0,1fr)]"><dt className="text-[11px] text-[#7b8681]">Status</dt><dd className="m-0 min-w-0 text-xs font-bold text-[#293732]"><span className={`inline-flex w-fit rounded-full px-2 py-1 text-[10px] font-extrabold capitalize ${integration?.status === 'active' ? 'bg-[#e5f2e9] text-[#426657]' : 'bg-[#ffe6de] text-[#81402f]'}`}>{integrationStatus(integration?.status)}</span></dd></div>
          <div className="grid grid-cols-[92px_minmax(0,1fr)] gap-4 border-t border-[#eceae4] py-3 sm:grid-cols-[120px_minmax(0,1fr)]"><dt className="text-[11px] text-[#7b8681]">Last sync</dt><dd className="m-0 min-w-0 text-xs font-bold text-[#293732]">{formatDate(integration?.last_synced_at)}</dd></div>
        </dl>
        {integration?.status === 'error' && <p className="m-0 text-sm text-[#8c2f2f]" role="status">The last sync failed. The background worker will retry automatically.</p>}
        {integration && canManage && <button className={primaryButtonClass} type="button" onClick={() => void handleSync()} disabled={busy}>{busy ? 'Syncing…' : 'Sync Stripe'}</button>}
      </section>

      <section className={surfaceClass}>
        <div className="mb-6 flex items-center justify-between gap-5">
          <div>
            <p className={eyebrowClass}>Configuration</p>
            <h2 className="m-0 text-[23px] font-semibold tracking-[-0.035em] text-[#18231f]">{integration ? 'Replace credentials' : 'Connect Stripe'}</h2>
          </div>
        </div>
        {canManage ? (
          <form className="grid gap-4.5" onSubmit={handleSave}>
            <p className="m-0 leading-relaxed text-[#74807c]">Only test keys are accepted. The backend encrypts the key.</p>
            <label className={labelClass}>Sandbox API key<input className={inputClass} name="api_key" type="password" placeholder="sk_test_…" autoComplete="off" required /></label>
            <label className={labelClass}>Webhook signing secret<input className={inputClass} name="webhook_secret" type="password" placeholder="whsec_…" autoComplete="off" /></label>
            <p className="m-0 text-sm text-[#74807c]">Leave blank to keep the existing secret. Use manual sync for the initial import and reconciliation.</p>
            {integration && <p className="m-0 break-all text-sm text-[#74807c]">Webhook endpoint: <code>/api/v1/webhooks/stripe/{integration.id}</code>. Register this path on your public backend URL for subscription created, updated, and deleted events.</p>}
            {error && <p className="m-0 rounded-[10px] bg-[#fff0ed] px-3.5 py-3 text-[13px] leading-relaxed text-[#8c2f2f]" role="alert">{error}</p>}
            {message && <p className="m-0 rounded-[10px] bg-[#ecf8ed] px-3.5 py-3 text-[13px] leading-relaxed text-[#245c48]" role="status">{message}</p>}
            <button className={primaryButtonClass} type="submit" disabled={busy}>{busy ? 'Saving…' : 'Save configuration'}</button>
          </form>
        ) : (
          <EmptyState title="View-only access" body="An owner or admin can update Stripe credentials and start a sync." />
        )}
      </section>
    </div>
  )
}

function HubSpotView({ integration, canManage, onChanged }: {
  integration: HubSpotIntegration | null
  canManage: boolean
  onChanged: (integration: HubSpotIntegration) => void
}) {
  const [busy, setBusy] = useState(false)
  const [message, setMessage] = useState('')
  const [error, setError] = useState('')

  async function handleSave(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const form = event.currentTarget
    setBusy(true)
    setError('')
    setMessage('')
    const values = new FormData(form)
    try {
      const result = await saveHubSpot({
        access_token: String(values.get('access_token') ?? ''),
      })
      onChanged(result)
      form.reset()
      setMessage('HubSpot connection saved securely.')
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
      await syncHubSpot()
      setMessage('Sync requested. The last sync time and findings will update automatically.')
    } catch (requestError) {
      setError(messageFrom(requestError))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="grid items-start gap-5 xl:grid-cols-[minmax(0,0.9fr)_minmax(380px,1.1fr)]">
      <section className={`${surfaceClass} grid gap-6`}>
        <div className="grid size-13 place-items-center rounded-[15px] bg-[#ff7a59] text-[23px] font-extrabold text-white">H</div>
        <div>
          <p className={eyebrowClass}>CRM source</p>
          <h2 className="m-0 text-[23px] font-semibold tracking-[-0.035em] text-[#18231f]">HubSpot</h2>
          <p className="m-0 leading-relaxed text-[#74807c]">Import companies, match customer records, and detect status or presence differences.</p>
        </div>
        <dl className="m-0 grid">
          <div className="grid grid-cols-[92px_minmax(0,1fr)] gap-4 border-t border-[#eceae4] py-3 sm:grid-cols-[120px_minmax(0,1fr)]"><dt className="text-[11px] text-[#7b8681]">Status</dt><dd className="m-0 min-w-0 text-xs font-bold text-[#293732]"><span className={`inline-flex w-fit rounded-full px-2 py-1 text-[10px] font-extrabold capitalize ${integration?.status === 'active' ? 'bg-[#e5f2e9] text-[#426657]' : 'bg-[#ffe6de] text-[#81402f]'}`}>{integrationStatus(integration?.status)}</span></dd></div>
          <div className="grid grid-cols-[92px_minmax(0,1fr)] gap-4 border-t border-[#eceae4] py-3 sm:grid-cols-[120px_minmax(0,1fr)]"><dt className="text-[11px] text-[#7b8681]">Last sync</dt><dd className="m-0 min-w-0 text-xs font-bold text-[#293732]">{formatDate(integration?.last_synced_at)}</dd></div>
        </dl>
        {integration?.status === 'error' && <p className="m-0 text-sm text-[#8c2f2f]" role="status">The last sync failed. The background worker will retry automatically.</p>}
        {integration && canManage && <button className={primaryButtonClass} type="button" onClick={() => void handleSync()} disabled={busy}>{busy ? 'Syncing…' : 'Sync HubSpot companies'}</button>}
        {message && <p className="m-0 rounded-[10px] bg-[#ecf8ed] px-3.5 py-3 text-[13px] leading-relaxed text-[#245c48]" role="status">{message}</p>}
        {error && <p className="m-0 rounded-[10px] bg-[#fff0ed] px-3.5 py-3 text-[13px] leading-relaxed text-[#8c2f2f]" role="alert">{error}</p>}
      </section>

      <section className={surfaceClass}>
        <div className="mb-6 flex items-center justify-between gap-5">
          <div>
            <p className={eyebrowClass}>Configuration</p>
            <h2 className="m-0 text-[23px] font-semibold tracking-[-0.035em] text-[#18231f]">{integration ? 'Replace connection' : 'Connect HubSpot'}</h2>
          </div>
        </div>
        {canManage ? (
          <form className="grid gap-4.5" onSubmit={handleSave}>
            <p className="m-0 leading-relaxed text-[#74807c]">Use a HubSpot private-app token with company read access. The backend validates and encrypts it.</p>
            <label className={labelClass}>Private-app access token<input className={inputClass} name="access_token" type="password" placeholder="pat-…" autoComplete="off" required /></label>
            <button className={primaryButtonClass} type="submit" disabled={busy}>{busy ? 'Checking…' : 'Save connection'}</button>
          </form>
        ) : (
          <EmptyState title="View-only access" body="An owner or admin can update HubSpot credentials and start a sync." />
        )}
      </section>
    </div>
  )
}

function FindingPanel({ finding, onClose }: { finding: Finding; onClose: () => void }) {
  return (
    <div className="fixed inset-0 z-50 flex justify-end bg-[rgba(9,18,15,0.45)] backdrop-blur-[2px]" role="presentation" onMouseDown={onClose}>
      <aside className="relative w-full max-w-[520px] overflow-y-auto bg-[#fffefa] px-6 py-11 shadow-[-20px_0_60px_rgba(12,22,18,0.15)] sm:px-9.5" role="dialog" aria-modal="true" aria-labelledby="finding-title" onMouseDown={(event) => event.stopPropagation()}>
        <button className="absolute top-4.5 right-5 grid size-[34px] cursor-pointer place-items-center rounded-[10px] border border-[#dcddd8] bg-white text-[22px] text-[#53615c]" type="button" onClick={onClose} aria-label="Close finding">×</button>
        <p className={eyebrowClass}>{finding.risk} risk · {finding.status}</p>
        <h2 className="m-0 pr-7.5 text-3xl font-semibold tracking-[-0.04em] text-[#15211d]" id="finding-title">{finding.title}</h2>
        <p className="my-4.5 mb-7 leading-relaxed text-[#596761]">{finding.explanation}</p>
        <dl className="m-0 grid">
          <div className="grid grid-cols-[92px_minmax(0,1fr)] gap-4 border-t border-[#eceae4] py-3 sm:grid-cols-[120px_minmax(0,1fr)]"><dt className="text-[11px] text-[#7b8681]">Customer</dt><dd className="m-0 min-w-0 text-xs font-bold text-[#293732] break-words">{finding.customer_name}</dd></div>
          <div className="grid grid-cols-[92px_minmax(0,1fr)] gap-4 border-t border-[#eceae4] py-3 sm:grid-cols-[120px_minmax(0,1fr)]"><dt className="text-[11px] text-[#7b8681]">Rule</dt><dd className="m-0 min-w-0 text-xs font-bold text-[#293732] break-words">{ruleLabel(finding.rule_name)} · v{finding.rule_version}</dd></div>
          <div className="grid grid-cols-[92px_minmax(0,1fr)] gap-4 border-t border-[#eceae4] py-3 sm:grid-cols-[120px_minmax(0,1fr)]"><dt className="text-[11px] text-[#7b8681]">First detected</dt><dd className="m-0 min-w-0 text-xs font-bold text-[#293732] break-words">{formatDate(finding.first_detected_at)}</dd></div>
          <div className="grid grid-cols-[92px_minmax(0,1fr)] gap-4 border-t border-[#eceae4] py-3 sm:grid-cols-[120px_minmax(0,1fr)]"><dt className="text-[11px] text-[#7b8681]">Last detected</dt><dd className="m-0 min-w-0 text-xs font-bold text-[#293732] break-words">{formatDate(finding.last_detected_at)}</dd></div>
        </dl>
        <div className="mt-7.5">
          <p className={eyebrowClass}>Evidence</p>
          {(finding.evidence ?? []).map((evidence) => (
            <article className="mt-2.5 grid grid-cols-[minmax(0,1fr)_auto] gap-x-3 gap-y-2 rounded-xl border border-[#e2e3de] p-3.5" key={evidence.id}>
              <div><strong className="block text-xs capitalize">{evidence.source}</strong><span className="mt-1 block text-[10px] text-[#7a8581]">{evidence.fact_type}</span></div>
              <code className="row-span-2 self-center rounded-md bg-[#edf2ef] px-1.5 py-1 font-mono text-[10px] text-[#38594c]">{JSON.stringify(evidence.value)}</code>
              <time className="text-[10px] text-[#7a8581]">{formatDate(evidence.observed_at)}</time>
            </article>
          ))}
          {!finding.evidence?.length && <p className="m-0 leading-relaxed text-[#74807c]">No evidence was returned for this finding.</p>}
        </div>
      </aside>
    </div>
  )
}

function EmptyState({ title, body }: { title: string; body: string }) {
  return <div className="grid justify-items-center px-5 py-11 text-center text-[#7b8782]"><span className="mb-3 grid size-10.5 place-items-center rounded-full bg-[#edf1ee] text-xl text-[#557065]">◎</span><strong className="text-[13px] text-[#3d4a45]">{title}</strong><p className="mt-2 mb-0 max-w-[400px] text-xs leading-relaxed">{body}</p></div>
}

function LoadingRows() {
  return <div className={`${surfaceClass} grid gap-3.5`} aria-label="Loading workspace">{[0, 1, 2].map((row) => <span className="h-16 animate-pulse rounded-xl bg-[#f0efe9]" key={row} />)}</div>
}
